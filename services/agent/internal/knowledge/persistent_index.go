package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

const persistentSnapshotVersion = 1

type fileRecord struct {
	Key             string
	Path            string
	ID              string
	Size            int64
	ModTimeUnixNano int64
	RawHash         string
	Managed         bool
	Included        bool
	Ignored         bool
	Unmapped        []string
}

type persistentSnapshot struct {
	Version      int
	Fingerprint  string
	SavedAt      time.Time
	Docs         []model.KnowledgeDoc
	Files        map[string]string
	Managed      map[string]bool
	StaticDocs   map[string]model.KnowledgeDoc
	TitleVectors map[string][]float64
	ChunkVectors map[string][][]float64
	Chunks       map[string][]string
	Manifest     map[string]fileRecord
	LoadStats    LoadStats
}

func (s *Store) indexFingerprint() string {
	allowed := make([]string, 0, len(s.allowedSources))
	for source := range s.allowedSources {
		allowed = append(allowed, source)
	}
	sort.Strings(allowed)
	mapKeys := make([]string, 0, len(s.categoryMap))
	for k := range s.categoryMap {
		mapKeys = append(mapKeys, k)
	}
	sort.Strings(mapKeys)
	mapping := make([]struct {
		Key string
		IDs []int64
	}, 0, len(mapKeys))
	for _, k := range mapKeys {
		mapping = append(mapping, struct {
			Key string
			IDs []int64
		}{k, append([]int64(nil), s.categoryMap[k]...)})
	}
	payload := struct {
		RAG               bool
		EmbeddingIdentity string
		EmbeddingProfile  string
		ChunkWords        int
		ChunkOverlap      int
		MaxChunksPerDoc   int
		CategoryMode      string
		IgnoreGlobs       []string
		AllowedSources    []string
		CategoryMap       any
	}{
		RAG: s.rag, EmbeddingIdentity: s.scoring.EmbeddingIdentity, EmbeddingProfile: s.scoring.EmbeddingProfile,
		ChunkWords: s.scoring.ChunkWords, ChunkOverlap: s.scoring.ChunkOverlap, MaxChunksPerDoc: s.scoring.MaxChunksPerDoc,
		CategoryMode: s.loadOptions.CategoryMode, IgnoreGlobs: append([]string(nil), s.loadOptions.IgnoreGlobs...), AllowedSources: allowed, CategoryMap: mapping,
	}
	b, _ := json.Marshal(payload)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Store) loadPersistentSnapshot() (bool, error) {
	f, err := os.Open(s.snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("open persistent knowledge index: %w", err)
	}
	defer f.Close()
	var snap persistentSnapshot
	if err := gob.NewDecoder(f).Decode(&snap); err != nil {
		return false, fmt.Errorf("decode persistent knowledge index: %w", err)
	}
	if snap.Version != persistentSnapshotVersion {
		return false, fmt.Errorf("persistent knowledge index version %d is unsupported", snap.Version)
	}
	if snap.Fingerprint != s.indexFingerprint() {
		return false, nil
	}
	if snap.Files == nil {
		snap.Files = map[string]string{}
	}
	if snap.Managed == nil {
		snap.Managed = map[string]bool{}
	}
	if snap.StaticDocs == nil {
		snap.StaticDocs = map[string]model.KnowledgeDoc{}
	}
	if snap.TitleVectors == nil {
		snap.TitleVectors = map[string][]float64{}
	}
	if snap.ChunkVectors == nil {
		snap.ChunkVectors = map[string][][]float64{}
	}
	if snap.Chunks == nil {
		snap.Chunks = map[string][]string{}
	}
	if snap.Manifest == nil {
		snap.Manifest = map[string]fileRecord{}
	}
	// Paths in a snapshot may come from another host/project location. Rebind
	// them to the current knowledge roots using the stable manifest key.
	reboundFiles := map[string]string{}
	for key, rec := range snap.Manifest {
		rel := strings.TrimPrefix(key, "static/")
		root := s.dir
		if strings.HasPrefix(key, "managed/") {
			rel = strings.TrimPrefix(key, "managed/")
			root = s.managedDir
		}
		rel = filepath.Clean(filepath.FromSlash(rel))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false, fmt.Errorf("persistent knowledge index contains invalid manifest path %q", key)
		}
		rec.Path = filepath.Join(root, rel)
		snap.Manifest[key] = rec
		if rec.Included && rec.ID != "" {
			if rec.Managed || reboundFiles[rec.ID] == "" {
				reboundFiles[rec.ID] = rec.Path
			}
		}
	}
	snap.Files = reboundFiles

	s.mu.Lock()
	s.docs = snap.Docs
	s.files = snap.Files
	s.managed = snap.Managed
	s.staticDocs = snap.StaticDocs
	s.titleVectors = snap.TitleVectors
	s.chunkVectors = snap.ChunkVectors
	s.chunks = snap.Chunks
	s.manifest = snap.Manifest
	s.external = map[string]string{}
	s.loadStats = snap.LoadStats
	s.initStatus = InitStatus{
		State: "ready", Phase: "ready-cache", TotalFiles: len(snap.Manifest), ProcessedFiles: len(snap.Manifest), LoadedDocs: len(snap.Docs),
		IndexedDocs: len(snap.Docs), CacheHits: len(snap.Docs), PendingEmbeddings: 0, StartedAt: time.Now(), FinishedAt: time.Now(),
		SnapshotLoaded: true, SnapshotPath: s.snapshotPath, SnapshotSavedAt: snap.SavedAt,
	}
	s.mu.Unlock()
	slog.Info("persistent knowledge index loaded", "documents", len(snap.Docs), "files", len(snap.Manifest), "saved_at", snap.SavedAt, "path", s.snapshotPath)
	return true, nil
}

