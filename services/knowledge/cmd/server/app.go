package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"kb-editor/internal/aifallback"
	"kb-editor/internal/brainactivity"
	"kb-editor/internal/obsidian"
	"kb-editor/internal/staging"
	"kb-editor/internal/store"
)

type appConfig struct {
	Mode                     string `json:"mode"`
	Title                    string `json:"title"`
	Subtitle                 string `json:"subtitle"`
	Writable                 bool   `json:"writable"`
	AIFallbackEnabled        bool   `json:"ai_fallback_enabled"`
	AIFallbackTimeoutSeconds int    `json:"ai_fallback_timeout_seconds,omitempty"`
	AIFallbackModel          string `json:"ai_fallback_model,omitempty"`
	StagingEnabled           bool   `json:"staging_enabled"`
}

type app struct {
	store   *store.Store
	web     fs.FS
	config  appConfig
	ai      *aifallback.Service
	staging *staging.Store
}

func newApp(s *store.Store, web fs.FS, configs ...appConfig) *app {
	cfg := appConfig{Mode: "editor", Title: "Knowledge Base Editor", Subtitle: "JSON · Massenbearbeitung · Docker", Writable: true}
	if len(configs) > 0 {
		cfg = configs[0]
	}
	return &app{store: s, web: web, config: cfg}
}

func (a *app) withAI(service *aifallback.Service) *app {
	a.ai = service
	return a
}

func (a *app) withStaging(st *staging.Store) *app {
	a.staging = st
	a.config.StagingEnabled = st != nil
	return a
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/config", a.handleConfig)
	mux.HandleFunc("GET /api/items", a.handleList)
	mux.HandleFunc("GET /api/search", a.handleSearch)
	mux.HandleFunc("GET /api/facets", a.handleFacets)
	mux.HandleFunc("GET /api/export/obsidian", a.handleObsidianExport)
	mux.HandleFunc("GET /api/items/{key}", a.handleGet)
	mux.HandleFunc("POST /api/ai/fallback", a.handleAIFallback)
	mux.HandleFunc("GET /api/staging", a.handleStagingList)
	mux.HandleFunc("GET /api/staging/{key}", a.handleStagingGet)
	mux.HandleFunc("POST /api/integrations/staging", a.handleIntegrationStaging)

	if a.config.Writable {
		mux.HandleFunc("PUT /api/items/{key}", a.handlePut)
		mux.HandleFunc("POST /api/bulk", a.handleBulk)
		mux.HandleFunc("POST /api/reload", a.handleReload)
		mux.HandleFunc("PUT /api/staging/{key}", a.handleStagingPut)
		mux.HandleFunc("DELETE /api/staging/{key}", a.handleStagingDelete)
		mux.HandleFunc("POST /api/staging/{key}/promote", a.handleStagingPromote)
		mux.HandleFunc("POST /api/staging/bulk", a.handleStagingBulk)
	} else {
		mux.HandleFunc("PUT /api/items/{key}", a.handleReadOnly)
		mux.HandleFunc("POST /api/bulk", a.handleReadOnly)
		mux.HandleFunc("POST /api/reload", a.handleReadOnly)
		mux.HandleFunc("PUT /api/staging/{key}", a.handleReadOnly)
		mux.HandleFunc("DELETE /api/staging/{key}", a.handleReadOnly)
		mux.HandleFunc("POST /api/staging/{key}/promote", a.handleReadOnly)
		mux.HandleFunc("POST /api/staging/bulk", a.handleReadOnly)
	}

	static := http.FileServer(http.FS(a.web))
	mux.Handle("GET /", static)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	payload := map[string]any{
		"ok":                  true,
		"count":               a.store.Count(),
		"data_dir":            a.store.DataDir(),
		"mode":                a.config.Mode,
		"writable":            a.config.Writable,
		"ai_fallback_enabled": a.config.AIFallbackEnabled && a.ai != nil,
		"staging_enabled":     a.staging != nil,
	}
	if a.staging != nil {
		payload["staging_count"] = a.staging.Count()
		payload["staging_dir"] = a.staging.Dir()
	}
	if a.config.Writable {
		payload["backup_dir"] = a.store.BackupDir()
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *app) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.config)
}

