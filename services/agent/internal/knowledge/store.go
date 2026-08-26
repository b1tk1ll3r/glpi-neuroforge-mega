package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/example/glpi-ai-agent/internal/brainactivity"
	"github.com/example/glpi-ai-agent/internal/model"
)

type Embedder interface {
	Embed(context.Context, []string) ([][]float64, error)
}
type ScoringConfig struct {
	SemanticWeight    float64
	TitleWeight       float64
	LexicalWeight     float64
	KeywordWeight     float64
	CategoryWeight    float64
	EmbeddingProfile  string
	EmbeddingIdentity string
	ChunkWords        int
	ChunkOverlap      int
	MaxChunksPerDoc   int
	MaxQueryChunks    int
	IndexMode         string
	EmbedBatchSize    int
	IndexScanInterval time.Duration
	CategoryMode      string
	CategoryMapFile   string
	IgnoreGlobs       []string
}

type LoadOptions struct {
	CategoryMode    string
	CategoryMapFile string
	IgnoreGlobs     []string
}

type LoadStats struct {
	IgnoredFiles          int      `json:"ignored_files"`
	UnmappedCategoryFiles int      `json:"unmapped_category_files"`
	UnmappedCategories    []string `json:"unmapped_categories,omitempty"`
}

// InitStatus exposes the asynchronous local knowledge startup state to the
// dashboard and readiness endpoint. The HTTP server can therefore be available
// while a large corpus is still being scanned or embedded.
type InitStatus struct {
	State             string    `json:"state"`
	Phase             string    `json:"phase"`
	TotalFiles        int       `json:"total_files"`
	ProcessedFiles    int       `json:"processed_files"`
	LoadedDocs        int       `json:"loaded_docs"`
	IndexedDocs       int       `json:"indexed_docs"`
	CacheHits         int       `json:"cache_hits"`
	PendingEmbeddings int       `json:"pending_embeddings"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	FinishedAt        time.Time `json:"finished_at,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	SnapshotLoaded    bool      `json:"snapshot_loaded"`
	SnapshotPath      string    `json:"snapshot_path,omitempty"`
	SnapshotSavedAt   time.Time `json:"snapshot_saved_at,omitempty"`
	LastScanAt        time.Time `json:"last_scan_at,omitempty"`
	LastScanError     string    `json:"last_scan_error,omitempty"`
	ChangedFiles      int       `json:"changed_files"`
	DeletedFiles      int       `json:"deleted_files"`
	ReusedFiles       int       `json:"reused_files"`
}

type Store struct {
	mu                      sync.RWMutex
	initMu                  sync.Mutex
	categoryMapWriteMu      sync.Mutex
	initStatus              InitStatus
	dir                     string
	managedDir              string
	docs                    []model.KnowledgeDoc
	files                   map[string]string
	managed                 map[string]bool
	external                map[string]string
	staticDocs              map[string]model.KnowledgeDoc
	titleVectors            map[string][]float64
	chunkVectors            map[string][][]float64
	chunks                  map[string][]string
	embedder                Embedder
	rag                     bool
	cachePath               string
	allowedSources          map[string]struct{}
	scoring                 ScoringConfig
	loadOptions             LoadOptions
	loadStats               LoadStats
	categoryMap             map[string][]int64
	manifest                map[string]fileRecord
	snapshotPath            string
	externalCachePath       string
	semanticBackend         SemanticBackend
	semanticBackendMode     string
	semanticBackendSearchK  int
	semanticBackendFailOpen bool
}
type cacheFile struct {
	Version      int                    `json:"version,omitempty"`
	Hashes       map[string]string      `json:"hashes"`
	TitleVectors map[string][]float64   `json:"title_vectors,omitempty"`
	ChunkVectors map[string][][]float64 `json:"chunk_vectors,omitempty"`
}

func DefaultScoringConfig() ScoringConfig {
	return ScoringConfig{SemanticWeight: .45, TitleWeight: .20, LexicalWeight: .20, KeywordWeight: .075, CategoryWeight: .075, EmbeddingProfile: "plain", ChunkWords: 160, ChunkOverlap: 30, MaxChunksPerDoc: 24, MaxQueryChunks: 64, IndexMode: "incremental", EmbedBatchSize: 64, IndexScanInterval: 5 * time.Minute}
}

// ResolveEmbeddingProfile selects prompt formatting for the configured embedding model.
// EmbeddingGemma benefits from distinct retrieval-query and retrieval-document prompts.
func ResolveEmbeddingProfile(profile, model string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "auto" {
		if strings.Contains(strings.ToLower(model), "embeddinggemma") {
			return "embeddinggemma"
		}
		return "plain"
	}
	return p
}

func normalizeScoring(c ScoringConfig) ScoringConfig {
	d := DefaultScoringConfig()
	if c.SemanticWeight < 0 || c.TitleWeight < 0 || c.LexicalWeight < 0 || c.KeywordWeight < 0 || c.CategoryWeight < 0 || c.SemanticWeight+c.TitleWeight+c.LexicalWeight+c.KeywordWeight+c.CategoryWeight <= 0 {
		c.SemanticWeight, c.TitleWeight, c.LexicalWeight, c.KeywordWeight, c.CategoryWeight = d.SemanticWeight, d.TitleWeight, d.LexicalWeight, d.KeywordWeight, d.CategoryWeight
	}
	if c.EmbeddingProfile == "" {
		c.EmbeddingProfile = d.EmbeddingProfile
	}
	if c.ChunkWords <= 0 {
		c.ChunkWords = d.ChunkWords
	}
	if c.ChunkOverlap < 0 || c.ChunkOverlap >= c.ChunkWords {
		c.ChunkOverlap = d.ChunkOverlap
	}
	if c.MaxChunksPerDoc <= 0 {
		c.MaxChunksPerDoc = d.MaxChunksPerDoc
	}
	if c.MaxQueryChunks <= 0 {
		c.MaxQueryChunks = d.MaxQueryChunks
	}
	if strings.TrimSpace(c.IndexMode) == "" {
		c.IndexMode = d.IndexMode
	}
	c.IndexMode = strings.ToLower(strings.TrimSpace(c.IndexMode))
	if c.EmbedBatchSize <= 0 {
		c.EmbedBatchSize = d.EmbedBatchSize
	}
	if c.IndexScanInterval < 0 {
		c.IndexScanInterval = d.IndexScanInterval
	}
	return c
}

func NewStore(dir, dataDir string, embedder Embedder, rag bool, allowedSources []string, scoring ...ScoringConfig) (*Store, error) {
	managedDir := filepath.Join(dataDir, "knowledge-managed")
	if err := os.MkdirAll(managedDir, 0o750); err != nil {
		return nil, fmt.Errorf("create managed knowledge directory: %w", err)
	}
	scoreCfg := DefaultScoringConfig()
	if len(scoring) > 0 {
		scoreCfg = normalizeScoring(scoring[0])
	}
	loadOpts := LoadOptions{CategoryMode: scoreCfg.CategoryMode, CategoryMapFile: scoreCfg.CategoryMapFile, IgnoreGlobs: scoreCfg.IgnoreGlobs}
	if strings.TrimSpace(loadOpts.CategoryMode) == "" {
		loadOpts.CategoryMode = "unscoped"
	}
	categoryMap, err := loadCategoryMap(loadOpts.CategoryMapFile)
	if err != nil {
		return nil, err
	}
	s := &Store{
		dir: dir, managedDir: managedDir,
		titleVectors: map[string][]float64{}, chunkVectors: map[string][][]float64{}, chunks: map[string][]string{},
		files: map[string]string{}, managed: map[string]bool{}, external: map[string]string{}, staticDocs: map[string]model.KnowledgeDoc{},
		embedder: embedder, rag: rag, cachePath: filepath.Join(dataDir, "embeddings.json"), allowedSources: map[string]struct{}{},
		scoring: scoreCfg, loadOptions: loadOpts, categoryMap: categoryMap, manifest: map[string]fileRecord{},
		snapshotPath: filepath.Join(dataDir, "knowledge-index", "snapshot.gob"), externalCachePath: filepath.Join(dataDir, "knowledge-index", "external-embeddings.json"),
		initStatus: InitStatus{State: "waiting", Phase: "waiting"},
	}
	for _, source := range allowedSources {
		s.allowedSources[strings.ToLower(strings.TrimSpace(source))] = struct{}{}
	}
	return s, nil
}

