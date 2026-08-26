package store

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"neuroforge/internal/core"
)

const (
	vectorJournalMagicV1  = "NFVJ1\n"
	vectorJournalMagicV2  = "NFVJ2\n"
	vectorFrameTypeBlock  = 1
	vectorFrameFixedBytes = 15 // type+dim+count+method+predictor+rawLen+metaLen
)

type vectorJournalOptions struct {
	Compression   string
	BlockVectors  int
	MinBlockBytes int
	MinSavingsPct float64
}

func vectorJournalOptionsFromConfig(c core.Config) vectorJournalOptions {
	v := c.Storage.VectorJournal
	return vectorJournalOptions{
		Compression: v.Compression, BlockVectors: v.BlockVectors,
		MinBlockBytes: v.MinBlockBytes, MinSavingsPct: v.MinSavingsPct,
	}
}

func normalizeVectorJournalOptions(o vectorJournalOptions) vectorJournalOptions {
	if o.Compression == "" {
		o.Compression = "sqar-auto"
	}
	if o.BlockVectors <= 0 {
		o.BlockVectors = 128
	}
	if o.MinBlockBytes < 0 {
		o.MinBlockBytes = 0
	}
	if o.MinSavingsPct < 0 {
		o.MinSavingsPct = 0
	}
	return o
}

type VectorJournalStats struct {
	Records               int     `json:"records"`
	Bytes                 int64   `json:"bytes"`
	Format                string  `json:"format"`
	Blocks                int     `json:"blocks,omitempty"`
	CompressedBlocks      int     `json:"compressed_blocks,omitempty"`
	SQARBlocks            int     `json:"sqar_blocks,omitempty"`
	VectorRawBytes        int64   `json:"vector_raw_bytes,omitempty"`
	VectorStoredBytes     int64   `json:"vector_stored_bytes,omitempty"`
	CompressionSavingsPct float64 `json:"compression_savings_pct,omitempty"`
}

// VectorJournal is a rebuildable binary sidecar containing the immutable
// vector payload of newly-created memories. The authoritative copy remains in
// memory-segments; this sidecar exists so large disk-ANN rebuilds do not need
// to parse gigabytes of JSON just to recover float arrays.
//
// NFVJ2 groups equal-dimension vectors into independently compressed blocks.
// It preserves streaming iteration and lets a reader skip unrelated dimensions
// without inflating them. Existing NFVJ1 journals are read and upgraded
// atomically on open; if the optional upgrade fails, V1 remains usable.
type VectorJournal struct {
	mu                sync.Mutex
	path              string
	format            int
	opts              vectorJournalOptions
	records           int
	bytes             int64
	blocks            int
	compressedBlocks  int
	sqarBlocks        int
	vectorRawBytes    int64
	vectorStoredBytes int64
}

func openVectorJournal(path string, opts vectorJournalOptions) (*VectorJournal, error) {
	opts = normalizeVectorJournalOptions(opts)
	j := &VectorJournal{path: path, opts: opts}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if st.Size() == 0 {
		if _, err := f.WriteString(vectorJournalMagicV2); err != nil {
			_ = f.Close()
			return nil, err
		}
		_ = f.Close()
		j.format = 2
		j.bytes = int64(len(vectorJournalMagicV2))
		return j, nil
	}
	var head [len(vectorJournalMagicV2)]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		_ = f.Close()
		return nil, errors.New("invalid vector journal header")
	}
	magic := string(head[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	switch magic {
	case vectorJournalMagicV1:
		j.format = 1
		err = j.scanV1(f)
	case vectorJournalMagicV2:
		j.format = 2
		err = j.scanV2(f)
	default:
		err = errors.New("invalid vector journal header")
	}
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	if j.format == 1 {
		// V1 is already a rebuildable cache, so migration can be opportunistic.
		// Atomic rename guarantees that a failed conversion leaves the old file.
		if err := upgradeVectorJournalV1(path, opts); err == nil {
			return openVectorJournal(path, opts)
		}
	}
	return j, nil
}

func (j *VectorJournal) Configure(opts vectorJournalOptions) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.opts = normalizeVectorJournalOptions(opts)
	j.mu.Unlock()
}

