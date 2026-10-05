package store

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"neuroforge/internal/core"
)

const maxSegmentRecordBytes = 128 << 20

type segmentRecord struct {
	Revision uint64       `json:"revision"`
	Op       string       `json:"op"`
	ID       string       `json:"id"`
	Memory   *core.Memory `json:"memory,omitempty"`
}

type segmentLocation struct {
	Path     string
	Offset   int64
	Length   uint32
	Revision uint64
	Deleted  bool
}

type SegmentStats struct {
	Enabled      bool   `json:"enabled"`
	Segments     int    `json:"segments"`
	Records      int    `json:"records"`
	Live         int    `json:"live"`
	Tombstones   int    `json:"tombstones"`
	Bytes        int64  `json:"bytes"`
	Active       string `json:"active,omitempty"`
	MmapSegments int    `json:"mmap_segments"`
}

type SegmentStore struct {
	mu              sync.RWMutex
	dir             string
	maxSegmentBytes int64
	mmapSealed      bool
	seq             int
	activePath      string
	activeSize      int64
	index           map[string]segmentLocation
	mmaps           map[string][]byte
	records         int
	tombstones      int
	scanMetadata    map[string]core.Memory
}

func openSegmentStore(dir string, maxBytes int64, mmapSealed bool) (*SegmentStore, error) {
	if maxBytes < 1<<20 {
		maxBytes = 128 << 20
	}
	ss := &SegmentStore{
		dir: dir, maxSegmentBytes: maxBytes, mmapSealed: mmapSealed,
		index: map[string]segmentLocation{}, mmaps: map[string][]byte{}, scanMetadata: map[string]core.Memory{},
	}
	if err := recoverInterruptedRebuild(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := ss.scan(); err != nil {
		ss.Close()
		return nil, err
	}
	return ss, nil
}

// recoverInterruptedRebuild handles a crash between the two renames in
// Rebuild: dir is gone but dir.old still holds the previous segments.
func recoverInterruptedRebuild(dir string) error {
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	backup := dir + ".old"
	if st, err := os.Stat(backup); err != nil || !st.IsDir() {
		return nil
	}
	if err := os.Rename(backup, dir); err != nil {
		return fmt.Errorf("restore %s after interrupted segment rebuild: %w", backup, err)
	}
	_ = os.RemoveAll(dir + ".rebuild")
	return syncDirBestEffort(filepath.Dir(dir))
}

// syncDirBestEffort persists renames in dir. Windows cannot fsync directory
// handles; NTFS persists renames without it.
func syncDirBestEffort(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

func (s *SegmentStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, b := range s.mmaps {
		_ = unmapSegmentFile(b)
		delete(s.mmaps, path)
	}
}

func segmentName(seq int) string { return fmt.Sprintf("segment-%06d.nfs", seq) }

func parseSegmentSeq(name string) (int, bool) {
	if !strings.HasPrefix(name, "segment-") || !strings.HasSuffix(name, ".nfs") {
		return 0, false
	}
	x := strings.TrimSuffix(strings.TrimPrefix(name, "segment-"), ".nfs")
	n, err := strconv.Atoi(x)
	return n, err == nil && n > 0
}

func (s *SegmentStore) scan() error {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	type item struct {
		seq  int
		path string
	}
	items := []item{}
	for _, ent := range ents {
		if ent.IsDir() {
			continue
		}
		seq, ok := parseSegmentSeq(ent.Name())
		if ok {
			items = append(items, item{seq: seq, path: filepath.Join(s.dir, ent.Name())})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].seq < items[j].seq })
	for _, it := range items {
		if err := s.scanFile(it.path); err != nil {
			return err
		}
		s.seq = it.seq
	}
	if s.seq == 0 {
		s.seq = 1
	}
	s.activePath = filepath.Join(s.dir, segmentName(s.seq))
	if st, err := os.Stat(s.activePath); err == nil {
		s.activeSize = st.Size()
	}
	if s.activeSize >= s.maxSegmentBytes {
		s.seq++
		s.activePath = filepath.Join(s.dir, segmentName(s.seq))
		s.activeSize = 0
	}
	return nil
}

func (s *SegmentStore) scanFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var offset int64
	// A crash can leave a partial tail. It was never acknowledged, so cut it
	// off: otherwise the next append lands behind the garbage and the following
	// restart misreads garbage+record as one corrupt record.
	dropTornTail := func() error {
		_ = f.Close()
		if err := os.Truncate(path, offset); err != nil {
			return fmt.Errorf("truncate torn segment tail %s:%d: %w", path, offset, err)
		}
		return nil
	}
	for {
		var hdr [4]byte
		_, err := io.ReadFull(f, hdr[:])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return dropTornTail()
		}
		if err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 || n > maxSegmentRecordBytes {
			return fmt.Errorf("invalid segment record length %d at %s:%d", n, path, offset)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(f, buf); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return dropTornTail()
			}
			return err
		}
		var rec segmentRecord
		if err := json.Unmarshal(buf, &rec); err != nil {
			return fmt.Errorf("decode segment %s:%d: %w", path, offset, err)
		}
		if rec.ID == "" {
			return fmt.Errorf("empty memory id in segment %s:%d", path, offset)
		}
		loc := segmentLocation{Path: path, Offset: offset + 4, Length: n, Revision: rec.Revision, Deleted: rec.Op == "delete"}
		if old, ok := s.index[rec.ID]; !ok || rec.Revision >= old.Revision {
			s.index[rec.ID] = loc
			if rec.Op == "delete" {
				delete(s.scanMetadata, rec.ID)
			} else if rec.Memory != nil {
				m := cloneMemory(*rec.Memory)
				// Historical segment records may have stored the canonical ID only
				// in segmentRecord.ID. Restore the invariant Memory.ID == record ID.
				m.ID = rec.ID
				if m.VectorDim == 0 && len(m.Vector) > 0 {
					m.VectorDim = len(m.Vector)
				}
				m.Text = ""
				m.Vector = nil
				s.scanMetadata[rec.ID] = m
			}
		}
		s.records++
		if rec.Op == "delete" {
			s.tombstones++
		}
		offset += 4 + int64(n)
	}
}