// Load keeps the synchronous API used by tests and small deployments. The main
// application uses NewStore + Initialize in a goroutine so the Web UI is
// reachable immediately even for very large knowledge directories.
func Load(ctx context.Context, dir, dataDir string, embedder Embedder, rag bool, allowedSources []string, scoring ...ScoringConfig) (*Store, error) {
	s, err := NewStore(dir, dataDir, embedder, rag, allowedSources, scoring...)
	if err != nil {
		return nil, err
	}
	if err := s.Initialize(ctx); err != nil {
		return s, err
	}
	return s, nil
}

// Initialize scans, validates and indexes the local knowledge corpus. It is
// safe to call from a background goroutine. Until it succeeds Ready() is false,
// so ticket processing can stay paused while the dashboard remains available.
func (s *Store) Initialize(ctx context.Context) (err error) {
	if s == nil {
		return fmt.Errorf("knowledge store is not initialized")
	}
	mode := strings.ToLower(strings.TrimSpace(s.scoring.IndexMode))
	if mode == "" {
		mode = "incremental"
	}
	if mode != "rebuild" {
		loaded, loadErr := s.loadPersistentSnapshot()
		if loadErr != nil {
			if mode == "readonly" {
				return loadErr
			}
			slog.Warn("persistent knowledge index unavailable; falling back to rebuild", "error", loadErr, "path", s.snapshotPath)
		} else if loaded {
			if _, err := s.syncLoadedSemanticBackend(ctx); err != nil {
				return err
			}
			return nil
		} else if mode == "readonly" {
			return fmt.Errorf("KNOWLEDGE_INDEX_MODE=readonly requires a compatible persistent index at %s", s.snapshotPath)
		}
	}
	return s.fullRebuild(ctx)
}

func (s *Store) fullRebuild(ctx context.Context) (err error) {
	s.initMu.Lock()
	defer s.initMu.Unlock()

	s.setInitStatus(func(st *InitStatus) {
		*st = InitStatus{State: "loading", Phase: "scanning", StartedAt: time.Now(), SnapshotPath: s.snapshotPath}
	})
	defer func() {
		if err != nil {
			s.setInitStatus(func(st *InitStatus) {
				st.State = "error"
				st.Phase = "error"
				st.LastError = err.Error()
				st.FinishedAt = time.Now()
			})
		}
	}()

	staticProcessed, staticTotal := 0, 0
	static, staticFiles, stats, err := readDocs(s.dir, s.allowedSources, s.loadOptions, s.categoryMap, func(total, processed, loaded int) {
		staticTotal, staticProcessed = total, processed
		s.setInitStatus(func(st *InitStatus) {
			st.TotalFiles = total
			st.ProcessedFiles = processed
			st.LoadedDocs = loaded
		})
		if processed > 0 && processed%1000 == 0 {
			slog.Info("knowledge scan progress", "processed_files", processed, "total_files", total, "loaded_docs", loaded)
		}
	})
	if err != nil {
		return err
	}
	managedTotal := 0
	managed, managedFiles, managedStats, err := readDocs(s.managedDir, s.allowedSources, LoadOptions{CategoryMode: "strict"}, s.categoryMap, func(total, processed, loaded int) {
		managedTotal = total
		s.setInitStatus(func(st *InitStatus) {
			st.TotalFiles = staticTotal + total
			st.ProcessedFiles = staticProcessed + processed
			st.LoadedDocs = len(static) + loaded
		})
	})
	if err != nil {
		return err
	}
	stats.IgnoredFiles += managedStats.IgnoredFiles
	stats.UnmappedCategoryFiles += managedStats.UnmappedCategoryFiles
	stats.UnmappedCategories = mergeStrings(stats.UnmappedCategories, managedStats.UnmappedCategories)

	staticManifest, err := buildManifestForDocs(static, staticFiles, "static", false)
	if err != nil {
		return fmt.Errorf("build static knowledge manifest: %w", err)
	}
	managedManifest, err := buildManifestForDocs(managed, managedFiles, "managed", true)
	if err != nil {
		return fmt.Errorf("build managed knowledge manifest: %w", err)
	}
	manifest := make(map[string]fileRecord, len(staticManifest)+len(managedManifest))
	for k, v := range staticManifest {
		manifest[k] = v
	}
	for k, v := range managedManifest {
		manifest[k] = v
	}
	if err := augmentManifestAllFiles(s.dir, "static", false, s.loadOptions, manifest); err != nil {
		return fmt.Errorf("complete static knowledge manifest: %w", err)
	}
	if err := augmentManifestAllFiles(s.managedDir, "managed", true, LoadOptions{CategoryMode: "strict"}, manifest); err != nil {
		return fmt.Errorf("complete managed knowledge manifest: %w", err)
	}

	files := map[string]string{}
	managedMap := map[string]bool{}
	staticMap := map[string]model.KnowledgeDoc{}
	merged := map[string]model.KnowledgeDoc{}
	order := []string{}
	for i, d := range static {
		staticMap[d.ID] = d
		files[d.ID] = staticFiles[i]
		if _, ok := merged[d.ID]; !ok {
			order = append(order, d.ID)
		}
		merged[d.ID] = d
	}
	for i, d := range managed {
		if _, ok := merged[d.ID]; !ok {
			order = append(order, d.ID)
		}
		merged[d.ID] = d
		files[d.ID] = managedFiles[i]
		managedMap[d.ID] = true
	}
	docs := make([]model.KnowledgeDoc, 0, len(order))
	for _, id := range order {
		docs = append(docs, merged[id])
	}

	s.mu.Lock()
	s.docs = docs
	s.files = files
	s.managed = managedMap
	s.external = map[string]string{}
	s.staticDocs = staticMap
	s.loadStats = stats
	s.manifest = manifest
	s.titleVectors = map[string][]float64{}
	s.chunkVectors = map[string][][]float64{}
	s.chunks = map[string][]string{}
	s.mu.Unlock()
	s.setInitStatus(func(st *InitStatus) {
		st.Phase = "indexing"
		st.TotalFiles = staticTotal + managedTotal
		st.ProcessedFiles = st.TotalFiles
		st.LoadedDocs = len(docs)
	})
	slog.Info("knowledge scan complete", "documents", len(docs), "ignored_files", stats.IgnoredFiles, "unmapped_category_files", stats.UnmappedCategoryFiles)

	if s.rag && len(docs) > 0 {
		if s.embedder == nil {
			return fmt.Errorf("RAG is enabled but no embedding provider is configured")
		}
		if err := s.index(ctx); err != nil {
			return err
		}
	}
	if err := s.persistSnapshot(); err != nil {
		return fmt.Errorf("persist knowledge snapshot: %w", err)
	}
	s.setInitStatus(func(st *InitStatus) {
		st.State = "ready"
		st.Phase = "ready"
		st.IndexedDocs = len(docs)
		st.PendingEmbeddings = 0
		st.FinishedAt = time.Now()
		st.LastError = ""
		st.SnapshotLoaded = false
		st.SnapshotPath = s.snapshotPath
		st.SnapshotSavedAt = time.Now()
	})
	slog.Info("knowledge store ready", "documents", len(docs), "rag_enabled", s.rag, "persistent_index", s.snapshotPath)
	return nil
}