func (a *app) handleList(w http.ResponseWriter, r *http.Request) {
	q := queryFromURL(r)
	writeJSON(w, http.StatusOK, a.store.List(q))
}

func (a *app) handleSearch(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	q := queryFromURL(r)
	result := a.store.Search(q)
	hits := make([]brainactivity.Hit, 0, len(result.Items))
	for _, hit := range result.Items {
		hits = append(hits, brainactivity.Hit{ID: hit.ID, Score: float64(hit.Score) / 100})
	}
	brainactivity.EmitSearch("knowledgebase", q.Q, hits, time.Since(startedAt))
	writeJSON(w, http.StatusOK, result)
}

func (a *app) handleObsidianExport(w http.ResponseWriter, r *http.Request) {
	records := a.store.ExportDocuments()
	docs := make([]obsidian.Document, 0, len(records))
	for _, record := range records {
		docs = append(docs, obsidian.Document{Data: record.Document, ModifiedAt: record.Summary.ModifiedAt})
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="glpi-knowledge-obsidian.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := obsidian.WriteZIP(w, docs, time.Now().UTC()); err != nil {
		return
	}
}

func (a *app) handleFacets(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, http.StatusOK, a.store.Facets(limit))
}

func queryFromURL(r *http.Request) store.Query {
	v := r.URL.Query()
	page, _ := strconv.Atoi(v.Get("page"))
	pageSize, _ := strconv.Atoi(v.Get("page_size"))
	return store.Query{
		Q:                  v.Get("q"),
		AutoReply:          v.Get("auto_reply"),
		Language:           v.Get("language"),
		CommunicationStyle: v.Get("communication_style"),
		Source:             v.Get("source"),
		Page:               page,
		PageSize:           pageSize,
	}
}

func (a *app) handleGet(w http.ResponseWriter, r *http.Request) {
	doc, meta, err := a.store.Get(r.PathValue("key"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Eintrag nicht gefunden")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"document": doc, "meta": meta})
}

type aiFallbackRequest struct {
	Query string `json:"query"`
}

func (a *app) handleAIFallback(w http.ResponseWriter, r *http.Request) {
	if !a.config.AIFallbackEnabled || a.ai == nil {
		writeError(w, http.StatusNotFound, "KI-Fallback ist auf dieser Instanz deaktiviert")
		return
	}
	if !mustJSONContentType(w, r) {
		return
	}
	var req aiFallbackRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültige Anfrage: "+err.Error())
		return
	}
	query := strings.TrimSpace(req.Query)
	if len([]rune(query)) < 3 {
		writeError(w, http.StatusBadRequest, "Suchanfrage ist für den KI-Fallback zu kurz")
		return
	}
	// Server-side guard: AI generation is only permitted when the regular KB has zero hits.
	check := a.store.Search(store.Query{Q: query, Page: 1, PageSize: 1})
	if check.Total > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "Die Wissensbasis enthält inzwischen passende Treffer; KI-Fallback wurde nicht gestartet",
			"total": check.Total,
		})
		return
	}
	result, err := a.ai.Generate(r.Context(), query)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "KI-Fallback hat das Zeitlimit überschritten")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

type integrationStagingRequest struct {
	Source     string   `json:"source"`
	Query      string   `json:"query"`
	Title      string   `json:"title"`
	Text       string   `json:"text"`
	Answer     string   `json:"answer"`
	Categories []string `json:"categories"`
	Keywords   []string `json:"keywords"`
	MinScore   *float64 `json:"min_score,omitempty"`
}