func (s *Store) persistSnapshot() error {
	if s == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.snapshotPath), 0o750); err != nil {
		return err
	}

	s.mu.RLock()
	localDocs := make([]model.KnowledgeDoc, 0, len(s.docs))
	files := map[string]string{}
	managed := map[string]bool{}
	staticDocs := make(map[string]model.KnowledgeDoc, len(s.staticDocs))
	titles := map[string][]float64{}
	vectors := map[string][][]float64{}
	chunks := map[string][]string{}
	for id, d := range s.staticDocs {
		staticDocs[id] = d
	}
	for _, d := range s.docs {
		if s.external[d.ID] != "" {
			continue
		}
		localDocs = append(localDocs, d)
		if path := s.files[d.ID]; path != "" {
			files[d.ID] = path
		}
		if s.managed[d.ID] {
			managed[d.ID] = true
		}
		if v := s.titleVectors[d.ID]; len(v) > 0 {
			titles[d.ID] = v
		}
		if vv := s.chunkVectors[d.ID]; len(vv) > 0 {
			vectors[d.ID] = vv
		}
		if cc := s.chunks[d.ID]; len(cc) > 0 {
			chunks[d.ID] = cc
		}
	}
	manifest := make(map[string]fileRecord, len(s.manifest))
	for k, v := range s.manifest {
		manifest[k] = v
	}
	stats := s.loadStats
	stats.UnmappedCategories = append([]string(nil), stats.UnmappedCategories...)
	s.mu.RUnlock()

	snap := persistentSnapshot{Version: persistentSnapshotVersion, Fingerprint: s.indexFingerprint(), SavedAt: time.Now(), Docs: localDocs, Files: files, Managed: managed, StaticDocs: staticDocs, TitleVectors: titles, ChunkVectors: vectors, Chunks: chunks, Manifest: manifest, LoadStats: stats}
	tmp := s.snapshotPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	encErr := gob.NewEncoder(f).Encode(&snap)
	closeErr := f.Close()
	if encErr != nil {
		_ = os.Remove(tmp)
		return encErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, s.snapshotPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.setInitStatus(func(st *InitStatus) {
		st.SnapshotPath = s.snapshotPath
		st.SnapshotSavedAt = snap.SavedAt
	})
	return nil
}