func (s *Store) setInitStatus(fn func(*InitStatus)) {
	s.mu.Lock()
	fn(&s.initStatus)
	s.mu.Unlock()
}

func (s *Store) InitStatus() InitStatus {
	if s == nil {
		return InitStatus{State: "error", Phase: "error", LastError: "knowledge store is nil"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initStatus
}

func (s *Store) Ready() bool { return s != nil && s.InitStatus().State == "ready" }

func readDocs(dir string, allowed map[string]struct{}, opts LoadOptions, categoryMap map[string][]int64, progress func(total, processed, loaded int)) ([]model.KnowledgeDoc, []string, LoadStats, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, LoadStats{}, fmt.Errorf("read knowledge directory %q: %w", dir, err)
	}
	var docs []model.KnowledgeDoc
	var files []string
	stats := LoadStats{}
	total := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			total++
		}
	}
	processed := 0
	if progress != nil {
		progress(total, 0, 0)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		processed++
		if matchesAnyGlob(e.Name(), opts.IgnoreGlobs) {
			stats.IgnoredFiles++
			if progress != nil {
				progress(total, processed, len(docs))
			}
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, stats, err
		}
		d, unmapped, skip, err := decodeKnowledgeDoc(b, opts.CategoryMode, categoryMap)
		if err != nil {
			return nil, nil, stats, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if len(unmapped) > 0 {
			stats.UnmappedCategoryFiles++
			stats.UnmappedCategories = mergeStrings(stats.UnmappedCategories, unmapped)
		}
		if skip {
			stats.IgnoredFiles++
			if progress != nil {
				progress(total, processed, len(docs))
			}
			continue
		}
		if d.ID == "" || d.Title == "" {
			return nil, nil, stats, fmt.Errorf("%s: id/title required", e.Name())
		}
		if !safeID(d.ID) {
			return nil, nil, stats, fmt.Errorf("%s: invalid id %q", e.Name(), d.ID)
		}
		d.Source = strings.ToLower(strings.TrimSpace(d.Source))
		if d.Source == "" {
			return nil, nil, stats, fmt.Errorf("%s: source required", e.Name())
		}
		if _, ok := allowed[d.Source]; !ok {
			if progress != nil {
				progress(total, processed, len(docs))
			}
			continue
		}
		d.Language = strings.TrimSpace(d.Language)
		d.CommunicationStyle = strings.ToLower(strings.TrimSpace(d.CommunicationStyle))
		docs = append(docs, d)
		files = append(files, path)
		if progress != nil {
			progress(total, processed, len(docs))
		}
	}
	return docs, files, stats, nil
}

func decodeKnowledgeDoc(b []byte, mode string, categoryMap map[string][]int64) (model.KnowledgeDoc, []string, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return model.KnowledgeDoc{}, nil, false, err
	}
	catRaw := raw["categories"]
	raw["categories"] = json.RawMessage(`[]`)
	normalized, err := json.Marshal(raw)
	if err != nil {
		return model.KnowledgeDoc{}, nil, false, err
	}
	var d model.KnowledgeDoc
	if err := json.Unmarshal(normalized, &d); err != nil {
		return model.KnowledgeDoc{}, nil, false, err
	}
	ids, labels, unmapped, err := parseKnowledgeCategories(catRaw, categoryMap)
	if err != nil {
		return model.KnowledgeDoc{}, nil, false, err
	}
	d.Categories = ids
	d.ExternalCategories = labels
	d.UnmappedExternalCategories = append([]string(nil), unmapped...)
	if len(unmapped) == 0 {
		return d, nil, false, nil
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "strict":
		return model.KnowledgeDoc{}, unmapped, false, fmt.Errorf("unmapped external categories: %s", strings.Join(unmapped, ", "))
	case "skip":
		return d, unmapped, true, nil
	default:
		// Unmapped external taxonomies remain searchable, but may not trigger an automatic reply.
		d.AutoReply = false
		return d, unmapped, false, nil
	}
}

func parseKnowledgeCategories(raw json.RawMessage, categoryMap map[string][]int64) ([]int64, []string, []string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, nil, nil, err
	}
	items, ok := v.([]any)
	if !ok {
		items = []any{v}
	}
	ids := []int64{}
	labels := []string{}
	unmapped := []string{}
	for _, item := range items {
		itemIDs, label, err := parseCategoryItem(item, categoryMap)
		if err != nil {
			return nil, nil, nil, err
		}
		ids = append(ids, itemIDs...)
		if label != "" {
			labels = appendUniqueString(labels, label)
			if len(itemIDs) == 0 {
				unmapped = appendUniqueString(unmapped, label)
			}
		}
	}
	return uniqueInt64(ids), labels, unmapped, nil
}

func parseCategoryItem(v any, categoryMap map[string][]int64) ([]int64, string, error) {
	switch x := v.(type) {
	case nil:
		return nil, "", nil
	case float64:
		if x <= 0 || math.Trunc(x) != x {
			return nil, "", fmt.Errorf("category id must be a positive integer")
		}
		return []int64{int64(x)}, "", nil
	case string:
		label := strings.TrimSpace(x)
		if label == "" {
			return nil, "", nil
		}
		if n, err := strconv.ParseInt(label, 10, 64); err == nil && n > 0 {
			return []int64{n}, "", nil
		}
		return append([]int64(nil), categoryMap[normalizeCategoryLabel(label)]...), label, nil
	case map[string]any:
		if id, ok := x["id"]; ok {
			ids, _, err := parseCategoryItem(id, categoryMap)
			if err == nil && len(ids) > 0 {
				return ids, "", nil
			}
		}
		for _, key := range []string{"name", "label", "title"} {
			if val, ok := x[key].(string); ok {
				return parseCategoryItem(val, categoryMap)
			}
		}
		return nil, "", fmt.Errorf("unsupported category object")
	default:
		return nil, "", fmt.Errorf("unsupported category value type %T", v)
	}
}

func loadCategoryMap(path string) (map[string][]int64, error) {
	out := map[string][]int64{}
	path = strings.TrimSpace(path)
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("read knowledge category map %q: %w", path, err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("parse knowledge category map %q: %w", path, err)
	}
	if nested, ok := root["mappings"]; ok {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(nested, &m); err != nil {
			return nil, fmt.Errorf("parse mappings in %q: %w", path, err)
		}
		root = m
	}
	for label, rv := range root {
		ids, err := parseMappingIDs(rv)
		if err != nil {
			return nil, fmt.Errorf("category mapping %q: %w", label, err)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("category mapping %q contains no positive GLPI ids", label)
		}
		out[normalizeCategoryLabel(label)] = uniqueInt64(ids)
	}
	return out, nil
}

func parseMappingIDs(raw json.RawMessage) ([]int64, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	items, ok := v.([]any)
	if !ok {
		items = []any{v}
	}
	ids := []int64{}
	for _, item := range items {
		switch x := item.(type) {
		case float64:
			if x <= 0 || math.Trunc(x) != x {
				return nil, fmt.Errorf("id must be a positive integer")
			}
			ids = append(ids, int64(x))
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("%q is not a positive GLPI category id", x)
			}
			ids = append(ids, n)
		default:
			return nil, fmt.Errorf("unsupported mapping value type %T", item)
		}
	}
	return ids, nil
}

