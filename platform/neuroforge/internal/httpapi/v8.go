package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"neuroforge/internal/brain"
)

func (s *Server) ingestText(w http.ResponseWriter, r *http.Request) {
	var q brain.IngestTextRequest
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	out, err := s.brain.IngestText(r.Context(), q)
	if err != nil {
		s.err(w, 400, err)
		return
	}
	s.json(w, http.StatusCreated, out)
}

func (s *Server) ingestDocument(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Config()
	max := cfg.Ingestion.MaxDocumentBytes
	if max <= 0 {
		max = 25 << 20
	}
	// requestLimits already caps the full body; this is a second, route-specific
	// bound on the actual uploaded file.
	if err := r.ParseMultipartForm(max + (1 << 20)); err != nil {
		s.err(w, 400, err)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		s.err(w, 400, errors.New("multipart field 'file' is required"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		s.err(w, 400, err)
		return
	}
	if int64(len(data)) > max {
		s.err(w, http.StatusRequestEntityTooLarge, errors.New("document exceeds configured max_document_bytes"))
		return
	}
	trust, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("trust")), 64)
	tags := splitCSV(r.FormValue("tags"))
	name := filepath.Base(hdr.Filename)
	out, err := s.brain.IngestDocument(r.Context(), name, hdr.Header.Get("Content-Type"), r.FormValue("title"), data, tags, trust)
	if err != nil {
		s.err(w, 400, err)
		return
	}
	s.json(w, http.StatusCreated, out)
}

func (s *Server) sourcesList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	s.json(w, 200, s.store.SourcesSnapshot(limit))
}

func (s *Server) sourceGet(w http.ResponseWriter, r *http.Request) {
	x, ok := s.store.GetSource(r.PathValue("id"))
	if !ok {
		s.err(w, 404, errors.New("source not found"))
		return
	}
	s.json(w, 200, x)
}

func (s *Server) researchSearch(w http.ResponseWriter, r *http.Request) {
	var q brain.ResearchRequest
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	out, err := s.brain.Research(r.Context(), q)
	if err != nil {
		s.err(w, 502, err)
		return
	}
	s.json(w, 200, out)
}

func (s *Server) adminResearchGet(w http.ResponseWriter, r *http.Request) {
	c := s.store.Config()
	sec := s.store.Secrets()
	s.json(w, 200, map[string]any{"research": c.Research, "ingestion": c.Ingestion, "autonomy": map[string]any{"enabled": c.Autonomy.Enabled, "run_on_goal_create": c.Autonomy.RunOnGoalCreate, "default_goal_interval_minutes": c.Autonomy.DefaultGoalIntervalMinutes}, "searxng_auth_configured": strings.TrimSpace(sec.SearXNGAuthHeader) != ""})
}

func (s *Server) adminResearchPut(w http.ResponseWriter, r *http.Request) {
	var patch map[string]json.RawMessage
	if err := decode(r, &patch); err != nil {
		s.err(w, 400, err)
		return
	}
	c := s.store.Config()
	base, _ := json.Marshal(c)
	var root map[string]json.RawMessage
	_ = json.Unmarshal(base, &root)
	for _, key := range []string{"research", "ingestion"} {
		if v, ok := patch[key]; ok {
			root[key] = v
		}
	}
	if raw, ok := patch["autonomy"]; ok {
		var a map[string]json.RawMessage
		_ = json.Unmarshal(root["autonomy"], &a)
		var ap map[string]json.RawMessage
		if err := json.Unmarshal(raw, &ap); err != nil {
			s.err(w, 400, err)
			return
		}
		for k, v := range ap {
			a[k] = v
		}
		root["autonomy"], _ = json.Marshal(a)
	}
	merged, _ := json.Marshal(root)
	if err := json.Unmarshal(merged, &c); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.store.ValidateConfig(c); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.store.UpdateConfig(c); err != nil {
		s.err(w, 500, err)
		return
	}
	if raw, ok := patch["searxng_auth_header"]; ok {
		var auth string
		if json.Unmarshal(raw, &auth) == nil && strings.TrimSpace(auth) != "" {
			for _, r := range auth {
				if r < 0x20 || r > 0x7e {
					s.err(w, 400, errors.New("searxng_auth_header must contain printable ASCII only"))
					return
				}
			}
			sec := s.store.Secrets()
			sec.SearXNGAuthHeader = auth
			if err := s.store.UpdateSecrets(sec); err != nil {
				s.err(w, 500, err)
				return
			}
		}
	}
	s.adminResearchGet(w, r)
}

func (s *Server) adminResearchTest(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Query string `json:"query"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &q); err != nil {
			s.err(w, 400, err)
			return
		}
	}
	if strings.TrimSpace(q.Query) == "" {
		q.Query = "NVIDIA GPU CUDA"
	}
	out, err := s.brain.Research(r.Context(), brain.ResearchRequest{Query: q.Query, Learn: false, FetchPages: false, MaxResults: 5})
	if err != nil {
		s.err(w, 502, err)
		return
	}
	s.json(w, 200, out)
}

func splitCSV(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		x = strings.TrimSpace(x)
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