func integrationBearerAuthorized(r *http.Request) (bool, bool) {
	expected := strings.TrimSpace(os.Getenv("KB_INTEGRATION_TOKEN"))
	if expected == "" {
		return false, false
	}
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(got, prefix) {
		return true, false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(got, prefix))
	return true, subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// handleIntegrationStaging is a one-way governance boundary: machine-generated
// research may enter human review, but it cannot write production knowledge or
// enable automatic replies.
func (a *app) handleIntegrationStaging(w http.ResponseWriter, r *http.Request) {
	enabled, authorized := integrationBearerAuthorized(r)
	if !enabled {
		writeError(w, http.StatusServiceUnavailable, "KB staging integration is disabled")
		return
	}
	if !authorized {
		writeError(w, http.StatusUnauthorized, "invalid integration token")
		return
	}
	if a.staging == nil {
		writeError(w, http.StatusServiceUnavailable, "staging is unavailable")
		return
	}
	if !mustJSONContentType(w, r) {
		return
	}
	var req integrationStagingRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		req.Source = "NeuroForge Research"
	}
	minScore := 0.85
	if req.MinScore != nil {
		minScore = *req.MinScore
	}
	result, err := a.staging.SaveFromSource(req.Query, req.Source, staging.Draft{
		Title: req.Title, Text: req.Text, Answer: req.Answer, Categories: req.Categories, Keywords: req.Keywords,
	}, false, minScore)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "staging": result, "governance": "human-review-required", "auto_reply": false,
	})
}