func matchesAnyGlob(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}
func normalizeCategoryLabel(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
func uniqueInt64(in []int64) []int64 {
	seen := map[int64]struct{}{}
	out := []int64{}
	for _, v := range in {
		if v <= 0 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
func appendUniqueString(in []string, s string) []string {
	for _, v := range in {
		if strings.EqualFold(v, s) {
			return in
		}
	}
	return append(in, s)
}
func mergeStrings(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, s := range b {
		out = appendUniqueString(out, s)
	}
	sort.Strings(out)
	return out
}

func (s *Store) LoadStats() LoadStats {
	if s == nil {
		return LoadStats{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.loadStats
	out.UnmappedCategories = append([]string(nil), s.loadStats.UnmappedCategories...)
	return out
}

func (s *Store) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.docs)
}
func (s *Store) ByID(id string) (model.KnowledgeDoc, bool) {
	if s == nil {
		return model.KnowledgeDoc{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.docs {
		if d.ID == id {
			return d, true
		}
	}
	return model.KnowledgeDoc{}, false
}
func (s *Store) List() []model.KnowledgeDoc {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]model.KnowledgeDoc(nil), s.docs...)
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out
}

func (s *Store) Upsert(ctx context.Context, d model.KnowledgeDoc) error {
	if s == nil {
		return fmt.Errorf("knowledge store is not initialized")
	}
	if !s.Ready() {
		return fmt.Errorf("knowledge store is still initializing")
	}
	d.ID = strings.TrimSpace(d.ID)
	d.Title = strings.TrimSpace(d.Title)
	d.Text = strings.TrimSpace(d.Text)
	d.Answer = strings.TrimSpace(d.Answer)
	d.Source = strings.ToLower(strings.TrimSpace(d.Source))
	d.Language = strings.TrimSpace(d.Language)
	d.CommunicationStyle = strings.ToLower(strings.TrimSpace(d.CommunicationStyle))
	if d.ID == "" || d.Title == "" {
		return fmt.Errorf("id/title required")
	}
	if !safeID(d.ID) {
		return fmt.Errorf("knowledge id may contain only letters, digits, dot, dash and underscore")
	}
	if d.Source == "" {
		return fmt.Errorf("source required")
	}
	if _, ok := s.allowedSources[d.Source]; !ok {
		return fmt.Errorf("source %q is not allowed", d.Source)
	}
	if d.Language == "" || d.CommunicationStyle == "" {
		return fmt.Errorf("language and communication_style required")
	}
	if d.MinScore < 0 || d.MinScore > 1 {
		return fmt.Errorf("min_score must be between 0 and 1")
	}
	s.mu.RLock()
	_, exists := s.files[d.ID]
	isManaged := s.managed[d.ID]
	externalSource := s.external[d.ID]
	s.mu.RUnlock()
	if externalSource != "" {
		return fmt.Errorf("externally synchronized knowledge entry %q from %q is read-only", d.ID, externalSource)
	}
	if exists && !isManaged {
		return fmt.Errorf("static knowledge entry %q is read-only; use a new id for a managed entry", d.ID)
	}

	var titleVector []float64
	var chunkVectors [][]float64
	chunks := chunkText(d.Text, s.scoring.ChunkWords, s.scoring.ChunkOverlap, s.scoring.MaxChunksPerDoc)
	if s.rag {
		if s.embedder == nil {
			return fmt.Errorf("RAG is enabled but no embedding provider is configured")
		}
		embedded, err := s.embedDocuments(ctx, []model.KnowledgeDoc{d})
		if err != nil {
			return err
		}
		titleVector = embedded[d.ID].title
		chunkVectors = embedded[d.ID].chunks
	}

	path := filepath.Join(s.managedDir, d.ID+".json")
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return statErr
	}
	rawSum := sha256.Sum256(b)
	manifestKey := "managed/" + filepath.Base(path)
	s.mu.Lock()
	replaced := false
	for i := range s.docs {
		if s.docs[i].ID == d.ID {
			s.docs[i] = d
			replaced = true
			break
		}
	}
	if !replaced {
		s.docs = append(s.docs, d)
	}
	s.files[d.ID] = path
	s.managed[d.ID] = true
	s.manifest[manifestKey] = fileRecord{Key: manifestKey, Path: path, ID: d.ID, Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano(), RawHash: hex.EncodeToString(rawSum[:]), Managed: true, Included: true, Unmapped: append([]string(nil), d.UnmappedExternalCategories...)}
	s.chunks[d.ID] = chunks
	if s.rag {
		s.titleVectors[d.ID] = titleVector
		s.chunkVectors[d.ID] = chunkVectors
	}
	s.mu.Unlock()
	semanticOK := true
	if s.rag && len(chunkVectors) > 0 {
		if err := s.syncSemanticDocument(ctx, d, chunks, chunkVectors); err != nil {
			semanticOK = false
			if handled := s.handleSemanticSyncError(err); handled != nil {
				return fmt.Errorf("knowledge saved locally but %w", handled)
			}
		}
	}
	if semanticOK && s.semanticExternalized() {
		s.mu.Lock()
		delete(s.chunkVectors, d.ID)
		s.mu.Unlock()
	}
	return s.persistVectorCache()
}

func (s *Store) Delete(id string) error {
	if s == nil {
		return fmt.Errorf("knowledge store is not initialized")
	}
	if !s.Ready() {
		return fmt.Errorf("knowledge store is still initializing")
	}
	id = strings.TrimSpace(id)
	if !safeID(id) {
		return fmt.Errorf("invalid knowledge id")
	}
	s.mu.RLock()
	path := s.files[id]
	isManaged := s.managed[id]
	s.mu.RUnlock()
	if path == "" {
		return os.ErrNotExist
	}
	if !isManaged {
		return fmt.Errorf("static knowledge entry %q is read-only", id)
	}
	if err := s.deleteSemanticDocument(context.Background(), id); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	s.mu.Lock()
	out := s.docs[:0]
	for _, d := range s.docs {
		if d.ID != id {
			out = append(out, d)
		}
	}
	s.docs = append([]model.KnowledgeDoc(nil), out...)
	delete(s.files, id)
	delete(s.managed, id)
	for key, rec := range s.manifest {
		if rec.Managed && rec.ID == id {
			delete(s.manifest, key)
		}
	}
	delete(s.titleVectors, id)
	delete(s.chunkVectors, id)
	delete(s.chunks, id)
	s.mu.Unlock()
	return s.persistVectorCache()
}

func (s *Store) IsManaged(id string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.managed[id]
}
func (s *Store) Origin(id string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.managed[id] {
		return "managed"
	}
	if src := s.external[id]; src != "" {
		return src
	}
	if _, ok := s.staticDocs[id]; ok {
		return "static"
	}
	return ""
}

// ReplaceExternalSource atomically replaces all read-only documents imported
// from one connector source. Embeddings are reused when the normalized article
// did not change. Long article bodies are indexed as overlapping chunks.
func (s *Store) ReplaceExternalSource(ctx context.Context, source string, docs []model.KnowledgeDoc) error {
	if s == nil {
		return fmt.Errorf("knowledge store is not initialized")
	}
	source = strings.ToLower(strings.TrimSpace(source))
	if _, ok := s.allowedSources[source]; !ok {
		return fmt.Errorf("source %q is not allowed", source)
	}

	s.mu.RLock()
	oldDocs := make(map[string]model.KnowledgeDoc, len(s.docs))
	oldTitle := cloneVectorMap(s.titleVectors)
	oldChunks := cloneChunkVectorMap(s.chunkVectors)
	oldExternal := make(map[string]string, len(s.external))
	for _, d := range s.docs {
		oldDocs[d.ID] = d
	}
	for id, src := range s.external {
		oldExternal[id] = src
	}
	s.mu.RUnlock()
	cached := loadCache(s.externalCachePath)
	if len(cached.Hashes) == 0 {
		cached = loadCache(s.cachePath) // one-time migration from the legacy combined cache
	}

	changed := make([]model.KnowledgeDoc, 0)
	seen := map[string]struct{}{}
	for i := range docs {
		d := &docs[i]
		d.ID = strings.TrimSpace(d.ID)
		d.Title = strings.TrimSpace(d.Title)
		d.Source = strings.ToLower(strings.TrimSpace(d.Source))
		if d.Source == "" {
			d.Source = source
		}
		if d.Source != source {
			return fmt.Errorf("external document %q has source %q, expected %q", d.ID, d.Source, source)
		}
		if d.ID == "" || d.Title == "" || !safeID(d.ID) {
			return fmt.Errorf("invalid external knowledge document id/title")
		}
		if _, dup := seen[d.ID]; dup {
			return fmt.Errorf("duplicate external knowledge id %q", d.ID)
		}
		seen[d.ID] = struct{}{}
		h := hashDoc(*d, s.scoring)
		bodyChunks := chunkText(d.Text, s.scoring.ChunkWords, s.scoring.ChunkOverlap, s.scoring.MaxChunksPerDoc)
		same := false
		if old, ok := oldDocs[d.ID]; ok && hashDoc(old, s.scoring) == h && len(oldTitle[d.ID]) > 0 && len(oldChunks[d.ID]) == len(bodyChunks) {
			same = true
		} else if cached.Hashes[d.ID] == h && len(cached.TitleVectors[d.ID]) > 0 && len(cached.ChunkVectors[d.ID]) == len(bodyChunks) {
			oldTitle[d.ID] = append([]float64(nil), cached.TitleVectors[d.ID]...)
			oldChunks[d.ID] = cloneChunkVectors(cached.ChunkVectors[d.ID])
			same = true
		}
		if !same {
			changed = append(changed, *d)
		}
	}

	newEmbedded := map[string]embeddedDoc{}
	if s.rag && len(changed) > 0 {
		if s.embedder == nil {
			return fmt.Errorf("RAG is enabled but no embedding provider is configured")
		}
		var err error
		newEmbedded, err = s.embedDocuments(ctx, changed)
		if err != nil {
			return err
		}
		if _, err := s.syncSemanticDocuments(ctx, changed, newEmbedded); err != nil {
			return err
		}
	}
	for id, src := range oldExternal {
		if src == source {
			if _, stillPresent := seen[id]; !stillPresent {
				if err := s.deleteSemanticDocument(ctx, id); err != nil {
					return err
				}
			}
		}
	}

	s.mu.Lock()
	for _, d := range docs {
		if src := s.external[d.ID]; src == "" {
			if _, exists := oldDocs[d.ID]; exists {
				s.mu.Unlock()
				return fmt.Errorf("external knowledge id %q collides with local knowledge", d.ID)
			}
		} else if src != source {
			s.mu.Unlock()
			return fmt.Errorf("external knowledge id %q belongs to source %q", d.ID, src)
		}
	}
	rebuilt := make([]model.KnowledgeDoc, 0, len(s.docs)+len(docs))
	for _, d := range s.docs {
		if s.external[d.ID] != source {
			rebuilt = append(rebuilt, d)
		}
	}
	for id, src := range s.external {
		if src == source {
			delete(s.external, id)
			delete(s.titleVectors, id)
			delete(s.chunkVectors, id)
			delete(s.chunks, id)
		}
	}
	for _, d := range docs {
		rebuilt = append(rebuilt, d)
		s.external[d.ID] = source
		s.chunks[d.ID] = chunkText(d.Text, s.scoring.ChunkWords, s.scoring.ChunkOverlap, s.scoring.MaxChunksPerDoc)
		if e, ok := newEmbedded[d.ID]; ok {
			s.titleVectors[d.ID] = e.title
			s.chunkVectors[d.ID] = e.chunks
		} else {
			s.titleVectors[d.ID] = oldTitle[d.ID]
			s.chunkVectors[d.ID] = oldChunks[d.ID]
		}
	}
	s.docs = rebuilt
	s.mu.Unlock()
	return s.persistExternalVectorCache(source)
}

func (s *Store) persistVectorCache() error {
	return s.persistSnapshot()
}

// FindMetadata searches knowledge metadata without copying the complete corpus.
// It is intended for interactive UI lookups on very large knowledge stores.
func (s *Store) FindMetadata(query string, limit int) []model.KnowledgeDoc {
	if s == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = 30
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.KnowledgeDoc, 0, limit)
	for _, d := range s.docs {
		hay := strings.ToLower(d.ID + " " + d.Title + " " + strings.Join(d.Keywords, " ") + " " + strings.Join(d.ExternalCategories, " "))
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		out = append(out, d)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Store) ManagedDir() string {
	if s == nil {
		return ""
	}
	return s.managedDir
}
func safeID(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return !strings.Contains(v, "..")
}

// Search calculates a transparent hybrid relevance score. Embedding similarity
// is only one component; titles, explicit keywords and category/learning hints
// are scored separately. Missing metadata does not lower a document's score:
// the weights of available components are normalized dynamically.
func (s *Store) Search(ctx context.Context, text string, topK int, categorySets ...[]model.Category) ([]model.KnowledgeHit, error) {
	startedAt := time.Now()
	if s == nil {
		return nil, fmt.Errorf("knowledge store is not initialized")
	}
	s.mu.RLock()
	scoreCfg := s.scoring
	ragEnabled := s.rag
	embedder := s.embedder
	s.mu.RUnlock()
	var cats []model.Category
	if len(categorySets) > 0 {
		cats = categorySets[0]
	}

	queryTitle, queryBody := splitQueryText(text)
	queryChunks := chunkText(queryBody, scoreCfg.ChunkWords, scoreCfg.ChunkOverlap, scoreCfg.MaxQueryChunks)
	if len(queryChunks) == 0 {
		queryChunks = chunkText(text, scoreCfg.ChunkWords, scoreCfg.ChunkOverlap, scoreCfg.MaxQueryChunks)
	}
	var queryVectors [][]float64
	var queryTitleVector []float64
	if ragEnabled && embedder != nil {
		if len(queryChunks) > 0 {
			q, err := s.embedTexts(ctx, formatQueryEmbeddings(queryChunks, scoreCfg.EmbeddingProfile), scoreCfg.EmbedBatchSize)
			if err != nil {
				return nil, err
			}
			queryVectors = q
		}
		if strings.TrimSpace(queryTitle) != "" {
			tq, err := embedder.Embed(ctx, formatQueryEmbeddings([]string{queryTitle}, scoreCfg.EmbeddingProfile))
			if err != nil {
				return nil, err
			}
			if len(tq) > 0 {
				queryTitleVector = tq[0]
			}
		}
	}

	type remoteSemanticMatch struct {
		score      float64
		chunk      string
		queryChunk string
	}
	remoteSemantic := map[string]remoteSemanticMatch{}
	remoteAttempted, remoteFailed := false, false
	backend, backendMode, backendSearchK, backendFailOpen := s.semanticSettings()
	if ragEnabled && backend != nil && backendMode == "neuroforge" && len(queryVectors) > 0 {
		remoteAttempted = true
		for qi, qv := range queryVectors {
			hits, err := backend.Search(ctx, qv, backendSearchK)
			if err != nil {
				remoteFailed = true
				if !backendFailOpen {
					return nil, fmt.Errorf("neuroforge semantic search: %w", err)
				}
				break
			}
			for _, h := range hits {
				cur, ok := remoteSemantic[h.DocumentID]
				if !ok || h.Similarity > cur.score {
					qc := ""
					if qi < len(queryChunks) {
						qc = queryChunks[qi]
					}
					remoteSemantic[h.DocumentID] = remoteSemanticMatch{score: clamp01(h.Similarity), chunk: h.Text, queryChunk: qc}
				}
			}
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.docs) == 0 {
		return nil, nil
	}
	hits := make([]model.KnowledgeHit, 0, len(s.docs))
	for _, d := range s.docs {
		semantic, bestChunk, bestQueryChunk := 0.0, "", ""
		semanticAvailable := false
		if remoteAttempted && !remoteFailed {
			if rm, ok := remoteSemantic[d.ID]; ok {
				semanticAvailable = true
				semantic = rm.score
				bestChunk = rm.chunk
				bestQueryChunk = rm.queryChunk
			}
		}
		if !semanticAvailable && len(queryVectors) > 0 && len(s.chunkVectors[d.ID]) > 0 {
			semanticAvailable = true
			for qi, qv := range queryVectors {
				for di, dv := range s.chunkVectors[d.ID] {
					score := clamp01(cosine(qv, dv))
					if score > semantic || bestChunk == "" {
						semantic = score
						if di < len(s.chunks[d.ID]) {
							bestChunk = s.chunks[d.ID][di]
						}
						if qi < len(queryChunks) {
							bestQueryChunk = queryChunks[qi]
						}
					}
				}
			}
		} else if !semanticAvailable && (!remoteAttempted || remoteFailed) && strings.TrimSpace(d.Text) != "" {
			semanticAvailable = true
			docChunks := s.chunks[d.ID]
			if len(docChunks) == 0 {
				docChunks = []string{d.Text}
			}
			for _, qc := range queryChunks {
				for _, dc := range docChunks {
					score := tokenF1(qc, dc)
					if score > semantic || bestChunk == "" {
						semantic, bestChunk, bestQueryChunk = score, dc, qc
					}
				}
			}
		}

		title := 0.0
		titleAvailable := strings.TrimSpace(d.Title) != ""
		if titleAvailable {
			titleQuery := queryTitle
			if strings.TrimSpace(titleQuery) == "" {
				titleQuery = text
			}
			title = titleSimilarity(titleQuery, d.Title)
			if len(queryTitleVector) > 0 && len(s.titleVectors[d.ID]) > 0 {
				title = math.Max(title, clamp01(cosine(queryTitleVector, s.titleVectors[d.ID])))
			}
		}
		lexicalScore := lexicalSimilarity(text, d)
		keyword, keywordAvailable := keywordSimilarity(text, d.Keywords)
		category, categoryAvailable := categorySimilarity(text, d.Categories, cats)
		// Keywords and category profiles are positive evidence signals. Metadata that
		// exists but has no lexical overlap must not drag an otherwise strong
		// semantic/title match toward zero.
		keywordAvailable = keywordAvailable && keyword > 0
		categoryAvailable = categoryAvailable && category > 0
		total := weightedScore(scoreCfg,
			scorePart{semantic, scoreCfg.SemanticWeight, semanticAvailable},
			scorePart{title, scoreCfg.TitleWeight, titleAvailable},
			scorePart{lexicalScore, scoreCfg.LexicalWeight, lexicalScore > 0},
			scorePart{keyword, scoreCfg.KeywordWeight, keywordAvailable},
			scorePart{category, scoreCfg.CategoryWeight, categoryAvailable},
		)
		hits = append(hits, model.KnowledgeHit{Doc: d, Score: total, SemanticScore: semantic, TitleScore: title, LexicalScore: lexicalScore, KeywordScore: keyword, CategoryScore: category, BestChunkExcerpt: excerpt(bestChunk, 280), BestQueryExcerpt: excerpt(bestQueryChunk, 280), QueryChunkCount: len(queryChunks), DocumentChunkCount: len(s.chunks[d.ID])})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].TitleScore > hits[j].TitleScore
		}
		return hits[i].Score > hits[j].Score
	})
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	activityHits := make([]brainactivity.Hit, 0, len(hits))
	for _, hit := range hits {
		activityHits = append(activityHits, brainactivity.Hit{ID: hit.Doc.ID, Score: hit.Score})
	}
	brainactivity.EmitSearch("agent", text, activityHits, time.Since(startedAt))
	return hits, nil
}

