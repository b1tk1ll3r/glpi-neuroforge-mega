package vector

import (
	"bufio"
	"container/heap"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

// PQConfig configures the disk-backed IVF-PQ index. Vectors are assumed to be
// cosine-search vectors and are normalized before training/encoding.
type PQConfig struct {
	Partitions      int `json:"partitions"`
	ProbePartitions int `json:"probe_partitions"`
	Subquantizers   int `json:"subquantizers"`
	Centroids       int `json:"centroids"`
	TrainingSamples int `json:"training_samples"`
	KMeansIters     int `json:"kmeans_iters"`
	BuildWorkers    int `json:"build_workers"`
}

type PQBuildStats struct {
	Dimension       int           `json:"dimension"`
	Items           int           `json:"items"`
	Partitions      int           `json:"partitions"`
	Subquantizers   int           `json:"subquantizers"`
	Centroids       int           `json:"centroids"`
	TrainingSamples int           `json:"training_samples"`
	Bytes           int64         `json:"bytes"`
	Duration        time.Duration `json:"duration"`
}

type PQHit struct {
	ID         string  `json:"id"`
	Similarity float64 `json:"similarity"`
}

type pqManifest struct {
	Version   int `json:"version"`
	Dimension int `json:"dimension"`
	// Count is the ordinal/ID-table size. Indexed is the number of records
	// actually present in partition files. They may differ when a vector is
	// deleted while a lock-free rebuild is in progress.
	Count          int           `json:"count"`
	Indexed        int           `json:"indexed,omitempty"`
	Config         PQConfig      `json:"config"`
	Coarse         [][]float32   `json:"coarse"`
	Codebooks      [][][]float32 `json:"codebooks"`
	PartitionCount []int         `json:"partition_count"`
}

type PQIndex struct {
	dir       string
	manifest  pqManifest
	files     []*os.File
	sizes     []int64
	idFile    *os.File
	idOffsets []uint64
}

func defaultPQConfig(cfg PQConfig, dim int) PQConfig {
	if cfg.Partitions < 2 {
		cfg.Partitions = 128
	}
	if cfg.ProbePartitions < 1 {
		cfg.ProbePartitions = 48
	}
	if cfg.ProbePartitions > cfg.Partitions {
		cfg.ProbePartitions = cfg.Partitions
	}
	if cfg.Subquantizers < 1 {
		cfg.Subquantizers = 16
	}
	if cfg.Subquantizers > dim {
		cfg.Subquantizers = dim
	}
	if cfg.Centroids < 2 {
		cfg.Centroids = 128
	}
	if cfg.Centroids > 256 {
		cfg.Centroids = 256
	}
	if cfg.TrainingSamples < cfg.Centroids*4 {
		cfg.TrainingSamples = 8192
	}
	if cfg.KMeansIters < 1 {
		cfg.KMeansIters = 6
	}
	if cfg.BuildWorkers < 1 {
		cfg.BuildWorkers = runtime.GOMAXPROCS(0)
	}
	if cfg.BuildWorkers > 64 {
		cfg.BuildWorkers = 64
	}
	return cfg
}

func partitionPath(dir string, p int) string {
	return filepath.Join(dir, fmt.Sprintf("part-%04d.pq", p))
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func l2norm(v []float32) {
	var s float64
	for _, x := range v {
		s += float64(x * x)
	}
	if s == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(s))
	for i := range v {
		v[i] *= inv
	}
}

func sqDist(a, b []float32) float32 {
	var s float32
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s
}

func nearest(v []float32, centers [][]float32) int {
	best := 0
	bestD := float32(math.MaxFloat32)
	for i, c := range centers {
		d := sqDist(v, c)
		if d < bestD {
			bestD, best = d, i
		}
	}
	return best
}

// deterministicKMeans intentionally avoids random initialization. A stable,
// farthest-point seed makes rebuilds reproducible and sufficiently diverse for
// the IVF/PQ coarse codebooks used here.
func deterministicKMeans(samples [][]float32, k, iters int) [][]float32 {
	if len(samples) == 0 {
		return nil
	}
	if k > len(samples) {
		k = len(samples)
	}
	dim := len(samples[0])
	centers := make([][]float32, 0, k)
	centers = append(centers, append([]float32(nil), samples[0]...))
	minDist := make([]float32, len(samples))
	for i := range minDist {
		minDist[i] = float32(math.MaxFloat32)
	}
	for len(centers) < k {
		last := centers[len(centers)-1]
		far, farD := 0, float32(-1)
		for i, s := range samples {
			d := sqDist(s, last)
			if d < minDist[i] {
				minDist[i] = d
			}
			if minDist[i] > farD {
				farD, far = minDist[i], i
			}
		}
		centers = append(centers, append([]float32(nil), samples[far]...))
	}
	assign := make([]int, len(samples))
	for iter := 0; iter < iters; iter++ {
		sums := make([][]float64, k)
		counts := make([]int, k)
		for i := range sums {
			sums[i] = make([]float64, dim)
		}
		changed := false
		for i, s := range samples {
			a := nearest(s, centers)
			if iter == 0 || assign[i] != a {
				changed = true
				assign[i] = a
			}
			counts[a]++
			for d, x := range s {
				sums[a][d] += float64(x)
			}
		}
		for c := 0; c < k; c++ {
			if counts[c] == 0 {
				continue
			}
			inv := 1 / float64(counts[c])
			for d := 0; d < dim; d++ {
				centers[c][d] = float32(sums[c][d] * inv)
			}
		}
		if !changed {
			break
		}
	}
	return centers
}

func subBounds(dim, m, sub int) (int, int) {
	base, rem := dim/m, dim%m
	start := sub*base + minIntPQ(sub, rem)
	n := base
	if sub < rem {
		n++
	}
	return start, start + n
}
func minIntPQ(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func residual(v, coarse []float32) []float32 {
	out := make([]float32, len(v))
	for i := range v {
		out[i] = v[i] - coarse[i]
	}
	return out
}

func trainPQ(samples [][]float32, coarse [][]float32, m, ks, iters int) [][][]float32 {
	residuals := make([][]float32, len(samples))
	for i, v := range samples {
		c := nearest(v, coarse)
		residuals[i] = residual(v, coarse[c])
	}
	books := make([][][]float32, m)
	dim := len(samples[0])
	workers := minIntPQ(m, maxIntPQ(1, runtime.GOMAXPROCS(0)))
	jobs := make(chan int, m)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sub := range jobs {
				a, b := subBounds(dim, m, sub)
				subv := make([][]float32, len(residuals))
				for i, r := range residuals {
					subv[i] = append([]float32(nil), r[a:b]...)
				}
				books[sub] = deterministicKMeans(subv, ks, iters)
			}
		}()
	}
	for sub := 0; sub < m; sub++ {
		jobs <- sub
	}
	close(jobs)
	wg.Wait()
	return books
}