func (a *app) handleStagingList(w http.ResponseWriter, r *http.Request) {
	if !a.config.Writable {
		writeError(w, http.StatusForbidden, "Die Staging-Liste ist nur im Editor-Modus verfügbar")
		return
	}
	if a.staging == nil {
		writeError(w, http.StatusNotFound, "Staging ist auf dieser Instanz nicht konfiguriert")
		return
	}
	v := r.URL.Query()
	page, _ := strconv.Atoi(v.Get("page"))
	pageSize, _ := strconv.Atoi(v.Get("page_size"))
	result, err := a.staging.List(staging.Query{
		Q:                  v.Get("q"),
		AutoReply:          v.Get("auto_reply"),
		Language:           v.Get("language"),
		CommunicationStyle: v.Get("communication_style"),
		Source:             v.Get("source"),
		Page:               page,
		PageSize:           pageSize,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) handleStagingGet(w http.ResponseWriter, r *http.Request) {
	if a.staging == nil || (!a.config.Writable && (!a.config.AIFallbackEnabled || a.ai == nil)) {
		writeError(w, http.StatusNotFound, "Staging ist auf dieser Instanz nicht konfiguriert")
		return
	}
	result, err := a.staging.Get(r.PathValue("key"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Staging-Eintrag nicht gefunden")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type stagingBulkRequest struct {
	Keys   []string `json:"keys"`
	Action string   `json:"action"`
}

func (a *app) handleStagingPut(w http.ResponseWriter, r *http.Request) {
	if a.staging == nil {
		writeError(w, http.StatusNotFound, "Staging ist auf dieser Instanz nicht konfiguriert")
		return
	}
	if !mustJSONContentType(w, r) {
		return
	}
	var doc map[string]any
	if err := decodeJSON(r, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültiges JSON: "+err.Error())
		return
	}
	result, err := a.staging.Update(r.PathValue("key"), doc)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Staging-Eintrag nicht gefunden")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "document": result.Document, "meta": result.Meta})
}

func (a *app) handleStagingDelete(w http.ResponseWriter, r *http.Request) {
	if a.staging == nil {
		writeError(w, http.StatusNotFound, "Staging ist auf dieser Instanz nicht konfiguriert")
		return
	}
	trash, err := a.staging.Delete(r.PathValue("key"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Staging-Eintrag nicht gefunden")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trash": trash})
}

func (a *app) handleStagingPromote(w http.ResponseWriter, r *http.Request) {
	if a.staging == nil {
		writeError(w, http.StatusNotFound, "Staging ist auf dieser Instanz nicht konfiguriert")
		return
	}
	result, err := a.promoteStaging(r.PathValue("key"))
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Staging-Eintrag nicht gefunden")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (a *app) handleStagingBulk(w http.ResponseWriter, r *http.Request) {
	if a.staging == nil {
		writeError(w, http.StatusNotFound, "Staging ist auf dieser Instanz nicht konfiguriert")
		return
	}
	if !mustJSONContentType(w, r) {
		return
	}
	var req stagingBulkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültige Anfrage: "+err.Error())
		return
	}
	if len(req.Keys) == 0 {
		writeError(w, http.StatusBadRequest, "Keine Staging-Dateien ausgewählt")
		return
	}
	if len(req.Keys) > 500 {
		writeError(w, http.StatusBadRequest, "Maximal 500 Staging-Dateien pro Vorgang")
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "promote" && action != "delete" {
		writeError(w, http.StatusBadRequest, "action muss promote oder delete sein")
		return
	}
	type itemResult struct {
		Key   string `json:"key"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	items := make([]itemResult, 0, len(req.Keys))
	succeeded := 0
	for _, key := range req.Keys {
		key = strings.TrimSpace(key)
		var err error
		if action == "promote" {
			_, err = a.promoteStaging(key)
		} else {
			_, err = a.staging.Delete(key)
		}
		item := itemResult{Key: key, OK: err == nil}
		if err != nil {
			item.Error = err.Error()
		} else {
			succeeded++
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"action": action, "targeted": len(req.Keys), "succeeded": succeeded,
		"failed": len(req.Keys) - succeeded, "items": items,
	})
}

func (a *app) promoteStaging(key string) (map[string]any, error) {
	staged, err := a.staging.Get(key)
	if err != nil {
		return nil, err
	}
	summary, err := a.store.ImportDocument(staged.Document, key)
	if err != nil {
		return nil, err
	}
	archive, err := a.staging.ArchiveApproved(key)
	if err != nil {
		return nil, fmt.Errorf("Produktivdatei wurde erstellt (%s), aber Staging konnte nicht als freigegeben archiviert werden: %w", summary.RelPath, err)
	}
	return map[string]any{"ok": true, "production": summary, "staging_key": key, "staging_archive": archive}, nil
}

func (a *app) handleReadOnly(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusForbidden, "Diese Instanz läuft im Google-/Viewer-Modus und ist schreibgeschützt")
}

func (a *app) handlePut(w http.ResponseWriter, r *http.Request) {
	if !mustJSONContentType(w, r) {
		return
	}
	var doc map[string]any
	if err := decodeJSON(r, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültiges JSON: "+err.Error())
		return
	}
	meta, backup, err := a.store.Save(r.PathValue("key"), doc)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "Eintrag nicht gefunden")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "meta": meta, "backup": backup})
}

type bulkRequest struct {
	Keys        []string        `json:"keys"`
	AllMatching bool            `json:"all_matching"`
	Query       store.Query     `json:"query"`
	Patch       store.BulkPatch `json:"patch"`
	DryRun      bool            `json:"dry_run"`
}

func (a *app) handleBulk(w http.ResponseWriter, r *http.Request) {
	if !mustJSONContentType(w, r) {
		return
	}
	var req bulkRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültige Anfrage: "+err.Error())
		return
	}
	keys := req.Keys
	if req.AllMatching {
		keys = a.store.MatchingKeys(req.Query)
	}
	if len(keys) == 0 {
		writeError(w, http.StatusBadRequest, "Keine Zieldateien ausgewählt")
		return
	}
	result, err := a.store.ApplyBulk(keys, req.Patch, req.DryRun)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) handleReload(w http.ResponseWriter, r *http.Request) {
	if !mustJSONContentType(w, r) {
		return
	}
	if err := a.store.Reload(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": a.store.Count()})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("mehr als ein JSON-Wert im Request")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": strings.TrimSpace(message)})
}