// FilterHitsBySources keeps the existing relevance order while applying a
// purpose-specific source allowlist. This lets one shared index serve normal
// retrieval and category-only knowledge without exposing category-only entries
// as reply candidates.
func FilterHitsBySources(hits []model.KnowledgeHit, sources []string, topK int) []model.KnowledgeHit {
	allowed := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		normalized := strings.ToLower(strings.TrimSpace(source))
		if normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	if len(allowed) == 0 || len(hits) == 0 {
		return nil
	}
	out := make([]model.KnowledgeHit, 0, len(hits))
	for _, hit := range hits {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(hit.Doc.Source))]; !ok {
			continue
		}
		out = append(out, hit)
		if topK > 0 && len(out) >= topK {
			break
		}
	}
	return out
}

// RerankForCategory applies a deterministic post-classification boost when a
// knowledge document is explicitly mapped to the category selected by the
// classifier. This happens after the model decision, so the dashboard and
// policy can distinguish retrieval evidence from category alignment.
func (s *Store) RerankForCategory(hits []model.KnowledgeHit, categoryID int64) []model.KnowledgeHit {
	if s == nil || len(hits) == 0 || categoryID == 0 {
		return hits
	}
	s.mu.RLock()
	cfg := s.scoring
	s.mu.RUnlock()
	out := append([]model.KnowledgeHit(nil), hits...)
	for i := range out {
		match := false
		for _, id := range out[i].Doc.Categories {
			if id == categoryID {
				match = true
				break
			}
		}
		if match {
			out[i].CategoryScore = 1
		}
		out[i].Score = weightedScore(cfg,
			scorePart{out[i].SemanticScore, cfg.SemanticWeight, out[i].SemanticScore > 0},
			scorePart{out[i].TitleScore, cfg.TitleWeight, out[i].TitleScore > 0},
			scorePart{out[i].LexicalScore, cfg.LexicalWeight, out[i].LexicalScore > 0},
			scorePart{out[i].KeywordScore, cfg.KeywordWeight, out[i].KeywordScore > 0},
			scorePart{out[i].CategoryScore, cfg.CategoryWeight, out[i].CategoryScore > 0},
		)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].SemanticScore > out[j].SemanticScore
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func (s *Store) index(ctx context.Context) error {
	_ = os.MkdirAll(filepath.Dir(s.cachePath), 0o750)
	cf := loadCache(s.cachePath)
	var need []model.KnowledgeDoc
	cacheHits := 0
	cachedEmbedded := map[string]embeddedDoc{}
	for _, d := range s.docs {
		bodyChunks := chunkText(d.Text, s.scoring.ChunkWords, s.scoring.ChunkOverlap, s.scoring.MaxChunksPerDoc)
		s.mu.Lock()
		s.chunks[d.ID] = bodyChunks
		s.mu.Unlock()
		h := hashDoc(d, s.scoring)
		if cf.Hashes[d.ID] == h && len(cf.TitleVectors[d.ID]) > 0 && len(cf.ChunkVectors[d.ID]) == len(bodyChunks) {
			title := append([]float64(nil), cf.TitleVectors[d.ID]...)
			chunks := cloneChunkVectors(cf.ChunkVectors[d.ID])
			s.mu.Lock()
			s.titleVectors[d.ID] = title
			s.chunkVectors[d.ID] = chunks
			s.mu.Unlock()
			cachedEmbedded[d.ID] = embeddedDoc{title: title, chunks: chunks}
			cacheHits++
		} else {
			need = append(need, d)
		}
	}
	s.setInitStatus(func(st *InitStatus) {
		st.CacheHits = cacheHits
		st.IndexedDocs = cacheHits
		st.PendingEmbeddings = len(need)
	})
	slog.Info("knowledge index prepared", "documents", len(s.docs), "cache_hits", cacheHits, "documents_to_embed", len(need))

	semanticReady := true
	if len(cachedEmbedded) > 0 {
		if ok, err := s.syncSemanticDocuments(ctx, s.docs, cachedEmbedded); err != nil {
			return err
		} else if !ok {
			semanticReady = false
		}
	}

	// Batch by documents, not by the whole corpus. A corpus with tens of
	// thousands of files can otherwise allocate hundreds of thousands of
	// embedding input strings before the first request is sent to Ollama.
	const docsPerBatch = 20
	embeddedDocs := 0
	for start := 0; start < len(need); start += docsPerBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + docsPerBatch
		if end > len(need) {
			end = len(need)
		}
		batch := need[start:end]
		embedded, err := s.embedDocuments(ctx, batch)
		if err != nil {
			return err
		}
		if ok, err := s.syncSemanticDocuments(ctx, batch, embedded); err != nil {
			return err
		} else if !ok {
			semanticReady = false
		}
		s.mu.Lock()
		for _, d := range batch {
			s.titleVectors[d.ID] = embedded[d.ID].title
			s.chunkVectors[d.ID] = embedded[d.ID].chunks
		}
		s.mu.Unlock()
		embeddedDocs += len(batch)
		indexed := cacheHits + embeddedDocs
		pending := len(need) - embeddedDocs
		s.setInitStatus(func(st *InitStatus) {
			st.IndexedDocs = indexed
			st.PendingEmbeddings = pending
		})
		if embeddedDocs%500 == 0 || embeddedDocs == len(need) {
			slog.Info("knowledge embedding progress", "indexed_docs", indexed, "total_docs", len(s.docs), "cache_hits", cacheHits, "pending_embeddings", pending)
		}
	}
	if semanticReady {
		s.externalizeChunkVectors()
	}
	return s.persistVectorCache()
}

