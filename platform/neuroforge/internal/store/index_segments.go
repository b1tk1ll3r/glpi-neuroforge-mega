package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"neuroforge/internal/vector"
)

type indexSegmentManifest struct {
	Revision     uint64            `json:"revision"`
	BaseRevision uint64            `json:"base_revision"`
	BaseFormat   string            `json:"base_format,omitempty"`
	BaseFiles    map[string]string `json:"base_files,omitempty"`
	Deltas       []string          `json:"deltas,omitempty"`
}

const indexBaseBinaryFormat = "bin-v1"

type indexDimensionDelta struct {
	DeletedDimension bool                      `json:"deleted_dimension,omitempty"`
	Config           vector.HNSWConfig         `json:"config"`
	EntryID          string                    `json:"entry_id"`
	MaxLevel         int                       `json:"max_level"`
	Upserts          []vector.HNSWSnapshotNode `json:"upserts,omitempty"`
	Deletes          []string                  `json:"deletes,omitempty"`
}

type indexDeltaBundle struct {
	Revision   uint64                         `json:"revision"`
	Dimensions map[string]indexDimensionDelta `json:"dimensions"`
}

type indexSnapshotShadow struct {
	Config   vector.HNSWConfig
	EntryID  string
	MaxLevel int
	Nodes    map[string][32]byte
}

func hashSnapshotNode(n vector.HNSWSnapshotNode) [32]byte {
	return vector.FingerprintSnapshotNode(n)
}

func buildIndexShadow(in map[int]vector.HNSWSnapshot) map[int]indexSnapshotShadow {
	out := make(map[int]indexSnapshotShadow, len(in))
	for dim, snap := range in {
		sh := indexSnapshotShadow{Config: snap.Config, EntryID: snap.EntryID, MaxLevel: snap.MaxLevel, Nodes: make(map[string][32]byte, len(snap.Nodes))}
		for _, n := range snap.Nodes {
			sh.Nodes[n.ID] = hashSnapshotNode(n)
		}
		out[dim] = sh
	}
	return out
}

func shadowFromHNSW(in map[int]*vector.HNSW) map[int]indexSnapshotShadow {
	out := make(map[int]indexSnapshotShadow, len(in))
	for dim, idx := range in {
		sh := idx.Shadow()
		out[dim] = indexSnapshotShadow{Config: sh.Config, EntryID: sh.EntryID, MaxLevel: sh.MaxLevel, Nodes: sh.Nodes}
	}
	return out
}

func writeHNSWAtomic(path string, idx *vector.HNSW) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if err := idx.WriteBinary(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *Store) writeBinaryIndexBasesLocked(dir string, revision uint64) (map[string]string, error) {
	dims := make([]int, 0, len(s.indexes))
	for dim := range s.indexes {
		dims = append(dims, dim)
	}
	sort.Ints(dims)
	files := make(map[string]string, len(dims))
	written := make([]string, 0, len(dims))
	for _, dim := range dims {
		name := fmt.Sprintf("base-%020d-%d.bin", revision, dim)
		if err := writeHNSWAtomic(filepath.Join(dir, name), s.indexes[dim]); err != nil {
			for _, x := range written {
				_ = os.Remove(filepath.Join(dir, x))
			}
			return nil, err
		}
		written = append(written, name)
		files[strconv.Itoa(dim)] = name
	}
	return files, nil
}

func loadBinaryIndexBases(dir string, manifest indexSegmentManifest) (map[int]*vector.HNSW, error) {
	indexes := make(map[int]*vector.HNSW, len(manifest.BaseFiles))
	for dimText, name := range manifest.BaseFiles {
		dim, err := strconv.Atoi(dimText)
		if err != nil || dim <= 0 {
			return nil, fmt.Errorf("invalid HNSW dimension %q", dimText)
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		idx, readErr := vector.ReadHNSWBinary(f)
		closeErr := f.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		indexes[dim] = idx
	}
	return indexes, nil
}

func cleanupOldIndexBases(dir string, keep map[string]string) {
	wanted := map[string]bool{}
	for _, name := range keep {
		wanted[name] = true
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "base-*.bin"))
	for _, path := range matches {
		if !wanted[filepath.Base(path)] {
			_ = os.Remove(path)
		}
	}
}
func (s *Store) currentSnapshotsLocked() map[int]vector.HNSWSnapshot {
	out := make(map[int]vector.HNSWSnapshot, len(s.indexes))
	for dim, idx := range s.indexes {
		out[dim] = idx.Snapshot()
	}
	return out
}