func (j *VectorJournal) scanV1(f *os.File) error {
	if _, err := f.Seek(int64(len(vectorJournalMagicV1)), io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	pos := int64(len(vectorJournalMagicV1))
	var hdr [4]byte
	var prefix [12]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		n := int64(binary.LittleEndian.Uint32(hdr[:]))
		if n < 12 || n > maxSegmentRecordBytes {
			return fmt.Errorf("invalid vector journal record length %d", n)
		}
		if _, err := io.ReadFull(br, prefix[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		idLen := int64(binary.LittleEndian.Uint16(prefix[8:10]))
		dim := int64(binary.LittleEndian.Uint16(prefix[10:12]))
		if idLen == 0 || 12+idLen+dim*4 != n {
			return errors.New("invalid vector journal payload")
		}
		if _, err := io.CopyN(io.Discard, br, n-12); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		j.records++
		j.vectorRawBytes += dim * 4
		j.vectorStoredBytes += dim * 4
		pos += 4 + n
	}
	j.bytes = pos
	return nil
}

func (j *VectorJournal) scanV2(f *os.File) error {
	if _, err := f.Seek(int64(len(vectorJournalMagicV2)), io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	pos := int64(len(vectorJournalMagicV2))
	var lenBuf [4]byte
	var fixed [vectorFrameFixedBytes]byte
	for {
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		n := int64(binary.LittleEndian.Uint32(lenBuf[:]))
		if n < vectorFrameFixedBytes || n > maxSegmentRecordBytes {
			return fmt.Errorf("invalid vector journal frame length %d", n)
		}
		if _, err := io.ReadFull(br, fixed[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		if fixed[0] != vectorFrameTypeBlock {
			return fmt.Errorf("unknown vector journal frame type %d", fixed[0])
		}
		dim := int64(binary.LittleEndian.Uint16(fixed[1:3]))
		count := int64(binary.LittleEndian.Uint16(fixed[3:5]))
		method := vectorCodecMethod(fixed[5])
		rawLen := int64(binary.LittleEndian.Uint32(fixed[7:11]))
		metaLen := int64(binary.LittleEndian.Uint32(fixed[11:15]))
		if dim < 1 || count < 1 || rawLen != dim*count*4 || metaLen < count*10 || metaLen > n-vectorFrameFixedBytes {
			return errors.New("invalid vector journal frame header")
		}
		payloadLen := n - vectorFrameFixedBytes - metaLen
		if payloadLen <= 0 || (method == vectorCodecRaw && payloadLen != rawLen) || method > vectorCodecSQARColumn {
			return errors.New("invalid vector journal frame payload")
		}
		if _, err := io.CopyN(io.Discard, br, n-vectorFrameFixedBytes); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		j.records += int(count)
		j.blocks++
		j.vectorRawBytes += rawLen
		j.vectorStoredBytes += payloadLen
		if method != vectorCodecRaw {
			j.compressedBlocks++
		}
		if method == vectorCodecSQARColumn {
			j.sqarBlocks++
		}
		pos += 4 + n
	}
	j.bytes = pos
	return nil
}

type journalVectorRaw struct {
	revision uint64
	id       string
	dim      int
	raw      []byte
}

func (j *VectorJournal) AppendNew(revision uint64, memories []core.Memory) error {
	if j == nil || len(memories) == 0 {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.format == 1 {
		return j.appendV1Locked(revision, memories)
	}
	return j.appendV2Locked(revision, memories)
}

func (j *VectorJournal) appendV1Locked(revision uint64, memories []core.Memory) error {
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	added := 0
	var addedBytes, rawBytes int64
	var lenBuf [4]byte
	var revBuf [8]byte
	var short [2]byte
	var fb [4]byte
	for i := range memories {
		m := &memories[i]
		if m.ID == "" || len(m.Vector) == 0 {
			continue
		}
		if len(m.ID) > math.MaxUint16 || len(m.Vector) > math.MaxUint16 {
			_ = f.Close()
			return errors.New("memory id/vector dimension exceeds vector journal format")
		}
		payload := 8 + 2 + 2 + len(m.ID) + len(m.Vector)*4
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(payload))
		binary.LittleEndian.PutUint64(revBuf[:], revision)
		if _, err := bw.Write(lenBuf[:]); err != nil {
			_ = f.Close()
			return err
		}
		if _, err := bw.Write(revBuf[:]); err != nil {
			_ = f.Close()
			return err
		}
		binary.LittleEndian.PutUint16(short[:], uint16(len(m.ID)))
		if _, err := bw.Write(short[:]); err != nil {
			_ = f.Close()
			return err
		}
		binary.LittleEndian.PutUint16(short[:], uint16(len(m.Vector)))
		if _, err := bw.Write(short[:]); err != nil {
			_ = f.Close()
			return err
		}
		if _, err := bw.WriteString(m.ID); err != nil {
			_ = f.Close()
			return err
		}
		for _, x := range m.Vector {
			binary.LittleEndian.PutUint32(fb[:], math.Float32bits(x))
			if _, err := bw.Write(fb[:]); err != nil {
				_ = f.Close()
				return err
			}
		}
		added++
		addedBytes += int64(4 + payload)
		rawBytes += int64(len(m.Vector) * 4)
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	j.records += added
	j.bytes += addedBytes
	j.vectorRawBytes += rawBytes
	j.vectorStoredBytes += rawBytes
	return nil
}

func (j *VectorJournal) appendV2Locked(revision uint64, memories []core.Memory) error {
	groups := map[int][]journalVectorRaw{}
	for i := range memories {
		m := &memories[i]
		if m.ID == "" || len(m.Vector) == 0 {
			continue
		}
		if len(m.ID) > math.MaxUint16 || len(m.Vector) > math.MaxUint16 {
			return errors.New("memory id/vector dimension exceeds vector journal format")
		}
		raw := make([]byte, len(m.Vector)*4)
		for k, x := range m.Vector {
			binary.LittleEndian.PutUint32(raw[k*4:k*4+4], math.Float32bits(x))
		}
		dim := len(m.Vector)
		groups[dim] = append(groups[dim], journalVectorRaw{revision: revision, id: m.ID, dim: dim, raw: raw})
	}
	if len(groups) == 0 {
		return nil
	}
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	dims := make([]int, 0, len(groups))
	for dim := range groups {
		dims = append(dims, dim)
	}
	sort.Ints(dims)
	for _, dim := range dims {
		entries := groups[dim]
		for len(entries) > 0 {
			n := j.opts.BlockVectors
			if n > len(entries) {
				n = len(entries)
			}
			if n > math.MaxUint16 {
				n = math.MaxUint16
			}
			chunk := entries[:n]
			frameBytes, st, err := buildVectorFrame(chunk, j.opts)
			if err != nil {
				_ = f.Close()
				return err
			}
			if _, err := bw.Write(frameBytes); err != nil {
				_ = f.Close()
				return err
			}
			j.records += len(chunk)
			j.blocks++
			j.bytes += int64(len(frameBytes))
			j.vectorRawBytes += int64(st.rawBytes)
			j.vectorStoredBytes += int64(st.storedBytes)
			if st.method != vectorCodecRaw {
				j.compressedBlocks++
			}
			if st.method == vectorCodecSQARColumn {
				j.sqarBlocks++
			}
			entries = entries[n:]
		}
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	// The vector journal is rebuildable acceleration data. The WAL + segments
	// remain the durability boundary, so we intentionally avoid a second fsync.
	return f.Close()
}

type vectorFrameStat struct {
	method      vectorCodecMethod
	rawBytes    int
	storedBytes int
}

func buildVectorFrame(entries []journalVectorRaw, opts vectorJournalOptions) ([]byte, vectorFrameStat, error) {
	if len(entries) == 0 || len(entries) > math.MaxUint16 {
		return nil, vectorFrameStat{}, errors.New("invalid vector journal block size")
	}
	dim := entries[0].dim
	if dim < 1 || dim > math.MaxUint16 {
		return nil, vectorFrameStat{}, errors.New("invalid vector dimension")
	}
	metaLen, rawLen := 0, 0
	for _, e := range entries {
		if e.dim != dim || e.id == "" || len(e.id) > math.MaxUint16 || len(e.raw) != dim*4 {
			return nil, vectorFrameStat{}, errors.New("invalid vector journal block entry")
		}
		metaLen += 8 + 2 + len(e.id)
		rawLen += len(e.raw)
	}
	meta := make([]byte, 0, metaLen)
	raw := make([]byte, 0, rawLen)
	var b8 [8]byte
	var b2 [2]byte
	for _, e := range entries {
		binary.LittleEndian.PutUint64(b8[:], e.revision)
		meta = append(meta, b8[:]...)
		binary.LittleEndian.PutUint16(b2[:], uint16(len(e.id)))
		meta = append(meta, b2[:]...)
		meta = append(meta, e.id...)
		raw = append(raw, e.raw...)
	}
	enc := encodedVectorPayload{method: vectorCodecRaw, data: raw}
	if opts.Compression == "sqar-auto" && rawLen >= opts.MinBlockBytes {
		var err error
		enc, err = encodeVectorPayload(raw, dim*4, len(entries), true, opts.MinSavingsPct)
		if err != nil {
			return nil, vectorFrameStat{}, err
		}
	}
	frameLen := vectorFrameFixedBytes + len(meta) + len(enc.data)
	if frameLen > maxSegmentRecordBytes || frameLen > math.MaxUint32 {
		return nil, vectorFrameStat{}, fmt.Errorf("vector journal block exceeds %d bytes", maxSegmentRecordBytes)
	}
	out := make([]byte, 4+frameLen)
	binary.LittleEndian.PutUint32(out[:4], uint32(frameLen))
	fixed := out[4 : 4+vectorFrameFixedBytes]
	fixed[0] = vectorFrameTypeBlock
	binary.LittleEndian.PutUint16(fixed[1:3], uint16(dim))
	binary.LittleEndian.PutUint16(fixed[3:5], uint16(len(entries)))
	fixed[5] = byte(enc.method)
	fixed[6] = byte(enc.predictor)
	binary.LittleEndian.PutUint32(fixed[7:11], uint32(rawLen))
	binary.LittleEndian.PutUint32(fixed[11:15], uint32(len(meta)))
	copy(out[4+vectorFrameFixedBytes:], meta)
	copy(out[4+vectorFrameFixedBytes+len(meta):], enc.data)
	return out, vectorFrameStat{method: enc.method, rawBytes: rawLen, storedBytes: len(enc.data)}, nil
}

func (j *VectorJournal) Iterate(dim int, fn func(id string, vector []float32) error) error {
	if j == nil || fn == nil {
		return errors.New("vector journal iterator unavailable")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if dim < 1 || dim > math.MaxUint16 {
		return errors.New("vector journal dimension out of range")
	}
	if j.format == 1 {
		return j.iterateV1Locked(dim, fn)
	}
	return j.iterateV2Locked(dim, fn)
}

func (j *VectorJournal) iterateV1Locked(dim int, fn func(id string, vector []float32) error) error {
	f, err := os.Open(j.path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(int64(len(vectorJournalMagicV1)), io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	var hdr [4]byte
	var payload []byte
	var vec []float32
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := int(binary.LittleEndian.Uint32(hdr[:]))
		if n < 12 || n > maxSegmentRecordBytes {
			return fmt.Errorf("invalid vector journal record length %d", n)
		}
		if cap(payload) < n {
			payload = make([]byte, n)
		} else {
			payload = payload[:n]
		}
		if _, err := io.ReadFull(br, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		idLen := int(binary.LittleEndian.Uint16(payload[8:10]))
		vdim := int(binary.LittleEndian.Uint16(payload[10:12]))
		if 12+idLen+vdim*4 != len(payload) || idLen == 0 {
			return errors.New("invalid vector journal payload")
		}
		if vdim != dim {
			continue
		}
		if cap(vec) < vdim {
			vec = make([]float32, vdim)
		} else {
			vec = vec[:vdim]
		}
		base := 12 + idLen
		for i := range vec {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(payload[base+i*4 : base+i*4+4]))
		}
		if err := fn(string(payload[12:12+idLen]), vec); err != nil {
			return err
		}
	}
}

func (j *VectorJournal) iterateV2Locked(dim int, fn func(id string, vector []float32) error) error {
	f, err := os.Open(j.path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(int64(len(vectorJournalMagicV2)), io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	var lenBuf [4]byte
	var fixed [vectorFrameFixedBytes]byte
	var tail []byte
	var vec []float32
	for {
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		if n < vectorFrameFixedBytes || n > maxSegmentRecordBytes {
			return fmt.Errorf("invalid vector journal frame length %d", n)
		}
		if _, err := io.ReadFull(br, fixed[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		if fixed[0] != vectorFrameTypeBlock {
			return fmt.Errorf("unknown vector journal frame type %d", fixed[0])
		}
		vdim := int(binary.LittleEndian.Uint16(fixed[1:3]))
		count := int(binary.LittleEndian.Uint16(fixed[3:5]))
		method := vectorCodecMethod(fixed[5])
		predictor := vectorPredictor(fixed[6])
		rawLen := int(binary.LittleEndian.Uint32(fixed[7:11]))
		metaLen := int(binary.LittleEndian.Uint32(fixed[11:15]))
		remaining := n - vectorFrameFixedBytes
		if vdim < 1 || count < 1 || rawLen != vdim*count*4 || metaLen < count*10 || metaLen > remaining || method > vectorCodecSQARColumn {
			return errors.New("invalid vector journal frame header")
		}
		if vdim != dim {
			if _, err := io.CopyN(io.Discard, br, int64(remaining)); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return nil
				}
				return err
			}
			continue
		}
		if cap(tail) < remaining {
			tail = make([]byte, remaining)
		} else {
			tail = tail[:remaining]
		}
		if _, err := io.ReadFull(br, tail); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		meta, payload := tail[:metaLen], tail[metaLen:]
		ids := make([]string, 0, count)
		pos := 0
		for i := 0; i < count; i++ {
			if pos+10 > len(meta) {
				return errors.New("truncated vector journal metadata")
			}
			idLen := int(binary.LittleEndian.Uint16(meta[pos+8 : pos+10]))
			pos += 10
			if idLen < 1 || pos+idLen > len(meta) {
				return errors.New("invalid vector journal id")
			}
			ids = append(ids, string(meta[pos:pos+idLen]))
			pos += idLen
		}
		if pos != len(meta) {
			return errors.New("vector journal metadata trailing bytes")
		}
		raw, err := decodeVectorPayload(encodedVectorPayload{method: method, predictor: predictor, data: payload}, vdim*4, count)
		if err != nil {
			return fmt.Errorf("decode vector journal block: %w", err)
		}
		if cap(vec) < vdim {
			vec = make([]float32, vdim)
		} else {
			vec = vec[:vdim]
		}
		for row, id := range ids {
			base := row * vdim * 4
			for i := range vec {
				vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[base+i*4 : base+i*4+4]))
			}
			if err := fn(id, vec); err != nil {
				return err
			}
		}
	}
}

func upgradeVectorJournalV1(path string, opts vectorJournalOptions) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	head := make([]byte, len(vectorJournalMagicV1))
	if _, err := io.ReadFull(src, head); err != nil || string(head) != vectorJournalMagicV1 {
		return errors.New("not an NFVJ1 journal")
	}
	tmp := path + ".v2tmp"
	_ = os.Remove(tmp)
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = dst.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	bw := bufio.NewWriterSize(dst, 1<<20)
	if _, err := bw.WriteString(vectorJournalMagicV2); err != nil {
		return err
	}
	pending := map[int][]journalVectorRaw{}
	flushDim := func(dim int) error {
		entries := pending[dim]
		for len(entries) > 0 {
			n := opts.BlockVectors
			if n > len(entries) {
				n = len(entries)
			}
			frame, _, err := buildVectorFrame(entries[:n], opts)
			if err != nil {
				return err
			}
			if _, err := bw.Write(frame); err != nil {
				return err
			}
			entries = entries[n:]
		}
		pending[dim] = pending[dim][:0]
		return nil
	}
	br := bufio.NewReaderSize(src, 1<<20)
	var lenBuf [4]byte
	for {
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		n := int(binary.LittleEndian.Uint32(lenBuf[:]))
		if n < 12 || n > maxSegmentRecordBytes {
			return fmt.Errorf("invalid V1 record length %d", n)
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return err
		}
		rev := binary.LittleEndian.Uint64(payload[:8])
		idLen := int(binary.LittleEndian.Uint16(payload[8:10]))
		dim := int(binary.LittleEndian.Uint16(payload[10:12]))
		if idLen < 1 || 12+idLen+dim*4 != len(payload) {
			return errors.New("invalid V1 vector payload")
		}
		id := string(payload[12 : 12+idLen])
		raw := append([]byte(nil), payload[12+idLen:]...)
		pending[dim] = append(pending[dim], journalVectorRaw{revision: rev, id: id, dim: dim, raw: raw})
		if len(pending[dim]) >= opts.BlockVectors {
			if err := flushDim(dim); err != nil {
				return err
			}
		}
	}
	dims := make([]int, 0, len(pending))
	for dim := range pending {
		dims = append(dims, dim)
	}
	sort.Ints(dims)
	for _, dim := range dims {
		if err := flushDim(dim); err != nil {
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// Best-effort directory sync makes the atomic replacement durable on Unix.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	ok = true
	return nil
}

func (j *VectorJournal) Stats() VectorJournalStats {
	if j == nil {
		return VectorJournalStats{}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	format := "NFVJ1"
	if j.format == 2 {
		format = "NFVJ2"
	}
	saved := 0.0
	if j.vectorRawBytes > 0 && j.vectorStoredBytes < j.vectorRawBytes {
		saved = float64(j.vectorRawBytes-j.vectorStoredBytes) / float64(j.vectorRawBytes) * 100
	}
	return VectorJournalStats{
		Records: j.records, Bytes: j.bytes, Format: format, Blocks: j.blocks,
		CompressedBlocks: j.compressedBlocks, SQARBlocks: j.sqarBlocks,
		VectorRawBytes: j.vectorRawBytes, VectorStoredBytes: j.vectorStoredBytes,
		CompressionSavingsPct: saved,
	}
}

func (s *Store) VectorJournalStats() VectorJournalStats {
	s.mu.RLock()
	j := s.vectorJournal
	s.mu.RUnlock()
	if j == nil {
		return VectorJournalStats{}
	}
	return j.Stats()
}