type embeddedDoc struct {
	title  []float64
	chunks [][]float64
}

func (s *Store) embedDocuments(ctx context.Context, docs []model.KnowledgeDoc) (map[string]embeddedDoc, error) {
	out := make(map[string]embeddedDoc, len(docs))
	type ref struct {
		id    string
		title bool
		chunk int
	}
	var texts []string
	var refs []ref
	for _, d := range docs {
		texts = append(texts, formatDocumentEmbedding(d.Title, d.Title, s.scoring.EmbeddingProfile))
		refs = append(refs, ref{id: d.ID, title: true})
		parts := chunkText(d.Text, s.scoring.ChunkWords, s.scoring.ChunkOverlap, s.scoring.MaxChunksPerDoc)
		for i, part := range parts {
			texts = append(texts, formatDocumentEmbedding(d.Title, part, s.scoring.EmbeddingProfile))
			refs = append(refs, ref{id: d.ID, chunk: i})
		}
	}
	vectors, err := s.embedTexts(ctx, texts, s.scoring.EmbedBatchSize)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(refs) {
		return nil, fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(vectors), len(refs))
	}
	for i, r := range refs {
		if len(vectors[i]) == 0 {
			return nil, fmt.Errorf("embedding provider returned empty vector for %s", r.id)
		}
		e := out[r.id]
		if r.title {
			e.title = vectors[i]
		} else {
			for len(e.chunks) <= r.chunk {
				e.chunks = append(e.chunks, nil)
			}
			e.chunks[r.chunk] = vectors[i]
		}
		out[r.id] = e
	}
	return out, nil
}