// StartIncrementalSync keeps a warm persistent index usable while source files
// are checked for changes in the background. The first delta scan runs
// immediately. A zero interval disables subsequent periodic scans.
func (s *Store) StartIncrementalSync(ctx context.Context, interval time.Duration) {
	if s == nil || strings.EqualFold(s.scoring.IndexMode, "readonly") {
		return
	}
	go func() {
		s.syncLocalSafely(ctx)
		if interval <= 0 {
			return
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.syncLocalSafely(ctx)
			}
		}
	}()
}

func (s *Store) syncLocalSafely(ctx context.Context) {
	if err := s.SyncLocal(ctx); err != nil {
		slog.Error("incremental knowledge scan failed; keeping previous index", "error", err)
		s.setInitStatus(func(st *InitStatus) { st.LastScanAt = time.Now(); st.LastScanError = err.Error(); st.Phase = "ready" })
	}
}

// SyncLocal performs a metadata-first delta scan. Unchanged files are never
// opened or parsed. Files whose size/mtime changed are hashed; unchanged bytes
// are reused without parsing/embedding. Only genuinely changed retrieval text
// is sent to the embedding provider.
func (s *Store) SyncLocal(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("knowledge store is not initialized")
	}
	if strings.EqualFold(s.scoring.IndexMode, "readonly") {
		return nil
	}
	s.initMu.Lock()
	defer s.initMu.Unlock()
	if !s.Ready() {
		return fmt.Errorf("knowledge store is not ready")
	}

	s.setInitStatus(func(st *InitStatus) {
		st.Phase = "syncing"
		st.LastScanError = ""
		st.ChangedFiles = 0
		st.DeletedFiles = 0
		st.ReusedFiles = 0
	})

	s.mu.RLock()
	oldManifest := make(map[string]fileRecord, len(s.manifest))
	for k, v := range s.manifest {
		oldManifest[k] = v
	}
	oldDocs := make(map[string]model.KnowledgeDoc, len(s.docs))
	for _, d := range s.docs {
		oldDocs[d.ID] = d
	}
	oldStatic := make(map[string]model.KnowledgeDoc, len(s.staticDocs))
	for k, v := range s.staticDocs {
		oldStatic[k] = v
	}
	oldManaged := make(map[string]model.KnowledgeDoc)
	for _, d := range s.docs {
		if s.managed[d.ID] {
			oldManaged[d.ID] = d
		}
	}
	oldTitle := s.titleVectors
	oldChunkVec := s.chunkVectors
	oldChunks := s.chunks
	_, semanticMode, _, _ := s.semanticSettings()
	externalDocs := make([]model.KnowledgeDoc, 0)
	externalMap := make(map[string]string, len(s.external))
	for id, src := range s.external {
		externalMap[id] = src
	}
	for _, d := range s.docs {
		if s.external[d.ID] != "" {
			externalDocs = append(externalDocs, d)
		}
	}
	s.mu.RUnlock()

	resStatic, err := s.scanDeltaDir(ctx, s.dir, "static", false, s.loadOptions, oldManifest, oldStatic)
	if err != nil {
		return err
	}
	resManaged, err := s.scanDeltaDir(ctx, s.managedDir, "managed", true, LoadOptions{CategoryMode: "strict"}, oldManifest, oldManaged)
	if err != nil {
		return err
	}

	manifest := make(map[string]fileRecord, len(resStatic.manifest)+len(resManaged.manifest))
	for k, v := range resStatic.manifest {
		manifest[k] = v
	}
	for k, v := range resManaged.manifest {
		manifest[k] = v
	}

	// Rebuild effective local documents. Managed entries intentionally override
	// static entries with the same id, matching the original startup behavior.
	staticDocs := resStatic.docs
	managedDocs := resManaged.docs
	ids := make([]string, 0, len(staticDocs)+len(managedDocs))
	merged := map[string]model.KnowledgeDoc{}
	for id, d := range staticDocs {
		merged[id] = d
		ids = append(ids, id)
	}
	for id, d := range managedDocs {
		if _, ok := merged[id]; !ok {
			ids = append(ids, id)
		}
		merged[id] = d
	}
	sort.Strings(ids)
	localDocs := make([]model.KnowledgeDoc, 0, len(ids))
	for _, id := range ids {
		localDocs = append(localDocs, merged[id])
	}

	newTitle := make(map[string][]float64, len(localDocs)+len(externalDocs))
	newChunkVec := make(map[string][][]float64, len(localDocs)+len(externalDocs))
	newChunks := make(map[string][]string, len(localDocs)+len(externalDocs))
	needEmbed := make([]model.KnowledgeDoc, 0)
	reused := 0
	for _, d := range localDocs {
		parts := chunkText(d.Text, s.scoring.ChunkWords, s.scoring.ChunkOverlap, s.scoring.MaxChunksPerDoc)
		newChunks[d.ID] = parts
		if old, ok := oldDocs[d.ID]; ok && hashDoc(old, s.scoring) == hashDoc(d, s.scoring) && len(oldTitle[d.ID]) > 0 && (semanticMode == "neuroforge" || len(oldChunkVec[d.ID]) == len(parts)) {
			newTitle[d.ID] = oldTitle[d.ID]
			if semanticMode != "neuroforge" {
				newChunkVec[d.ID] = oldChunkVec[d.ID]
			}
			reused++
		} else if s.rag {
			needEmbed = append(needEmbed, d)
		}
	}

	semanticReady := true
	if s.rag && len(needEmbed) > 0 {
		if s.embedder == nil {
			return fmt.Errorf("RAG is enabled but no embedding provider is configured")
		}
		const docsPerBatch = 20
		for start := 0; start < len(needEmbed); start += docsPerBatch {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := start + docsPerBatch
			if end > len(needEmbed) {
				end = len(needEmbed)
			}
			batch := needEmbed[start:end]
			emb, err := s.embedDocuments(ctx, batch)
			if err != nil {
				return err
			}
			if ok, syncErr := s.syncSemanticDocuments(ctx, batch, emb); syncErr != nil {
				return syncErr
			} else if !ok {
				semanticReady = false
			}
			for _, d := range batch {
				newTitle[d.ID] = emb[d.ID].title
				newChunkVec[d.ID] = emb[d.ID].chunks
			}
		}
	}

	// Preserve connector-backed documents and vectors; their own syncers manage them.
	for _, d := range externalDocs {
		if _, collision := merged[d.ID]; collision {
			return fmt.Errorf("local knowledge id %q collides with external source %q", d.ID, externalMap[d.ID])
		}
		newTitle[d.ID] = oldTitle[d.ID]
		newChunkVec[d.ID] = oldChunkVec[d.ID]
		newChunks[d.ID] = oldChunks[d.ID]
	}

	for id := range oldDocs {
		if externalMap[id] != "" {
			continue
		}
		if _, stillLocal := merged[id]; !stillLocal {
			if err := s.deleteSemanticDocument(ctx, id); err != nil {
				return err
			}
		}
	}
	if semanticMode == "neuroforge" && semanticReady {
		for _, d := range localDocs {
			delete(newChunkVec, d.ID)
		}
	}

	files := map[string]string{}
	managedMap := map[string]bool{}
	for _, rec := range manifest {
		if !rec.Included || rec.ID == "" {
			continue
		}
		if rec.Managed {
			managedMap[rec.ID] = true
			files[rec.ID] = rec.Path
		} else if !managedMap[rec.ID] {
			files[rec.ID] = rec.Path
		}
	}
	stats := mergeLoadStats(resStatic.stats, resManaged.stats)
	allDocs := append(localDocs, externalDocs...)

	changed := resStatic.changed + resManaged.changed
	deleted := countDeleted(oldManifest, manifest)
	s.mu.Lock()
	s.docs = allDocs
	s.files = files
	s.managed = managedMap
	s.staticDocs = staticDocs
	s.titleVectors = newTitle
	s.chunkVectors = newChunkVec
	s.chunks = newChunks
	s.manifest = manifest
	s.loadStats = stats
	s.initStatus.State = "ready"
	s.initStatus.Phase = "ready"
	s.initStatus.TotalFiles = len(manifest)
	s.initStatus.ProcessedFiles = len(manifest)
	s.initStatus.LoadedDocs = len(localDocs)
	s.initStatus.IndexedDocs = len(localDocs)
	s.initStatus.PendingEmbeddings = 0
	s.initStatus.LastScanAt = time.Now()
	s.initStatus.ChangedFiles = changed
	s.initStatus.DeletedFiles = deleted
	s.initStatus.ReusedFiles = reused
	s.initStatus.LastScanError = ""
	s.mu.Unlock()

	if changed > 0 || deleted > 0 {
		if err := s.persistSnapshot(); err != nil {
			return fmt.Errorf("persist incremental knowledge index: %w", err)
		}
	}
	slog.Info("incremental knowledge scan complete", "files", len(manifest), "documents", len(localDocs), "changed_files", changed, "deleted_files", deleted, "reused_vectors", reused, "embedded_documents", len(needEmbed))
	return nil
}