func maxIntPQ(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func encodePQInto(v, coarse []float32, books [][][]float32, out []byte) {
	m := len(books)
	for sub, book := range books {
		a, b := subBounds(len(v), m, sub)
		best := 0
		bestD := float32(math.MaxFloat32)
		for ci, cent := range book {
			var d float32
			for j := a; j < b; j++ {
				r := v[j] - coarse[j]
				x := r - cent[j-a]
				d += x * x
			}
			if d < bestD {
				bestD, best = d, ci
			}
		}
		out[sub] = byte(best)
	}
}

type pqRecord struct {
	ordinal uint32
	codeLen uint16
	codes   [256]byte
}

// PQVectorIterator streams source vectors to the builder. The callback vector
// is copied before asynchronous encoding, so callers may reuse their storage
// after yield returns.
type PQVectorIterator func(yield func(id string, vector []float32) error) error

func trainPQModel(dim int, cfg PQConfig, ids []string, getVector func(string) ([]float32, bool)) (PQConfig, [][]float32, [][][]float32, int, error) {
	cfg = defaultPQConfig(cfg, dim)
	if len(ids) == 0 || getVector == nil {
		return cfg, nil, nil, 0, errors.New("no vectors for PQ training")
	}
	want := cfg.TrainingSamples
	if want > len(ids) {
		want = len(ids)
	}
	samples := make([][]float32, 0, want)
	step := float64(len(ids)) / float64(want)
	for i := 0; i < want; i++ {
		pos := int(float64(i) * step)
		if pos >= len(ids) {
			pos = len(ids) - 1
		}
		v, ok := getVector(ids[pos])
		if !ok || len(v) != dim {
			continue
		}
		cp := append([]float32(nil), v...)
		l2norm(cp)
		samples = append(samples, cp)
	}
	if len(samples) < 2 {
		return cfg, nil, nil, len(samples), errors.New("not enough valid vectors for PQ training")
	}
	if cfg.Partitions > len(samples) {
		cfg.Partitions = len(samples)
	}
	if cfg.Centroids > len(samples) {
		cfg.Centroids = len(samples)
	}
	if cfg.ProbePartitions > cfg.Partitions {
		cfg.ProbePartitions = cfg.Partitions
	}
	coarse := deterministicKMeans(samples, cfg.Partitions, cfg.KMeansIters)
	books := trainPQ(samples, coarse, cfg.Subquantizers, cfg.Centroids, cfg.KMeansIters)
	return cfg, coarse, books, len(samples), nil
}

// BuildPQIndex builds a complete IVF-PQ index from an ID list. It remains the
// convenient in-memory/random-access API used by tests and small stores.
func BuildPQIndex(dir string, dim int, cfg PQConfig, ids []string, getVector func(string) ([]float32, bool)) (PQBuildStats, error) {
	return BuildPQIndexStream(dir, dim, cfg, ids, getVector, func(yield func(string, []float32) error) error {
		for _, id := range ids {
			v, ok := getVector(id)
			if !ok || len(v) != dim {
				continue
			}
			if err := yield(id, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// BuildPQIndexStream trains from a bounded sample but performs the full encode
// pass through a sequential iterator. This is the production path for cold
// segment stores: it avoids one random segment lookup/open per memory and keeps
// build memory bounded independently of the number of vectors.
func BuildPQIndexStream(dir string, dim int, cfg PQConfig, sampleIDs []string, getSampleVector func(string) ([]float32, bool), iterate PQVectorIterator) (PQBuildStats, error) {
	start := time.Now()
	if dim < 2 {
		return PQBuildStats{}, errors.New("PQ dimension must be >= 2")
	}
	if iterate == nil {
		return PQBuildStats{}, errors.New("PQ vector iterator required")
	}
	cfg, coarse, books, trainingCount, err := trainPQModel(dim, cfg, sampleIDs, getSampleVector)
	if err != nil {
		return PQBuildStats{}, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return PQBuildStats{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return PQBuildStats{}, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()

	type writer struct {
		f     *os.File
		bw    *bufio.Writer
		ch    chan pqRecord
		count int
		err   error
	}
	writers := make([]*writer, cfg.Partitions)
	for part := 0; part < cfg.Partitions; part++ {
		f, err := os.OpenFile(partitionPath(dir, part), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			for i := 0; i < part; i++ {
				_ = writers[i].f.Close()
			}
			return PQBuildStats{}, err
		}
		writers[part] = &writer{f: f, bw: bufio.NewWriterSize(f, 1<<20), ch: make(chan pqRecord, 256)}
	}
	var writerWG sync.WaitGroup
	for _, w := range writers {
		writerWG.Add(1)
		go func(w *writer) {
			defer writerWG.Done()
			var ord [4]byte
			for rec := range w.ch {
				if w.err != nil {
					continue
				}
				binary.LittleEndian.PutUint32(ord[:], rec.ordinal)
				if _, err := w.bw.Write(ord[:]); err != nil {
					w.err = err
					continue
				}
				if _, err := w.bw.Write(rec.codes[:rec.codeLen]); err != nil {
					w.err = err
					continue
				}
				w.count++
			}
			if err := w.bw.Flush(); w.err == nil && err != nil {
				w.err = err
			}
			if err := w.f.Sync(); w.err == nil && err != nil {
				w.err = err
			}
			if err := w.f.Close(); w.err == nil && err != nil {
				w.err = err
			}
		}(w)
	}

	idf, err := os.OpenFile(filepath.Join(dir, "ids.bin"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		for _, w := range writers {
			close(w.ch)
		}
		writerWG.Wait()
		return PQBuildStats{}, err
	}
	off, err := os.OpenFile(filepath.Join(dir, "id-offsets.bin"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		_ = idf.Close()
		for _, w := range writers {
			close(w.ch)
		}
		writerWG.Wait()
		return PQBuildStats{}, err
	}
	idbw := bufio.NewWriterSize(idf, 1<<20)
	offbw := bufio.NewWriterSize(off, 1<<20)
	var idPos uint64
	var ordinal uint32
	var ob [8]byte
	var lb [2]byte

	type buildJob struct {
		ordinal uint32
		vector  []float32
	}
	jobs := make(chan buildJob, cfg.BuildWorkers*2)
	var vectorPool sync.Pool
	vectorPool.New = func() any { return make([]float32, dim) }
	var encWG sync.WaitGroup
	for wi := 0; wi < cfg.BuildWorkers; wi++ {
		encWG.Add(1)
		go func() {
			defer encWG.Done()
			for job := range jobs {
				v := job.vector
				l2norm(v)
				part := nearest(v, coarse)
				var rec pqRecord
				rec.ordinal = job.ordinal
				rec.codeLen = uint16(len(books))
				encodePQInto(v, coarse[part], books, rec.codes[:rec.codeLen])
				writers[part].ch <- rec
				vectorPool.Put(v)
			}
		}()
	}

	iterErr := iterate(func(id string, v []float32) error {
		if id == "" || len(v) != dim {
			return nil
		}
		if len(id) > math.MaxUint16 {
			return errors.New("memory id too long for PQ index")
		}
		if ordinal == math.MaxUint32 {
			return errors.New("PQ index supports at most uint32 ordinals per dimension")
		}
		binary.LittleEndian.PutUint64(ob[:], idPos)
		if _, err := offbw.Write(ob[:]); err != nil {
			return err
		}
		binary.LittleEndian.PutUint16(lb[:], uint16(len(id)))
		if _, err := idbw.Write(lb[:]); err != nil {
			return err
		}
		if _, err := idbw.WriteString(id); err != nil {
			return err
		}
		idPos += uint64(2 + len(id))
		cp := vectorPool.Get().([]float32)
		if cap(cp) < dim {
			cp = make([]float32, dim)
		} else {
			cp = cp[:dim]
		}
		copy(cp, v)
		jobs <- buildJob{ordinal: ordinal, vector: cp}
		ordinal++
		return nil
	})
	close(jobs)
	encWG.Wait()
	for _, w := range writers {
		close(w.ch)
	}
	writerWG.Wait()

	closeIDs := func() error {
		if err := idbw.Flush(); err != nil {
			return err
		}
		if err := offbw.Flush(); err != nil {
			return err
		}
		if err := idf.Sync(); err != nil {
			return err
		}
		if err := off.Sync(); err != nil {
			return err
		}
		if err := idf.Close(); err != nil {
			return err
		}
		return off.Close()
	}
	if err := closeIDs(); err != nil {
		return PQBuildStats{}, err
	}
	if iterErr != nil {
		return PQBuildStats{}, iterErr
	}

	counts := make([]int, len(writers))
	total := 0
	for i, w := range writers {
		if w.err != nil {
			return PQBuildStats{}, w.err
		}
		counts[i] = w.count
		total += w.count
	}
	if total == 0 || total != int(ordinal) {
		return PQBuildStats{}, fmt.Errorf("PQ encode count mismatch: encoded=%d ids=%d", total, ordinal)
	}
	man := pqManifest{Version: 2, Dimension: dim, Count: total, Indexed: total, Config: cfg, Coarse: coarse, Codebooks: books, PartitionCount: counts}
	if err := writeJSONAtomic(filepath.Join(dir, "manifest.json"), &man); err != nil {
		return PQBuildStats{}, err
	}
	var bytes int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			bytes += info.Size()
		}
		return nil
	})
	success = true
	return PQBuildStats{Dimension: dim, Items: total, Partitions: cfg.Partitions, Subquantizers: cfg.Subquantizers, Centroids: cfg.Centroids, TrainingSamples: trainingCount, Bytes: bytes, Duration: time.Since(start)}, nil
}

func OpenPQIndex(dir string) (*PQIndex, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var man pqManifest
	if err := json.Unmarshal(b, &man); err != nil {
		return nil, err
	}
	if man.Version != 2 || man.Dimension < 2 || len(man.Coarse) == 0 || len(man.Codebooks) == 0 {
		return nil, errors.New("invalid PQ manifest")
	}
	if man.Indexed == 0 {
		// Backward-compatible with early v0.6 development indexes.
		man.Indexed = man.Count
	}
	if man.Indexed < 0 || man.Indexed > man.Count {
		return nil, errors.New("invalid PQ indexed count")
	}
	idx := &PQIndex{dir: dir, manifest: man, files: make([]*os.File, len(man.Coarse)), sizes: make([]int64, len(man.Coarse))}
	idf, err := os.Open(filepath.Join(dir, "ids.bin"))
	if err != nil {
		return nil, err
	}
	idx.idFile = idf
	offb, err := os.ReadFile(filepath.Join(dir, "id-offsets.bin"))
	if err != nil {
		idx.Close()
		return nil, err
	}
	if len(offb) != man.Count*8 {
		idx.Close()
		return nil, errors.New("invalid PQ id offset table")
	}
	idx.idOffsets = make([]uint64, man.Count)
	for i := range idx.idOffsets {
		idx.idOffsets[i] = binary.LittleEndian.Uint64(offb[i*8 : i*8+8])
	}
	for p := range idx.files {
		f, err := os.Open(partitionPath(dir, p))
		if err != nil {
			idx.Close()
			return nil, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			idx.Close()
			return nil, err
		}
		idx.files[p], idx.sizes[p] = f, st.Size()
	}
	return idx, nil
}
func (p *PQIndex) Close() error {
	var first error
	if p.idFile != nil {
		if err := p.idFile.Close(); err != nil {
			first = err
		}
		p.idFile = nil
	}
	for i, f := range p.files {
		if f != nil {
			if err := f.Close(); first == nil && err != nil {
				first = err
			}
			p.files[i] = nil
		}
	}
	return first
}
func (p *PQIndex) Len() int         { return p.manifest.Indexed }
func (p *PQIndex) Dimension() int   { return p.manifest.Dimension }
func (p *PQIndex) Config() PQConfig { return p.manifest.Config }
func (p *PQIndex) DiskBytes() int64 {
	var n int64
	for _, s := range p.sizes {
		n += s
	}
	for _, name := range []string{"manifest.json", "ids.bin", "id-offsets.bin"} {
		if st, err := os.Stat(filepath.Join(p.dir, name)); err == nil {
			n += st.Size()
		}
	}
	return n
}

func dotPQ(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

type pqHeapItem struct {
	ordinal uint32
	sim     float32
}
type pqMinHeap []pqHeapItem

func (h pqMinHeap) Len() int           { return len(h) }
func (h pqMinHeap) Less(i, j int) bool { return h[i].sim < h[j].sim }
func (h pqMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *pqMinHeap) Push(x any)        { *h = append(*h, x.(pqHeapItem)) }
func (h *pqMinHeap) Pop() any          { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }
func pushTopPQ(h *pqMinHeap, capN int, x pqHeapItem) {
	if capN <= 0 {
		return
	}
	if h.Len() < capN {
		heap.Push(h, x)
		return
	}
	if (*h)[0].sim < x.sim {
		(*h)[0] = x
		heap.Fix(h, 0)
	}
}

func buildPQLookup(q []float32, books [][][]float32) [][]float32 {
	m := len(books)
	lookup := make([][]float32, m)
	for sub, book := range books {
		a, b := subBounds(len(q), m, sub)
		row := make([]float32, len(book))
		for c, cent := range book {
			row[c] = dotPQ(q[a:b], cent)
		}
		lookup[sub] = row
	}
	return lookup
}

func (p *PQIndex) scanPartition(part int, q []float32, lookup [][]float32, keep int) ([]pqHeapItem, error) {
	if part < 0 || part >= len(p.files) {
		return nil, errors.New("invalid PQ partition")
	}
	base := dotPQ(q, p.manifest.Coarse[part])
	m := len(p.manifest.Codebooks)
	// A small sequential buffer is enough because partition files are compact
	// fixed-width records and normally served by the OS page cache. Keeping this
	// at 64 KiB prevents a single query from allocating ProbePartitions MiB.
	r := bufio.NewReaderSize(io.NewSectionReader(p.files[part], 0, p.sizes[part]), 64<<10)
	h := &pqMinHeap{}
	heap.Init(h)
	rec := make([]byte, 4+m)
	for {
		_, err := io.ReadFull(r, rec)
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errors.New("truncated PQ partition")
		}
		if err != nil {
			return nil, err
		}
		ord := binary.LittleEndian.Uint32(rec[:4])
		if int(ord) >= p.manifest.Count {
			return nil, errors.New("invalid PQ ordinal")
		}
		sim := base
		valid := true
		for sub, code := range rec[4:] {
			ci := int(code)
			if ci >= len(lookup[sub]) {
				valid = false
				break
			}
			sim += lookup[sub][ci]
		}
		if valid {
			pushTopPQ(h, keep, pqHeapItem{ordinal: ord, sim: sim})
		}
	}
	return *h, nil
}

func (p *PQIndex) resolveID(ord uint32) (string, bool) {
	if int(ord) >= len(p.idOffsets) || p.idFile == nil {
		return "", false
	}
	o := int64(p.idOffsets[ord])
	var lb [2]byte
	if _, err := p.idFile.ReadAt(lb[:], o); err != nil {
		return "", false
	}
	n := int(binary.LittleEndian.Uint16(lb[:]))
	if n <= 0 {
		return "", false
	}
	b := make([]byte, n)
	if _, err := p.idFile.ReadAt(b, o+2); err != nil {
		return "", false
	}
	return string(b), true
}

func (p *PQIndex) Search(q []float32, k int) []PQHit {
	if len(q) != p.manifest.Dimension || k <= 0 {
		return nil
	}
	qn := append([]float32(nil), q...)
	l2norm(qn)
	type coarseHit struct {
		p    int
		dist float32
	}
	coarse := make([]coarseHit, len(p.manifest.Coarse))
	for i, c := range p.manifest.Coarse {
		// Training assigns partitions by squared L2 distance. Use the same
		// metric during probing; ranking only by dot(q,c) is wrong when centroid
		// norms differ.
		coarse[i] = coarseHit{i, sqDist(qn, c)}
	}
	sort.Slice(coarse, func(i, j int) bool { return coarse[i].dist < coarse[j].dist })
	probes := p.manifest.Config.ProbePartitions
	if probes < 1 {
		probes = 1
	}
	if probes > len(coarse) {
		probes = len(coarse)
	}
	perPart := k * 2
	if perPart < 32 {
		perPart = 32
	}
	lookup := buildPQLookup(qn, p.manifest.Codebooks)

	// Bound concurrent partition scanners. The old one-goroutine-per-probe
	// path multiplied buffers and temporary heaps by up to 48+ partitions per
	// query and became expensive under concurrent recall traffic.
	workers := probes
	if workers > 8 {
		workers = 8
	}
	type scanResult struct {
		items []pqHeapItem
		err   error
	}
	jobs := make(chan int, probes)
	results := make(chan scanResult, probes)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for part := range jobs {
				items, err := p.scanPartition(part, qn, lookup, perPart)
				results <- scanResult{items: items, err: err}
			}
		}()
	}
	for _, c := range coarse[:probes] {
		jobs <- c.p
	}
	close(jobs)
	wg.Wait()
	close(results)

	h := &pqMinHeap{}
	heap.Init(h)
	for res := range results {
		if res.err != nil {
			continue
		}
		for _, x := range res.items {
			pushTopPQ(h, k, x)
		}
	}
	out := make([]PQHit, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		x := heap.Pop(h).(pqHeapItem)
		id, ok := p.resolveID(x.ordinal)
		if !ok {
			continue
		}
		out[i] = PQHit{ID: id, Similarity: float64(x.sim)}
	}
	return out
}