func (s *Store) embedTexts(ctx context.Context, texts []string, batch int) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if batch <= 0 {
		batch = 64
	}
	out := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += batch {
		end := start + batch
		if end > len(texts) {
			end = len(texts)
		}
		vv, err := s.embedder.Embed(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		if len(vv) != end-start {
			return nil, fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(vv), end-start)
		}
		out = append(out, vv...)
	}
	return out, nil
}

func loadCache(path string) cacheFile {
	cf := cacheFile{Version: 3, Hashes: map[string]string{}, TitleVectors: map[string][]float64{}, ChunkVectors: map[string][][]float64{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cf)
		if cf.Version != 3 {
			cf = cacheFile{Version: 3, Hashes: map[string]string{}, TitleVectors: map[string][]float64{}, ChunkVectors: map[string][][]float64{}}
		}
	}
	if cf.Hashes == nil {
		cf.Hashes = map[string]string{}
	}
	if cf.TitleVectors == nil {
		cf.TitleVectors = map[string][]float64{}
	}
	if cf.ChunkVectors == nil {
		cf.ChunkVectors = map[string][][]float64{}
	}
	return cf
}

func formatQueryEmbeddings(texts []string, profile string) []string {
	out := make([]string, len(texts))
	for i, text := range texts {
		if profile == "embeddinggemma" {
			out[i] = "task: search result | query: " + strings.TrimSpace(text)
		} else {
			out[i] = text
		}
	}
	return out
}

func formatDocumentEmbedding(title, text, profile string) string {
	if profile == "embeddinggemma" {
		t := strings.TrimSpace(title)
		if t == "" {
			t = "none"
		}
		return "title: " + t + " | text: " + strings.TrimSpace(text)
	}
	return text
}

func splitQueryText(text string) (title, body string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		title = strings.TrimSpace(text[:i])
		body = strings.TrimSpace(text[i+1:])
		return title, body
	}
	return text, text
}

func chunkText(text string, words, overlap, maxChunks int) []string {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 {
		return nil
	}
	if words <= 0 {
		words = 160
	}
	if overlap < 0 || overlap >= words {
		overlap = 0
	}
	if maxChunks <= 0 {
		maxChunks = 24
	}
	step := words - overlap
	out := make([]string, 0, minInt(maxChunks, (len(parts)+step-1)/step))
	for start := 0; start < len(parts) && len(out) < maxChunks; start += step {
		end := start + words
		if end > len(parts) {
			end = len(parts)
		}
		out = append(out, strings.Join(parts[start:end], " "))
		if end == len(parts) {
			break
		}
	}
	return out
}

type scorePart struct {
	value, weight float64
	available     bool
}

func weightedScore(_ ScoringConfig, parts ...scorePart) float64 {
	var sum, weights float64
	for _, p := range parts {
		if !p.available || p.weight <= 0 {
			continue
		}
		sum += clamp01(p.value) * p.weight
		weights += p.weight
	}
	if weights == 0 {
		return 0
	}
	return clamp01(sum / weights)
}

func titleSimilarity(query, title string) float64 {
	q := strings.TrimSpace(query)
	t := strings.TrimSpace(title)
	if q == "" || t == "" {
		return 0
	}
	qn := normalizeText(q)
	tn := normalizeText(t)
	if qn == tn || strings.Contains(tn, qn) || strings.Contains(qn, tn) {
		return 1
	}
	// Title relevance is intentionally asymmetric: if the short ticket subject
	// is fully represented by one of several concepts in a longer KB title, that
	// is a strong title match rather than a low symmetric F1 score.
	return math.Max(tokenCoverage(q, t), tokenF1(q, t))
}

