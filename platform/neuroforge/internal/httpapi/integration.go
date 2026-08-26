package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"neuroforge/internal/core"
)

type integrationKnowledgeChunk struct {
	Index       int       `json:"index"`
	Text        string    `json:"text"`
	Vector      []float32 `json:"vector"`
	ContentHash string    `json:"content_hash,omitempty"`
}

type integrationKnowledgeUpsert struct {
	Namespace  string                      `json:"namespace"`
	DocumentID string                      `json:"document_id"`
	Title      string                      `json:"title,omitempty"`
	SourceURI  string                      `json:"source_uri,omitempty"`
	Tags       []string                    `json:"tags,omitempty"`
	Confidence float64                     `json:"confidence,omitempty"`
	Chunks     []integrationKnowledgeChunk `json:"chunks"`
}

type integrationKnowledgeSearch struct {
	Namespace     string    `json:"namespace"`
	Vector        []float32 `json:"vector"`
	K             int       `json:"k"`
	MinSimilarity *float64  `json:"min_similarity,omitempty"`
}

type integrationEventRequest struct {
	Type    string `json:"type"`
	Source  string `json:"source"`
	Message string `json:"message,omitempty"`
	Query   string `json:"query,omitempty"`
	Hits    []struct {
		ID    string  `json:"id"`
		Score float64 `json:"score,omitempty"`
	} `json:"hits,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func integrationSource(namespace string) string {
	return "integration:" + strings.ToLower(strings.TrimSpace(namespace))
}

func integrationMemoryID(namespace, documentID string, chunk int) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(namespace)) + "\x00" + strings.TrimSpace(documentID) + fmt.Sprintf("\x00chunk\x00%d", chunk)))
	return "ik_" + hex.EncodeToString(sum[:16])
}

func validIntegrationName(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == ':') {
			return false
		}
	}
	return true
}

func (s *Server) integrationKnowledgeUpsert(w http.ResponseWriter, r *http.Request) {
	var q integrationKnowledgeUpsert
	if err := decode(r, &q); err != nil {
		s.err(w, http.StatusBadRequest, err)
		return
	}
	q.Namespace = strings.TrimSpace(q.Namespace)
	q.DocumentID = strings.TrimSpace(q.DocumentID)
	if !validIntegrationName(q.Namespace) || !validIntegrationName(q.DocumentID) {
		s.err(w, http.StatusBadRequest, errors.New("namespace/document_id contains unsupported characters"))
		return
	}
	if len(q.Chunks) == 0 || len(q.Chunks) > 512 {
		s.err(w, http.StatusBadRequest, errors.New("chunks must contain 1..512 entries"))
		return
	}
	source := integrationSource(q.Namespace)
	confidence := q.Confidence
	if confidence <= 0 {
		confidence = 1
	}
	if confidence > 1 {
		confidence = 1
	}

	// Snapshot once so document replacement is O(total memories + chunks), not
	// O(chunks * total memories), and batch deletes rebuild ANN indexes once.
	existing := make(map[string]core.Memory)
	for _, m := range s.store.MemoriesSnapshot() {
		if m.Provenance.Source == source && m.Provenance.SourceMemoryID == q.DocumentID && m.Kind == "knowledge.chunk" {
			existing[m.ID] = m
		}
	}

	desired := make(map[string]bool, len(q.Chunks))
	createItems := make([]core.Memory, 0, len(q.Chunks))
	deleteIDs := make([]string, 0, len(existing))
	created, updated, unchanged := 0, 0, 0
	seenIndexes := make(map[int]struct{}, len(q.Chunks))
	sort.Slice(q.Chunks, func(i, j int) bool { return q.Chunks[i].Index < q.Chunks[j].Index })
	for _, chunk := range q.Chunks {
		if chunk.Index < 0 || strings.TrimSpace(chunk.Text) == "" || len(chunk.Vector) == 0 {
			s.err(w, http.StatusBadRequest, errors.New("each chunk requires non-negative index, text and vector"))
			return
		}
		if _, duplicate := seenIndexes[chunk.Index]; duplicate {
			s.err(w, http.StatusBadRequest, fmt.Errorf("duplicate chunk index %d", chunk.Index))
			return
		}
		seenIndexes[chunk.Index] = struct{}{}
		id := integrationMemoryID(q.Namespace, q.DocumentID, chunk.Index)
		desired[id] = true
		current, exists := existing[id]
		if exists && current.Provenance.ContentHash == chunk.ContentHash && current.Provenance.SourceMemoryID == q.DocumentID && len(current.Vector) == len(chunk.Vector) {
			unchanged++
			continue
		}
		if exists {
			deleteIDs = append(deleteIDs, id)
			updated++
		} else {
			created++
		}
		tags := append([]string(nil), q.Tags...)
		tags = append(tags, "integration", "namespace:"+q.Namespace, "document:"+q.DocumentID, "record:chunk")
		createItems = append(createItems, core.Memory{
			ID: id, Kind: "knowledge.chunk", MemoryType: core.MemorySemantic,
			Text: chunk.Text, Vector: append([]float32(nil), chunk.Vector...), Tags: tags,
			Salience: 1, Confidence: confidence,
			Provenance: core.MemoryProvenance{
				Source: source, Actor: "knowledge-sync", SourceMemoryID: q.DocumentID,
				SourceURI: strings.TrimSpace(q.SourceURI), SourceTitle: strings.TrimSpace(q.Title),
				ChunkIndex: chunk.Index, ChunkCount: len(q.Chunks), ContentHash: chunk.ContentHash,
			},
		})
	}
	deleted := 0
	for id := range existing {
		if !desired[id] {
			deleteIDs = append(deleteIDs, id)
			deleted++
		}
	}
	if err := s.store.DeleteMemoriesBatch(deleteIDs); err != nil {
		s.err(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.store.AddMemoriesBatch(createItems); err != nil {
		s.err(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{
		Type: "integration.knowledge.synced", Summary: "External knowledge document synchronized", Actor: q.Namespace,
		Metadata: map[string]string{"namespace": q.Namespace, "document_id": q.DocumentID, "created": fmt.Sprint(created), "updated": fmt.Sprint(updated), "deleted": fmt.Sprint(deleted), "unchanged": fmt.Sprint(unchanged)},
	})
	s.json(w, http.StatusOK, map[string]any{"ok": true, "document_id": q.DocumentID, "created": created, "updated": updated, "deleted": deleted, "unchanged": unchanged})
}

func (s *Server) integrationKnowledgeDelete(w http.ResponseWriter, r *http.Request) {
	namespace := strings.TrimSpace(r.PathValue("namespace"))
	documentID := strings.TrimSpace(r.PathValue("document_id"))
	if !validIntegrationName(namespace) || !validIntegrationName(documentID) {
		s.err(w, http.StatusBadRequest, errors.New("invalid namespace or document id"))
		return
	}
	source := integrationSource(namespace)
	ids := make([]string, 0)
	for _, m := range s.store.MemoriesSnapshot() {
		if m.Provenance.Source == source && m.Provenance.SourceMemoryID == documentID && m.Kind == "knowledge.chunk" {
			ids = append(ids, m.ID)
		}
	}
	if err := s.store.DeleteMemoriesBatch(ids); err != nil {
		s.err(w, http.StatusInternalServerError, err)
		return
	}
	deleted := len(ids)
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "integration.knowledge.deleted", Summary: "External knowledge document removed", Actor: namespace, Metadata: map[string]string{"namespace": namespace, "document_id": documentID, "deleted": fmt.Sprint(deleted)}})
	s.json(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

func (s *Server) integrationKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	var q integrationKnowledgeSearch
	if err := decode(r, &q); err != nil {
		s.err(w, http.StatusBadRequest, err)
		return
	}
	q.Namespace = strings.TrimSpace(q.Namespace)
	if !validIntegrationName(q.Namespace) || len(q.Vector) == 0 {
		s.err(w, http.StatusBadRequest, errors.New("namespace and vector are required"))
		return
	}
	if q.K <= 0 {
		q.K = 128
	}
	if q.K > 500 {
		q.K = 500
	}
	min := -1.0
	if q.MinSimilarity != nil {
		min = *q.MinSimilarity
	}
	hits := s.store.SearchVectorByProvenanceSource(q.Vector, q.K, min, 0, integrationSource(q.Namespace))
	s.json(w, http.StatusOK, hits)
}

func (s *Server) integrationEvent(w http.ResponseWriter, r *http.Request) {
	var q integrationEventRequest
	if err := decode(r, &q); err != nil {
		s.err(w, http.StatusBadRequest, err)
		return
	}
	q.Type = strings.TrimSpace(q.Type)
	q.Source = strings.TrimSpace(q.Source)
	if q.Type == "" || q.Source == "" {
		s.err(w, http.StatusBadRequest, errors.New("type and source are required"))
		return
	}
	meta := map[string]string{}
	for k, v := range q.Metadata {
		if strings.TrimSpace(k) != "" {
			meta[k] = fmt.Sprint(v)
		}
	}
	if strings.TrimSpace(q.Query) != "" {
		meta["query"] = q.Query
	}
	if len(q.Hits) > 0 {
		meta["hit_count"] = fmt.Sprint(len(q.Hits))
		limit := len(q.Hits)
		if limit > 8 {
			limit = 8
		}
		for i := 0; i < limit; i++ {
			meta[fmt.Sprintf("hit_%d", i+1)] = fmt.Sprintf("%s:%.4f", q.Hits[i].ID, q.Hits[i].Score)
		}
	}
	if err := s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: q.Type, Summary: strings.TrimSpace(q.Message), Actor: q.Source, Reason: "integration event", Metadata: meta}); err != nil {
		s.err(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusAccepted, map[string]bool{"ok": true})
}