type deltaScanResult struct {
	docs     map[string]model.KnowledgeDoc
	manifest map[string]fileRecord
	stats    LoadStats
	changed  int
}

func (s *Store) scanDeltaDir(ctx context.Context, dir, origin string, managed bool, opts LoadOptions, old map[string]fileRecord, oldDocs map[string]model.KnowledgeDoc) (deltaScanResult, error) {
	res := deltaScanResult{docs: map[string]model.KnowledgeDoc{}, manifest: map[string]fileRecord{}}
	entries, err := listKnowledgeJSONFiles(dir)
	if err != nil {
		return res, fmt.Errorf("read knowledge directory %q: %w", dir, err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		key := origin + "/" + e.Rel
		path := e.Path
		info := e.Info
		if matchesAnyGlob(e.Rel, opts.IgnoreGlobs) {
			res.manifest[key] = fileRecord{Key: key, Path: path, Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano(), Managed: managed, Ignored: true}
			res.stats.IgnoredFiles++
			continue
		}
		if prev, ok := old[key]; ok && prev.Size == info.Size() && prev.ModTimeUnixNano == info.ModTime().UnixNano() {
			prev.Path = path
			res.manifest[key] = prev
			if prev.Included && prev.ID != "" {
				if d, ok := oldDocs[prev.ID]; ok {
					res.docs[prev.ID] = d
				}
			}
			for _, u := range prev.Unmapped {
				res.stats.UnmappedCategories = appendUniqueString(res.stats.UnmappedCategories, u)
			}
			if len(prev.Unmapped) > 0 {
				res.stats.UnmappedCategoryFiles++
			}
			if prev.Ignored {
				res.stats.IgnoredFiles++
			}
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return res, err
		}
		h := sha256.Sum256(b)
		rawHash := hex.EncodeToString(h[:])
		if prev, ok := old[key]; ok && prev.RawHash != "" && prev.RawHash == rawHash {
			prev.Path = path
			prev.Size = info.Size()
			prev.ModTimeUnixNano = info.ModTime().UnixNano()
			res.manifest[key] = prev
			if prev.Included && prev.ID != "" {
				if d, ok := oldDocs[prev.ID]; ok {
					res.docs[prev.ID] = d
				}
			}
			continue
		}
		d, unmapped, skip, err := decodeKnowledgeDoc(b, opts.CategoryMode, s.categoryMap)
		if err != nil {
			return res, fmt.Errorf("%s: %w", e.Rel, err)
		}
		rec := fileRecord{Key: key, Path: path, Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano(), RawHash: rawHash, Managed: managed, Unmapped: append([]string(nil), unmapped...)}
		if len(unmapped) > 0 {
			res.stats.UnmappedCategoryFiles++
			res.stats.UnmappedCategories = mergeStrings(res.stats.UnmappedCategories, unmapped)
		}
		if skip {
			rec.Ignored = true
			res.stats.IgnoredFiles++
			res.manifest[key] = rec
			res.changed++
			continue
		}
		if d.ID == "" || d.Title == "" {
			return res, fmt.Errorf("%s: id/title required", e.Rel)
		}
		if !safeID(d.ID) {
			return res, fmt.Errorf("%s: invalid id %q", e.Rel, d.ID)
		}
		d.Source = strings.ToLower(strings.TrimSpace(d.Source))
		if d.Source == "" {
			return res, fmt.Errorf("%s: source required", e.Rel)
		}
		if _, allowed := s.allowedSources[d.Source]; !allowed {
			res.manifest[key] = rec
			res.changed++
			continue
		}
		d.Language = strings.TrimSpace(d.Language)
		d.CommunicationStyle = strings.ToLower(strings.TrimSpace(d.CommunicationStyle))
		rec.ID = d.ID
		rec.Included = true
		res.manifest[key] = rec
		if _, dup := res.docs[d.ID]; dup {
			return res, fmt.Errorf("duplicate knowledge id %q in %s", d.ID, dir)
		}
		res.docs[d.ID] = d
		res.changed++
	}
	return res, nil
}

func countDeleted(old, cur map[string]fileRecord) int {
	n := 0
	for k := range old {
		if _, ok := cur[k]; !ok {
			n++
		}
	}
	return n
}

func mergeLoadStats(a, b LoadStats) LoadStats {
	return LoadStats{IgnoredFiles: a.IgnoredFiles + b.IgnoredFiles, UnmappedCategoryFiles: a.UnmappedCategoryFiles + b.UnmappedCategoryFiles, UnmappedCategories: mergeStrings(a.UnmappedCategories, b.UnmappedCategories)}
}

func buildManifestForDocs(docs []model.KnowledgeDoc, files []string, root, origin string, managed bool) (map[string]fileRecord, error) {
	out := make(map[string]fileRecord, len(docs))
	for i, d := range docs {
		if i >= len(files) {
			return nil, fmt.Errorf("knowledge manifest mismatch: %d docs, %d files", len(docs), len(files))
		}
		path := files[i]
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		h := sha256.Sum256(b)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		key := origin + "/" + filepath.ToSlash(rel)
		out[key] = fileRecord{Key: key, Path: path, ID: d.ID, Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano(), RawHash: hex.EncodeToString(h[:]), Managed: managed, Included: true, Unmapped: append([]string(nil), d.UnmappedExternalCategories...)}
	}
	return out, nil
}

func (s *Store) persistExternalVectorCache(source string) error {
	if s == nil || !s.rag {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.externalCachePath), 0o750); err != nil {
		return err
	}
	s.mu.RLock()
	cf := cacheFile{Version: 3, Hashes: map[string]string{}, TitleVectors: map[string][]float64{}, ChunkVectors: map[string][][]float64{}}
	for _, d := range s.docs {
		if s.external[d.ID] != source || len(s.titleVectors[d.ID]) == 0 {
			continue
		}
		cf.Hashes[d.ID] = hashDoc(d, s.scoring)
		cf.TitleVectors[d.ID] = append([]float64(nil), s.titleVectors[d.ID]...)
		cf.ChunkVectors[d.ID] = cloneChunkVectors(s.chunkVectors[d.ID])
	}
	s.mu.RUnlock()
	b, err := json.Marshal(cf)
	if err != nil {
		return err
	}
	tmp := s.externalCachePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.externalCachePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// augmentManifestAllFiles records even filtered/ignored JSON files so periodic
// delta scans do not repeatedly open files that are intentionally not part of
// the active corpus.
func augmentManifestAllFiles(dir, origin string, managed bool, opts LoadOptions, manifest map[string]fileRecord) error {
	entries, err := listKnowledgeJSONFiles(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		key := origin + "/" + e.Rel
		if _, ok := manifest[key]; ok {
			continue
		}
		info := e.Info
		path := e.Path
		rec := fileRecord{Key: key, Path: path, Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano(), Managed: managed, Included: false, Ignored: matchesAnyGlob(e.Rel, opts.IgnoreGlobs)}
		if !rec.Ignored {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h := sha256.Sum256(b)
			rec.RawHash = hex.EncodeToString(h[:])
		}
		manifest[key] = rec
	}
	return nil
}