func (s *Store) writeSegmentedIndexSnapshotLocked() error {
	cfg := s.state.Config.Storage.IndexSegments
	if !cfg.Enabled {
		return s.writeLegacyIndexSnapshotLocked()
	}
	dir := filepath.Join(s.dir, "hnsw-index")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	var manifest indexSegmentManifest
	_ = s.loadJSON(manifestPath, &manifest)
	if manifest.BaseRevision > 0 && manifest.Revision == s.state.Revision && s.indexSnapshotRevision == s.state.Revision && s.indexShadow != nil {
		return nil
	}

	needBase := manifest.BaseRevision == 0 || manifest.Revision == 0 || s.indexShadow == nil
	baseEvery := cfg.BaseEvery
	if baseEvery <= 0 {
		baseEvery = 20
	}
	maxDeltas := cfg.MaxDeltas
	if maxDeltas <= 0 {
		maxDeltas = 64
	}
	if len(manifest.Deltas) >= maxDeltas || len(manifest.Deltas) >= baseEvery-1 {
		needBase = true
	}
	if needBase {
		files, err := s.writeBinaryIndexBasesLocked(dir, s.state.Revision)
		if err != nil {
			return err
		}
		old := manifest
		manifest = indexSegmentManifest{Revision: s.state.Revision, BaseRevision: s.state.Revision, BaseFormat: indexBaseBinaryFormat, BaseFiles: files}
		if err := writeAtomic(manifestPath, 0600, &manifest); err != nil {
			return err
		}
		for _, name := range old.Deltas {
			_ = os.Remove(filepath.Join(dir, name))
		}
		cleanupOldIndexBases(dir, files)
		_ = os.Remove(filepath.Join(dir, "base.json"))
		s.indexShadow = shadowFromHNSW(s.indexes)
		s.indexSnapshotRevision = s.state.Revision
		s.indexDeltaCount = 0
		return nil
	}

	current := s.currentSnapshotsLocked()
	delta := indexDeltaBundle{Revision: s.state.Revision, Dimensions: map[string]indexDimensionDelta{}}
	dims := map[int]bool{}
	for dim := range current {
		dims[dim] = true
	}
	for dim := range s.indexShadow {
		dims[dim] = true
	}
	for dim := range dims {
		cur, curOK := current[dim]
		prev, prevOK := s.indexShadow[dim]
		key := strconv.Itoa(dim)
		if !curOK {
			delta.Dimensions[key] = indexDimensionDelta{DeletedDimension: true}
			continue
		}
		d := indexDimensionDelta{Config: cur.Config, EntryID: cur.EntryID, MaxLevel: cur.MaxLevel}
		curIDs := map[string]bool{}
		for _, n := range cur.Nodes {
			curIDs[n.ID] = true
			h := hashSnapshotNode(n)
			if ph, ok := prev.Nodes[n.ID]; !prevOK || !ok || ph != h {
				d.Upserts = append(d.Upserts, n)
			}
		}
		if prevOK {
			for id := range prev.Nodes {
				if !curIDs[id] {
					d.Deletes = append(d.Deletes, id)
				}
			}
		}
		sort.Strings(d.Deletes)
		if !prevOK || len(d.Upserts) > 0 || len(d.Deletes) > 0 || prev.EntryID != cur.EntryID || prev.MaxLevel != cur.MaxLevel || prev.Config != cur.Config {
			delta.Dimensions[key] = d
		}
	}
	name := fmt.Sprintf("delta-%020d.json", s.state.Revision)
	if err := writeAtomic(filepath.Join(dir, name), 0600, &delta); err != nil {
		return err
	}
	manifest.Revision = s.state.Revision
	manifest.Deltas = append(manifest.Deltas, name)
	if err := writeAtomic(manifestPath, 0600, &manifest); err != nil {
		return err
	}
	s.indexShadow = buildIndexShadow(current)
	s.indexSnapshotRevision = s.state.Revision
	s.indexDeltaCount = len(manifest.Deltas)
	return nil
}