func (s *SegmentStore) appendRecord(rec segmentRecord) error {
	return s.appendRecords([]segmentRecord{rec})
}

func (s *SegmentStore) appendRecords(records []segmentRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var f *os.File
	var openPath string
	closeFile := func() error {
		if f == nil {
			return nil
		}
		err := f.Sync()
		cerr := f.Close()
		f = nil
		openPath = ""
		if err != nil {
			return err
		}
		return cerr
	}
	defer func() { _ = closeFile() }()
	for _, rec := range records {
		if rec.ID == "" {
			return errors.New("segment record id required")
		}
		deleted := rec.Op == "delete"
		if old, ok := s.index[rec.ID]; ok && old.Revision == rec.Revision && old.Deleted == deleted {
			continue
		}
		payload, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if len(payload) > maxSegmentRecordBytes {
			return fmt.Errorf("memory segment record exceeds %d bytes", maxSegmentRecordBytes)
		}
		recordBytes := int64(4 + len(payload))
		if s.activeSize > 0 && s.activeSize+recordBytes > s.maxSegmentBytes {
			if err := closeFile(); err != nil {
				return err
			}
			if s.mmapSealed {
				_, _ = s.mapLocked(s.activePath)
			}
			s.seq++
			s.activePath = filepath.Join(s.dir, segmentName(s.seq))
			s.activeSize = 0
		}
		if f == nil || openPath != s.activePath {
			if err := closeFile(); err != nil {
				return err
			}
			f, err = os.OpenFile(s.activePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			openPath = s.activePath
		}
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
		start := s.activeSize
		if _, err := f.Write(hdr[:]); err != nil {
			_ = f.Truncate(start) // keep offsets of later records valid
			return err
		}
		if _, err := f.Write(payload); err != nil {
			_ = f.Truncate(start)
			return err
		}
		s.activeSize += recordBytes
		s.index[rec.ID] = segmentLocation{Path: s.activePath, Offset: start + 4, Length: uint32(len(payload)), Revision: rec.Revision, Deleted: rec.Op == "delete"}
		s.records++
		if rec.Op == "delete" {
			s.tombstones++
		}
	}
	return closeFile()
}

func (s *SegmentStore) AppendUpsert(revision uint64, memories []core.Memory) error {
	recs := make([]segmentRecord, 0, len(memories))
	for i := range memories {
		m := cloneMemory(memories[i])
		recs = append(recs, segmentRecord{Revision: revision, Op: "upsert", ID: m.ID, Memory: &m})
	}
	return s.appendRecords(recs)
}

func (s *SegmentStore) AppendDelete(revision uint64, ids []string) error {
	recs := make([]segmentRecord, 0, len(ids))
	for _, id := range ids {
		recs = append(recs, segmentRecord{Revision: revision, Op: "delete", ID: id})
	}
	return s.appendRecords(recs)
}

func (s *SegmentStore) mapLocked(path string) ([]byte, bool) {
	if !s.mmapSealed || path == "" || path == s.activePath {
		return nil, false
	}
	if b, ok := s.mmaps[path]; ok {
		return b, true
	}
	b, ok, err := mapSegmentFile(path)
	if err != nil || !ok {
		return nil, false
	}
	s.mmaps[path] = b
	return b, true
}

func (s *SegmentStore) readLocation(loc segmentLocation) (segmentRecord, error) {
	s.mu.Lock()
	if b, ok := s.mapLocked(loc.Path); ok {
		start, end := loc.Offset, loc.Offset+int64(loc.Length)
		if start >= 0 && end <= int64(len(b)) {
			buf := append([]byte(nil), b[start:end]...)
			s.mu.Unlock()
			var rec segmentRecord
			return rec, json.Unmarshal(buf, &rec)
		}
	}
	s.mu.Unlock()
	f, err := os.Open(loc.Path)
	if err != nil {
		return segmentRecord{}, err
	}
	defer f.Close()
	buf := make([]byte, loc.Length)
	if _, err := f.ReadAt(buf, loc.Offset); err != nil {
		return segmentRecord{}, err
	}
	var rec segmentRecord
	return rec, json.Unmarshal(buf, &rec)
}

func (s *SegmentStore) Get(id string) (core.Memory, bool, bool, error) {
	s.mu.RLock()
	loc, ok := s.index[id]
	s.mu.RUnlock()
	if !ok {
		return core.Memory{}, false, false, nil
	}
	if loc.Deleted {
		return core.Memory{}, true, true, nil
	}
	rec, err := s.readLocation(loc)
	if err != nil {
		return core.Memory{}, true, false, err
	}
	if rec.Memory == nil {
		return core.Memory{}, true, false, errors.New("segment upsert has no memory body")
	}
	m := cloneMemory(*rec.Memory)
	// The lookup key comes from the outer segment record and is authoritative.
	m.ID = id
	return m, true, false, nil
}

type liveSegmentCursor struct {
	id  string
	loc segmentLocation
}

// iterateLivePayloadsSequential captures a point-in-time view of the latest
// live records and then walks each segment strictly forward. It never performs
// one ReadAt/seek per memory: bytes between live records are consumed through a
// buffered stream and the payload buffer is reused. Because segment files are
// append-only, captured locations remain valid while newer writes continue.
func (s *SegmentStore) iterateLivePayloadsSequential(fn func(id string, payload []byte) error) error {
	if fn == nil {
		return errors.New("segment payload iterator callback required")
	}

	s.mu.RLock()
	byPath := make(map[string][]liveSegmentCursor)
	for id, loc := range s.index {
		if loc.Deleted {
			continue
		}
		byPath[loc.Path] = append(byPath[loc.Path], liveSegmentCursor{id: id, loc: loc})
	}
	s.mu.RUnlock()

	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var payload []byte
	for _, path := range paths {
		cursors := byPath[path]
		sort.Slice(cursors, func(i, j int) bool { return cursors[i].loc.Offset < cursors[j].loc.Offset })

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		br := bufio.NewReaderSize(f, 1<<20)
		var pos int64
		for _, cursor := range cursors {
			loc := cursor.loc
			if loc.Offset < pos {
				_ = f.Close()
				return fmt.Errorf("segment iterator moved backwards in %s: current=%d target=%d", path, pos, loc.Offset)
			}
			if gap := loc.Offset - pos; gap > 0 {
				if _, err := io.CopyN(io.Discard, br, gap); err != nil {
					_ = f.Close()
					return fmt.Errorf("scan segment %s to offset %d: %w", path, loc.Offset, err)
				}
				pos += gap
			}

			n := int(loc.Length)
			if cap(payload) < n {
				payload = make([]byte, n)
			} else {
				payload = payload[:n]
			}
			if _, err := io.ReadFull(br, payload); err != nil {
				_ = f.Close()
				return fmt.Errorf("read segment %s:%d: %w", path, loc.Offset, err)
			}
			pos += int64(n)
			if err := fn(cursor.id, payload); err != nil {
				_ = f.Close()
				return err
			}
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// IterateLiveMemories walks the latest live records using the sequential
// segment scanner. The callback receives a detached Memory and may retain it.
func (s *SegmentStore) IterateLiveMemories(fn func(core.Memory) error) error {
	if fn == nil {
		return errors.New("segment iterator callback required")
	}
	return s.iterateLivePayloadsSequential(func(expectedID string, payload []byte) error {
		var rec segmentRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			return fmt.Errorf("decode live segment memory %s: %w", expectedID, err)
		}
		if rec.ID != expectedID {
			return fmt.Errorf("segment index mismatch: expected %q, payload contains %q", expectedID, rec.ID)
		}
		if rec.Op == "delete" || rec.Memory == nil {
			return nil
		}
		return fn(cloneMemory(*rec.Memory))
	})
}

type segmentVectorMemory struct {
	Status    string    `json:"status"`
	Vector    []float32 `json:"vector"`
	VectorDim int       `json:"vector_dim,omitempty"`
}

type segmentVectorRecord struct {
	Op     string               `json:"op"`
	ID     string               `json:"id"`
	Memory *segmentVectorMemory `json:"memory,omitempty"`
}

// IterateLiveVectorsSequential is the disk-ANN migration/build fast path. It
// scans every segment at most once in ascending physical offset order and only
// decodes the fields required by the vector builder. The vector slice is owned
// by the iterator and must not be retained after the callback returns.
func (s *SegmentStore) IterateLiveVectorsSequential(dim int, fn func(id string, vector []float32) error) error {
	if dim < 1 {
		return errors.New("vector dimension must be positive")
	}
	if fn == nil {
		return errors.New("segment vector iterator callback required")
	}
	return s.iterateLivePayloadsSequential(func(expectedID string, payload []byte) error {
		var rec segmentVectorRecord
		if err := json.Unmarshal(payload, &rec); err != nil {
			return fmt.Errorf("decode live segment vector %s: %w", expectedID, err)
		}
		if rec.ID != expectedID {
			return fmt.Errorf("segment index mismatch: expected %q, payload contains %q", expectedID, rec.ID)
		}
		if rec.Op == "delete" || rec.Memory == nil {
			return nil
		}
		status := rec.Memory.Status
		if status != "" && status != core.MemoryActive && status != core.MemoryConflicted {
			return nil
		}
		actualDim := rec.Memory.VectorDim
		if actualDim == 0 {
			actualDim = len(rec.Memory.Vector)
		}
		if actualDim != dim || len(rec.Memory.Vector) != dim {
			return nil
		}
		return fn(rec.ID, rec.Memory.Vector)
	})
}

func (s *SegmentStore) Hydrate(memories map[string]*core.Memory) error {
	for id := range memories {
		m, found, deleted, err := s.Get(id)
		if err != nil {
			return err
		}
		if !found {
			// v0.3 migration: the body can still be present in state.json.
			continue
		}
		if deleted {
			delete(memories, id)
			continue
		}
		memories[id] = &m
	}
	return nil
}

func (s *SegmentStore) ConsumeMetadata() map[string]*core.Memory {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*core.Memory, len(s.scanMetadata))
	for id, m := range s.scanMetadata {
		cp := cloneMemory(m)
		out[id] = &cp
	}
	s.scanMetadata = nil
	return out
}

func (s *SegmentStore) HasRecords() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.records > 0
}

func (s *SegmentStore) Rebuild(memories map[string]*core.Memory, revision uint64) error {
	s.mu.Lock()
	for _, b := range s.mmaps {
		_ = unmapSegmentFile(b)
	}
	s.mmaps = map[string][]byte{}
	s.mu.Unlock()
	tmp := s.dir + ".rebuild"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0700); err != nil {
		return err
	}
	fresh, err := openSegmentStore(tmp, s.maxSegmentBytes, s.mmapSealed)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(memories))
	for id := range memories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := cloneMemory(*memories[id])
		if !memoryBodyResident(&m) {
			// Metadata-only entry (hydrated at boot or evicted by tiering): carry
			// the body over from the current segments before they are replaced,
			// otherwise compaction would permanently drop text and vector.
			full, found, deleted, err := s.Get(id)
			if err != nil {
				fresh.Close()
				return fmt.Errorf("rebuild segments: load body of %s: %w", id, err)
			}
			if found && !deleted {
				m.Text = full.Text
				m.Vector = full.Vector
			}
		}
		if err := fresh.AppendUpsert(revision, []core.Memory{m}); err != nil {
			fresh.Close()
			return err
		}
	}
	fresh.Close()
	backup := s.dir + ".old"
	_ = os.RemoveAll(backup)
	if err := os.Rename(s.dir, backup); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.dir); err != nil {
		_ = os.Rename(backup, s.dir)
		return err
	}
	// Make both renames durable before the old segments are deleted.
	if err := syncDirBestEffort(filepath.Dir(s.dir)); err != nil {
		return err
	}
	_ = os.RemoveAll(backup)

	reloaded, err := openSegmentStore(s.dir, s.maxSegmentBytes, s.mmapSealed)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.seq = reloaded.seq
	s.activePath = reloaded.activePath
	s.activeSize = reloaded.activeSize
	s.index = reloaded.index
	s.records = reloaded.records
	s.tombstones = reloaded.tombstones
	s.mmaps = reloaded.mmaps
	s.scanMetadata = nil
	s.mu.Unlock()
	// Ownership of mmaps moved to s.
	reloaded.mmaps = map[string][]byte{}
	return nil
}

func (s *SegmentStore) Stats() SegmentStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	live := 0
	for _, loc := range s.index {
		if !loc.Deleted {
			live++
		}
	}
	stats := SegmentStats{Enabled: true, Records: s.records, Live: live, Tombstones: s.tombstones, Active: filepath.Base(s.activePath), MmapSegments: len(s.mmaps)}
	ents, _ := os.ReadDir(s.dir)
	for _, ent := range ents {
		if ent.IsDir() {
			continue
		}
		if _, ok := parseSegmentSeq(ent.Name()); !ok {
			continue
		}
		stats.Segments++
		if info, err := ent.Info(); err == nil {
			stats.Bytes += info.Size()
		}
	}
	return stats
}

func (s *SegmentStore) TombstoneRatio() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.records == 0 {
		return 0
	}
	return float64(s.tombstones) / float64(s.records)
}

func (s *SegmentStore) HasLive(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	loc, ok := s.index[id]
	return ok && !loc.Deleted
}