func lexicalSimilarity(query string, d model.KnowledgeDoc) float64 {
	best := math.Max(tokenF1(query, d.Title+" "+d.Text), tokenCoverage(query, d.Title+" "+d.Text))
	if t, _ := splitQueryText(query); strings.TrimSpace(t) != "" {
		best = math.Max(best, tokenCoverage(t, d.Title))
	}
	for _, kw := range d.Keywords {
		best = math.Max(best, phraseCoverage(kw, query))
	}
	return clamp01(best)
}

func keywordSimilarity(query string, keywords []string) (float64, bool) {
	if len(keywords) == 0 {
		return 0, false
	}
	best := 0.0
	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}
		best = math.Max(best, phraseCoverage(kw, query))
	}
	return clamp01(best), true
}

func categorySimilarity(query string, ids []int64, categories []model.Category) (float64, bool) {
	if len(ids) == 0 || len(categories) == 0 {
		return 0, false
	}
	wanted := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	best, found := 0.0, false
	for _, c := range categories {
		if _, ok := wanted[c.ID]; !ok {
			continue
		}
		found = true
		for _, part := range append([]string{c.Name, c.CompleteName}, append(c.Hints, c.Examples...)...) {
			if strings.TrimSpace(part) == "" {
				continue
			}
			best = math.Max(best, phraseCoverage(part, query))
		}
	}
	return clamp01(best), found
}

// phraseCoverage asks "how much of this concept phrase occurs in the query?".
// It is better suited to support terminology than symmetric F1 because a long
// user ticket may contain lots of harmless extra words.
func phraseCoverage(phrase, query string) float64 {
	pn := normalizeText(phrase)
	qn := normalizeText(query)
	if pn == "" || qn == "" {
		return 0
	}
	if strings.Contains(qn, pn) {
		return 1
	}
	return tokenCoverage(phrase, query)
}

func tokenCoverage(needle, haystack string) float64 {
	a := tokenList(needle)
	b := tokenList(haystack)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var sum float64
	for _, x := range a {
		best := 0.0
		for _, y := range b {
			best = math.Max(best, tokenSimilarity(x, y))
		}
		sum += best
	}
	return clamp01(sum / float64(len(a)))
}

func tokenSimilarity(a, b string) float64 {
	if a == b {
		return 1
	}
	if len(a) < 4 || len(b) < 4 {
		return 0
	}
	// Helpdesk-German contains many compounds and inflections (anmelden,
	// Anmeldung, Benutzeranmeldung, Nutzerkonto, Benutzerkonto). Exact-token
	// overlap is therefore too brittle. First reward strong substring matches,
	// then compare a deliberately small set of German support stems.
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	if len(short) >= 5 && strings.Contains(long, short) {
		ratio := float64(len(short)) / float64(len(long))
		return clamp01(.75 + .25*ratio)
	}
	sa, sb := supportStem(a), supportStem(b)
	if sa == sb && len(sa) >= 5 {
		return .95
	}
	stemShort, stemLong := sa, sb
	if len(stemShort) > len(stemLong) {
		stemShort, stemLong = stemLong, stemShort
	}
	if len(stemShort) >= 5 && strings.Contains(stemLong, stemShort) {
		return .90
	}
	common := 0
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for common < limit && a[common] == b[common] {
		common++
	}
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if common >= 5 && float64(common)/float64(minLen) >= .70 {
		return clamp01(float64(common) / float64(maxLen))
	}
	return 0
}

func supportStem(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Long, semantically common German suffixes first. This is intentionally
	// conservative and is not meant to be a full linguistic stemmer.
	for _, suffix := range []string{"ungen", "ern", "ung", "ieren", "ischen", "ische", "isch", "enden", "ende", "en", "er", "es", "e", "n", "s"} {
		if strings.HasSuffix(s, suffix) && len(s)-len(suffix) >= 5 {
			s = strings.TrimSuffix(s, suffix)
			break
		}
	}
	return s
}

func normalizeText(s string) string {
	return strings.Join(tokenList(s), " ")
}

func tokenList(s string) []string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len([]rune(p)) < 3 || isStopword(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func isStopword(s string) bool {
	switch s {
	case "der", "die", "das", "den", "dem", "des", "ein", "eine", "einer", "einem", "einen", "und", "oder", "aber", "mit", "ohne", "für", "fuer", "von", "vom", "zum", "zur", "ist", "sind", "war", "wird", "werden", "ich", "wir", "sie", "seit", "heute", "gestern", "bitte", "hilfe", "vielen", "dank", "nicht", "mehr", "kann", "mich", "mir", "mein", "meine", "meinen", "meinem":
		return true
	default:
		return false
	}
}

func tokenF1(a, b string) float64 {
	aTok, bTok := tokens(a), tokens(b)
	if len(aTok) == 0 || len(bTok) == 0 {
		return 0
	}
	common := 0
	for t := range aTok {
		if _, ok := bTok[t]; ok {
			common++
		}
	}
	if common == 0 {
		return 0
	}
	precision := float64(common) / float64(len(aTok))
	recall := float64(common) / float64(len(bTok))
	return 2 * precision * recall / (precision + recall)
}

func excerpt(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func cloneVectorMap(in map[string][]float64) map[string][]float64 {
	out := make(map[string][]float64, len(in))
	for k, v := range in {
		out[k] = append([]float64(nil), v...)
	}
	return out
}
func cloneChunkVectorMap(in map[string][][]float64) map[string][][]float64 {
	out := make(map[string][][]float64, len(in))
	for k, v := range in {
		out[k] = cloneChunkVectors(v)
	}
	return out
}
func cloneChunkVectors(in [][]float64) [][]float64 {
	out := make([][]float64, len(in))
	for i, v := range in {
		out[i] = append([]float64(nil), v...)
	}
	return out
}
func cloneStringSliceMap(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func hashDoc(d model.KnowledgeDoc, cfg ScoringConfig) string {
	// Only fields that change the actual embedding input belong in this fingerprint.
	// Category mappings affect deterministic ranking/policy, not the text sent to
	// the embedding model, so remapping categories must not force re-embedding.
	// Formatting-only changes to AnswerHTML must not force re-embedding.
	b, _ := json.Marshal(struct {
		ID                string   `json:"id"`
		Title             string   `json:"title"`
		Text              string   `json:"text"`
		Keywords          []string `json:"keywords"`
		Profile           string   `json:"profile"`
		EmbeddingIdentity string   `json:"embedding_identity"`
		ChunkWords        int      `json:"chunk_words"`
		ChunkOverlap      int      `json:"chunk_overlap"`
		MaxChunks         int      `json:"max_chunks"`
	}{d.ID, d.Title, d.Text, d.Keywords, cfg.EmbeddingProfile, cfg.EmbeddingIdentity, cfg.ChunkWords, cfg.ChunkOverlap, cfg.MaxChunksPerDoc})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		dot += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb))
}
func lexical(text string, d model.KnowledgeDoc) float64 {
	q := tokens(text)
	hay := tokens(d.Title + " " + d.Text + " " + strings.Join(d.Keywords, " ") + " " + strings.Join(d.ExternalCategories, " "))
	if len(q) == 0 {
		return 0
	}
	hits := 0
	for t := range q {
		if _, ok := hay[t]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(q))
}
func tokens(s string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, p := range tokenList(s) {
		m[p] = struct{}{}
	}
	return m
}