func applyIndexDelta(snapshots map[int]vector.HNSWSnapshot, delta indexDeltaBundle) error {
	for dimText, d := range delta.Dimensions {
		dim, err := strconv.Atoi(dimText)
		if err != nil || dim <= 0 {
			return fmt.Errorf("invalid HNSW dimension %q", dimText)
		}
		if d.DeletedDimension {
			delete(snapshots, dim)
			continue
		}
		snap := snapshots[dim]
		snap.Config, snap.EntryID, snap.MaxLevel = d.Config, d.EntryID, d.MaxLevel
		nodes := make(map[string]vector.HNSWSnapshotNode, len(snap.Nodes)+len(d.Upserts))
		for _, n := range snap.Nodes {
			nodes[n.ID] = n
		}
		for _, id := range d.Deletes {
			delete(nodes, id)
		}
		for _, n := range d.Upserts {
			nodes[n.ID] = n
		}
		snap.Nodes = snap.Nodes[:0]
		for _, n := range nodes {
			snap.Nodes = append(snap.Nodes, n)
		}
		sort.Slice(snap.Nodes, func(i, j int) bool { return snap.Nodes[i].ID < snap.Nodes[j].ID })
		snapshots[dim] = snap
	}
	return nil
}

func (s *Store) loadSegmentedIndexSnapshotLocked() bool {
	if !s.state.Config.Storage.IndexSegments.Enabled || !s.state.Config.Storage.IndexSnapshot || !s.state.Config.Brain.Index.Enabled {
		return false
	}
	dir := filepath.Join(s.dir, "hnsw-index")
	var manifest indexSegmentManifest
	if err := s.loadJSON(filepath.Join(dir, "manifest.json"), &manifest); err != nil || manifest.Revision != s.state.Revision || manifest.BaseRevision == 0 {
		return false
	}
	var snapshots map[int]vector.HNSWSnapshot
	if manifest.BaseFormat == indexBaseBinaryFormat {
		indexes, err := loadBinaryIndexBases(dir, manifest)
		if err != nil {
			return false
		}
		if len(manifest.Deltas) == 0 {
			if !s.indexCountMatchesLocked(indexes) {
				return false
			}
			s.indexes = indexes
			s.indexShadow = shadowFromHNSW(indexes)
			s.indexSnapshotRevision = manifest.Revision
			s.indexDeltaCount = 0
			return true
		}
		snapshots = make(map[int]vector.HNSWSnapshot, len(indexes))
		for dim, idx := range indexes {
			snapshots[dim] = idx.Snapshot()
		}
	} else {
		var base indexSnapshotBundle
		if err := s.loadJSON(filepath.Join(dir, "base.json"), &base); err != nil || base.Revision != manifest.BaseRevision {
			return false
		}
		snapshots = map[int]vector.HNSWSnapshot{}
		for dimText, snap := range base.Indexes {
			dim, err := strconv.Atoi(dimText)
			if err != nil || dim <= 0 {
				return false
			}
			snapshots[dim] = snap
		}
	}
	lastRevision := manifest.BaseRevision
	for _, name := range manifest.Deltas {
		var delta indexDeltaBundle
		if err := s.loadJSON(filepath.Join(dir, name), &delta); err != nil || delta.Revision <= lastRevision || delta.Revision > manifest.Revision {
			return false
		}
		if err := applyIndexDelta(snapshots, delta); err != nil {
			return false
		}
		lastRevision = delta.Revision
	}
	if lastRevision != manifest.Revision {
		return false
	}
	indexes := map[int]*vector.HNSW{}
	for dim, snap := range snapshots {
		indexes[dim] = vector.NewHNSWFromSnapshot(snap)
	}
	if !s.indexCountMatchesLocked(indexes) {
		return false
	}
	s.indexes = indexes
	s.indexShadow = shadowFromHNSW(indexes)
	s.indexSnapshotRevision = manifest.Revision
	s.indexDeltaCount = len(manifest.Deltas)
	return true
}

