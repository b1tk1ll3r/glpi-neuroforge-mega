package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

// SemanticBackend externalizes chunk-vector persistence/search while the
// existing deterministic GLPI hybrid scorer remains authoritative.
type SemanticBackend interface {
	Name() string
	UpsertDocument(context.Context, model.KnowledgeDoc, []string, [][]float64) error
	DeleteDocument(context.Context, string) error
	Search(context.Context, []float64, int) ([]SemanticHit, error)
	Health(context.Context) error
}

type SemanticHit struct {
	DocumentID string
	ChunkIndex int
	Text       string
	Similarity float64
	Source     string
}

type NeuroForgeBackendConfig struct {
	BaseURL   string
	APIKey    string
	Namespace string
	Timeout   time.Duration
}

type NeuroForgeBackend struct {
	baseURL   string
	apiKey    string
	namespace string
	http      *http.Client
}

func NewNeuroForgeBackend(cfg NeuroForgeBackendConfig) (*NeuroForgeBackend, error) {
	raw := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if raw == "" {
		return nil, errors.New("neuroforge base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid neuroforge URL %q", raw)
	}
	ns := strings.TrimSpace(cfg.Namespace)
	if ns == "" {
		ns = "glpi-agent"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &NeuroForgeBackend{baseURL: raw, apiKey: strings.TrimSpace(cfg.APIKey), namespace: ns, http: &http.Client{Timeout: cfg.Timeout}}, nil
}

func (c *NeuroForgeBackend) Name() string { return "neuroforge" }

func vector32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

func contentHash(text string, vec []float64) string {
	h := sha256.New()
	_, _ = h.Write([]byte(text))
	_, _ = h.Write([]byte{0})
	for _, v := range vec {
		_, _ = h.Write([]byte(strconv.FormatFloat(v, 'g', 9, 64)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *NeuroForgeBackend) UpsertDocument(ctx context.Context, d model.KnowledgeDoc, chunks []string, vectors [][]float64) error {
	if len(chunks) != len(vectors) {
		return fmt.Errorf("chunk/vector mismatch for %s: %d != %d", d.ID, len(chunks), len(vectors))
	}
	type chunkReq struct {
		Index       int       `json:"index"`
		Text        string    `json:"text"`
		Vector      []float32 `json:"vector"`
		ContentHash string    `json:"content_hash"`
	}
	req := struct {
		Namespace  string     `json:"namespace"`
		DocumentID string     `json:"document_id"`
		Title      string     `json:"title,omitempty"`
		SourceURI  string     `json:"source_uri,omitempty"`
		Tags       []string   `json:"tags,omitempty"`
		Confidence float64    `json:"confidence"`
		Chunks     []chunkReq `json:"chunks"`
	}{Namespace: c.namespace, DocumentID: d.ID, Title: d.Title, SourceURI: d.SourceURI, Confidence: 1}
	req.Tags = append(req.Tags, "source:"+d.Source)
	for _, cat := range d.Categories {
		req.Tags = append(req.Tags, "category:"+strconv.FormatInt(cat, 10))
	}
	req.Chunks = make([]chunkReq, 0, len(chunks))
	for i := range chunks {
		req.Chunks = append(req.Chunks, chunkReq{Index: i, Text: chunks[i], Vector: vector32(vectors[i]), ContentHash: contentHash(chunks[i], vectors[i])})
	}
	var out map[string]any
	return c.doJSON(ctx, http.MethodPost, "/api/v1/integrations/knowledge/upsert", req, &out)
}

func (c *NeuroForgeBackend) DeleteDocument(ctx context.Context, id string) error {
	path := "/api/v1/integrations/knowledge/" + url.PathEscape(c.namespace) + "/" + url.PathEscape(strings.TrimSpace(id))
	var out map[string]any
	return c.doJSON(ctx, http.MethodDelete, path, nil, &out)
}

func (c *NeuroForgeBackend) Search(ctx context.Context, vector []float64, k int) ([]SemanticHit, error) {
	if k <= 0 {
		k = 128
	}
	req := struct {
		Namespace string    `json:"namespace"`
		Vector    []float32 `json:"vector"`
		K         int       `json:"k"`
	}{Namespace: c.namespace, Vector: vector32(vector), K: k}
	var raw []struct {
		Memory struct {
			ID         string `json:"id"`
			Text       string `json:"text"`
			Provenance struct {
				Source         string `json:"source"`
				SourceMemoryID string `json:"source_memory_id"`
				ChunkIndex     int    `json:"chunk_index"`
			} `json:"provenance"`
		} `json:"memory"`
		Similarity float64 `json:"similarity"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/integrations/knowledge/search", req, &raw); err != nil {
		return nil, err
	}
	out := make([]SemanticHit, 0, len(raw))
	for _, h := range raw {
		if h.Memory.Provenance.SourceMemoryID == "" {
			continue
		}
		out = append(out, SemanticHit{DocumentID: h.Memory.Provenance.SourceMemoryID, ChunkIndex: h.Memory.Provenance.ChunkIndex, Text: h.Memory.Text, Similarity: h.Similarity, Source: h.Memory.Provenance.Source})
	}
	return out, nil
}

func (c *NeuroForgeBackend) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/readyz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("neuroforge readiness failed: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func (c *NeuroForgeBackend) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("neuroforge %s %s failed: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(output)
}

// SetSemanticBackend configures the migration mode. local keeps the original
// snapshot-only behavior; dual mirrors vectors to the backend but preserves the
// local cache; neuroforge makes NeuroForge authoritative for chunk-vector
// persistence/search while keeping lexical/title fallback locally.
func (s *Store) SetSemanticBackend(backend SemanticBackend, mode string, searchK int, failOpen bool) error {
	if s == nil {
		return errors.New("knowledge store is nil")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "local"
	}
	switch mode {
	case "local":
		backend = nil
	case "dual", "neuroforge":
		if backend == nil {
			return fmt.Errorf("semantic backend %q requires a configured backend", mode)
		}
	default:
		return fmt.Errorf("semantic backend mode must be local, dual or neuroforge")
	}
	if searchK <= 0 {
		searchK = 128
	}
	s.mu.Lock()
	s.semanticBackend = backend
	s.semanticBackendMode = mode
	s.semanticBackendSearchK = searchK
	s.semanticBackendFailOpen = failOpen
	s.mu.Unlock()
	return nil
}

func (s *Store) semanticExternalized() bool {
	return s != nil && s.semanticBackend != nil && s.semanticBackendMode == "neuroforge"
}

func (s *Store) syncSemanticDocument(ctx context.Context, d model.KnowledgeDoc, chunks []string, vectors [][]float64) error {
	s.mu.RLock()
	backend := s.semanticBackend
	mode := s.semanticBackendMode
	s.mu.RUnlock()
	if backend == nil || mode == "local" || len(vectors) == 0 {
		return nil
	}
	if err := backend.UpsertDocument(ctx, d, chunks, vectors); err != nil {
		return fmt.Errorf("semantic backend sync %s: %w", d.ID, err)
	}
	return nil
}

func (s *Store) semanticSettings() (SemanticBackend, string, int, bool) {
	if s == nil {
		return nil, "local", 0, true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.semanticBackend, s.semanticBackendMode, s.semanticBackendSearchK, s.semanticBackendFailOpen
}

func (s *Store) handleSemanticSyncError(err error) error {
	if err == nil {
		return nil
	}
	_, mode, _, failOpen := s.semanticSettings()
	if mode == "dual" || failOpen {
		return nil
	}
	return err
}

func (s *Store) externalizeChunkVectors() {
	if !s.semanticExternalized() {
		return
	}
	s.mu.Lock()
	s.chunkVectors = map[string][][]float64{}
	s.mu.Unlock()
}

func (s *Store) syncSemanticDocuments(ctx context.Context, docs []model.KnowledgeDoc, embedded map[string]embeddedDoc) (bool, error) {
	backend, mode, _, failOpen := s.semanticSettings()
	if backend == nil || mode == "local" {
		return true, nil
	}
	allOK := true
	for _, d := range docs {
		e, ok := embedded[d.ID]
		if !ok || len(e.chunks) == 0 {
			continue
		}
		chunks := chunkText(d.Text, s.scoring.ChunkWords, s.scoring.ChunkOverlap, s.scoring.MaxChunksPerDoc)
		if err := s.syncSemanticDocument(ctx, d, chunks, e.chunks); err != nil {
			allOK = false
			slog.Warn("semantic backend sync failed; local vectors retained", "backend", backend.Name(), "document", d.ID, "mode", mode, "error", err)
			if mode == "neuroforge" && !failOpen {
				return false, err
			}
			// dual mode and fail-open neuroforge mode preserve the local vectors.
			continue
		}
	}
	return allOK, nil
}

func (s *Store) syncLoadedSemanticBackend(ctx context.Context) (bool, error) {
	backend, mode, _, failOpen := s.semanticSettings()
	if backend == nil || mode == "local" {
		return true, nil
	}
	s.mu.RLock()
	docs := append([]model.KnowledgeDoc(nil), s.docs...)
	embedded := make(map[string]embeddedDoc, len(s.chunkVectors))
	for _, d := range docs {
		if vv := s.chunkVectors[d.ID]; len(vv) > 0 {
			embedded[d.ID] = embeddedDoc{title: append([]float64(nil), s.titleVectors[d.ID]...), chunks: cloneChunkVectors(vv)}
		}
	}
	s.mu.RUnlock()
	if len(embedded) == 0 {
		if err := backend.Health(ctx); err != nil {
			if failOpen {
				return false, nil
			}
			return false, fmt.Errorf("semantic backend readiness: %w", err)
		}
		return true, nil
	}
	ok, err := s.syncSemanticDocuments(ctx, docs, embedded)
	if err != nil {
		return false, err
	}
	if ok && mode == "neuroforge" {
		s.externalizeChunkVectors()
		if err := s.persistSnapshot(); err != nil {
			return false, err
		}
	}
	return ok, nil
}

func (s *Store) deleteSemanticDocument(ctx context.Context, id string) error {
	backend, mode, _, failOpen := s.semanticSettings()
	if backend == nil || mode == "local" {
		return nil
	}
	if err := backend.DeleteDocument(ctx, id); err != nil {
		if mode == "dual" || failOpen {
			slog.Warn("semantic backend delete failed; continuing by policy", "backend", backend.Name(), "document", id, "mode", mode, "error", err)
			return nil
		}
		return fmt.Errorf("semantic backend delete %s: %w", id, err)
	}
	return nil
}
