package store

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
)

// The vector-journal codec is a focused migration of the useful part of the
// SQAR PoC: expose 2D row/column structure to DEFLATE, but keep the search
// bounded because this path sits on ingestion and index-rebuild hot paths.
//
// Vectors are laid out as rows of dim*4 bytes. We compare plain DEFLATE with a
// column traversal of reversible residuals and keep only a net-positive result.
type vectorCodecMethod uint8

const (
	vectorCodecRaw vectorCodecMethod = iota
	vectorCodecDeflate
	vectorCodecSQARColumn
)

type vectorPredictor uint8

const (
	vectorPredictorNone vectorPredictor = iota
	vectorPredictorTop
	vectorPredictorXOR2D
	vectorPredictorPaeth
)

type encodedVectorPayload struct {
	method    vectorCodecMethod
	predictor vectorPredictor
	data      []byte
}

func deflateVectorBytes(src []byte) ([]byte, error) {
	var b bytes.Buffer
	w, err := flate.NewWriter(&b, 6)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(src); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func inflateVectorBytes(src []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(src))
	defer r.Close()
	return io.ReadAll(r)
}

func encodeVectorPayload(src []byte, width, rows int, enableSQAR bool, minSavingsPct float64) (encodedVectorPayload, error) {
	if width <= 0 || rows <= 0 || len(src) != width*rows {
		return encodedVectorPayload{}, errors.New("invalid vector block geometry")
	}
	best := encodedVectorPayload{method: vectorCodecRaw, data: append([]byte(nil), src...)}
	z, err := deflateVectorBytes(src)
	if err != nil {
		return encodedVectorPayload{}, err
	}
	if len(z) < len(best.data) {
		best = encodedVectorPayload{method: vectorCodecDeflate, data: z}
	}
	if enableSQAR {
		for _, p := range []vectorPredictor{vectorPredictorNone, vectorPredictorTop, vectorPredictorXOR2D, vectorPredictorPaeth} {
			residual := makeVectorResidual(src, width, rows, p)
			column := serializeVectorColumns(residual, width, rows)
			candidate, err := deflateVectorBytes(column)
			if err != nil {
				return encodedVectorPayload{}, err
			}
			if len(candidate) < len(best.data) {
				best = encodedVectorPayload{method: vectorCodecSQARColumn, predictor: p, data: candidate}
			}
		}
	}
	// Compression is optional and must earn its CPU/format cost. Compare against
	// the original vector payload, not just the DEFLATE baseline.
	if best.method != vectorCodecRaw && minSavingsPct > 0 {
		saved := float64(len(src)-len(best.data)) / float64(len(src))
		if saved < minSavingsPct {
			return encodedVectorPayload{method: vectorCodecRaw, data: append([]byte(nil), src...)}, nil
		}
	}
	return best, nil
}

func decodeVectorPayload(enc encodedVectorPayload, width, rows int) ([]byte, error) {
	want := width * rows
	switch enc.method {
	case vectorCodecRaw:
		if len(enc.data) != want {
			return nil, fmt.Errorf("raw vector block length=%d want=%d", len(enc.data), want)
		}
		return append([]byte(nil), enc.data...), nil
	case vectorCodecDeflate:
		out, err := inflateVectorBytes(enc.data)
		if err != nil {
			return nil, err
		}
		if len(out) != want {
			return nil, fmt.Errorf("deflated vector block length=%d want=%d", len(out), want)
		}
		return out, nil
	case vectorCodecSQARColumn:
		column, err := inflateVectorBytes(enc.data)
		if err != nil {
			return nil, err
		}
		if len(column) != want {
			return nil, fmt.Errorf("SQAR column length=%d want=%d", len(column), want)
		}
		residual := deserializeVectorColumns(column, width, rows)
		return restoreVectorResidual(residual, width, rows, enc.predictor), nil
	default:
		return nil, fmt.Errorf("unknown vector codec method %d", enc.method)
	}
}

func makeVectorResidual(src []byte, width, rows int, p vectorPredictor) []byte {
	out := make([]byte, len(src))
	for r := 0; r < rows; r++ {
		for c := 0; c < width; c++ {
			i := r*width + c
			out[i] = src[i] ^ vectorPredictorValue(src, width, r, c, p)
		}
	}
	return out
}

func restoreVectorResidual(res []byte, width, rows int, p vectorPredictor) []byte {
	out := make([]byte, len(res))
	for r := 0; r < rows; r++ {
		for c := 0; c < width; c++ {
			i := r*width + c
			out[i] = res[i] ^ vectorPredictorValue(out, width, r, c, p)
		}
	}
	return out
}

func vectorPredictorValue(buf []byte, width, r, c int, p vectorPredictor) byte {
	var left, top, topLeft byte
	if c > 0 {
		left = buf[r*width+c-1]
	}
	if r > 0 {
		top = buf[(r-1)*width+c]
		if c > 0 {
			topLeft = buf[(r-1)*width+c-1]
		}
	}
	switch p {
	case vectorPredictorNone:
		return 0
	case vectorPredictorTop:
		return top
	case vectorPredictorXOR2D:
		return left ^ top ^ topLeft
	case vectorPredictorPaeth:
		return paethByte(left, top, topLeft)
	default:
		return 0
	}
}

func paethByte(a, b, c byte) byte {
	ai, bi, ci := int(a), int(b), int(c)
	p := ai + bi - ci
	pa, pb, pc := absIntStore(p-ai), absIntStore(p-bi), absIntStore(p-ci)
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func absIntStore(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func serializeVectorColumns(src []byte, width, rows int) []byte {
	out := make([]byte, len(src))
	k := 0
	for c := 0; c < width; c++ {
		for r := 0; r < rows; r++ {
			out[k] = src[r*width+c]
			k++
		}
	}
	return out
}

func deserializeVectorColumns(src []byte, width, rows int) []byte {
	out := make([]byte, len(src))
	k := 0
	for c := 0; c < width; c++ {
		for r := 0; r < rows; r++ {
			out[r*width+c] = src[k]
			k++
		}
	}
	return out
}