func (s *Store) indexCountMatchesLocked(indexes map[int]*vector.HNSW) bool {
	got := 0
	for _, idx := range indexes {
		got += idx.Len()
	}
	if indexMode(s.state.Config) != "hnsw" && len(s.diskIndexes) > 0 {
		// Hybrid snapshots intentionally contain only the hot/delta tier.
		return got >= 0
	}
	want := 0
	for _, m := range s.state.Memories {
		if m.VectorDim > 0 || len(m.Vector) > 0 {
			want++
		}
	}
	return got == want
}

func (s *Store) writeLegacyIndexSnapshotLocked() error {
	bundle := indexSnapshotBundle{Revision: s.state.Revision, Indexes: map[string]vector.HNSWSnapshot{}}
	for dim, idx := range s.indexes {
		bundle.Indexes[strconv.Itoa(dim)] = idx.Snapshot()
	}
	return writeAtomic(filepath.Join(s.dir, "hnsw.snapshot.json"), 0600, &bundle)
}

func (s *Store) loadLegacyIndexSnapshotLocked() bool {
	var bundle indexSnapshotBundle
	if err := s.loadJSON(filepath.Join(s.dir, "hnsw.snapshot.json"), &bundle); err != nil || bundle.Revision != s.state.Revision {
		return false
	}
	indexes := map[int]*vector.HNSW{}
	snapshots := map[int]vector.HNSWSnapshot{}
	for dimText, snap := range bundle.Indexes {
		dim, err := strconv.Atoi(dimText)
		if err != nil || dim <= 0 {
			return false
		}
		idx := vector.NewHNSWFromSnapshot(snap)
		indexes[dim] = idx
		snapshots[dim] = snap
	}
	want := 0
	for _, m := range s.state.Memories {
		if m.VectorDim > 0 || len(m.Vector) > 0 {
			want++
		}
	}
	got := 0
	for _, idx := range indexes {
		got += idx.Len()
	}
	if got != want && !(indexMode(s.state.Config) != "hnsw" && len(s.diskIndexes) > 0) {
		return false
	}
	s.indexes = indexes
	s.indexShadow = buildIndexShadow(snapshots)
	s.indexSnapshotRevision = bundle.Revision
	return true
}

func (s *Store) IndexSnapshotStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IndexSnapshotStatusUnlocked()
}

// CompactIndexSegments merges the current HNSW state into a new base snapshot
// and removes accumulated deltas. It is safe to call from background maintenance.
func (s *Store) CompactIndexSegments() (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Config.Storage.IndexSegments.Enabled || !s.state.Config.Storage.IndexSnapshot || !s.state.Config.Brain.Index.Enabled {
		return s.IndexSnapshotStatusUnlocked(), nil
	}
	if err := s.writeIndexBaseLocked(); err != nil {
		return nil, err
	}
	return s.IndexSnapshotStatusUnlocked(), nil
}

func (s *Store) writeIndexBaseLocked() error {
	dir := filepath.Join(s.dir, "hnsw-index")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	files, err := s.writeBinaryIndexBasesLocked(dir, s.state.Revision)
	if err != nil {
		return err
	}
	var old indexSegmentManifest
	_ = s.loadJSON(filepath.Join(dir, "manifest.json"), &old)
	manifest := indexSegmentManifest{Revision: s.state.Revision, BaseRevision: s.state.Revision, BaseFormat: indexBaseBinaryFormat, BaseFiles: files}
	if err := writeAtomic(filepath.Join(dir, "manifest.json"), 0600, &manifest); err != nil {
		return err
	}
	for _, name := range old.Deltas {
		_ = os.Remove(filepath.Join(dir, name))
	}
	cleanupOldIndexBases(dir, files)
	_ = os.Remove(filepath.Join(dir, "base.json"))
	s.indexShadow = shadowFromHNSW(s.indexes)
	s.indexSnapshotRevision = s.state.Revision
	s.indexDeltaCount = 0
	return nil
}

func (s *Store) IndexSnapshotStatusUnlocked() map[string]any {
	out := map[string]any{"revision": s.indexSnapshotRevision, "deltas": s.indexDeltaCount, "segmented": s.state.Config.Storage.IndexSegments.Enabled}
	if b, err := json.Marshal(s.indexShadow); err == nil {
		out["shadow_bytes_estimate"] = len(b)
	}
	return out
}
