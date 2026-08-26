package web

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/knowledge"
	"github.com/example/glpi-ai-agent/internal/learning"
	"github.com/example/glpi-ai-agent/internal/metrics"
	"github.com/example/glpi-ai-agent/internal/model"
	"github.com/example/glpi-ai-agent/internal/queue"
	"github.com/example/glpi-ai-agent/internal/state"
)

func TestExtractTicketID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int64
	}{
		{name: "explicit ticket id", body: `{"ticket_id":42}`, want: 42},
		{name: "camel case ticket id", body: `{"ticketId":"43"}`, want: 43},
		{name: "typed ticket", body: `{"itemtype":"Ticket","id":44}`, want: 44},
		{name: "nested ticket", body: `{"ticket":{"id":45}}`, want: 45},
		{name: "ticket url", body: `{"url":"https://glpi.example/api.php/v2.3/Assistance/Ticket/46"}`, want: 46},
		{name: "unrelated generic id", body: `{"itemtype":"User","id":99}`, want: 0},
		{name: "wrapped unrelated generic id", body: `{"data":{"id":100}}`, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractTicketID([]byte(tt.body)); got != tt.want {
				t.Fatalf("extractTicketID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDashboardTemplateParses(t *testing.T) {
	if _, err := template.ParseFS(files, "templates/*.html"); err != nil {
		t.Fatal(err)
	}
}

type fakeFeedback struct {
	cats        []model.Category
	outcomeRows []model.ValidatedOutcomeEvidence
}

func (f fakeFeedback) Categories(context.Context) ([]model.Category, error) { return f.cats, nil }
func (f fakeFeedback) RecordCategoryFeedback(context.Context, string, int64) (model.LearningExample, error) {
	return model.LearningExample{}, nil
}
func (f fakeFeedback) LearningExamples() []model.LearningExample { return nil }
func (f fakeFeedback) DeleteLearning(string) error               { return os.ErrNotExist }
func (f fakeFeedback) LearningCount() int                        { return 0 }
func (f fakeFeedback) RecordTicketOutcome(context.Context, string, string, string, string, string) (learning.TicketOutcome, error) {
	return learning.TicketOutcome{}, nil
}
func (f fakeFeedback) TicketOutcomes() []learning.TicketOutcome { return nil }
func (f fakeFeedback) SearchValidatedOutcomes(context.Context, string, int) ([]model.ValidatedOutcomeEvidence, error) {
	return append([]model.ValidatedOutcomeEvidence(nil), f.outcomeRows...), nil
}

func newKnowledgeTestServer(t *testing.T) (http.Handler, *knowledge.Store) {
	t.Helper()
	root := t.TempDir()
	staticDir := filepath.Join(root, "knowledge")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(staticDir, 0o750); err != nil {
		t.Fatal(err)
	}
	store, err := knowledge.Load(context.Background(), staticDir, dataDir, nil, false, []string{"internal-kb"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{WebAllowAnonymous: true, KnowledgeWebEditEnabled: true, CommunicationLanguage: "de-DE", CommunicationStyle: "formal", KnowledgeAllowedSources: []string{"internal-kb"}}
	srv, err := New(cfg, metrics.New(), nil, queue.New(8), store, fakeFeedback{cats: []model.Category{{ID: 2, Name: "Active Directory", CompleteName: "IT > Active Directory"}}})
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler(), store
}

func mutationRequest(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &b)
	req.Header.Set("X-Requested-With", "GLPI-AI-Agent")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestKnowledgeCreateUpdateDeleteFlow(t *testing.T) {
	h, store := newKnowledgeTestServer(t)
	doc := model.KnowledgeDoc{ID: "KB-TEST-1", Title: "Benutzerkonto gesperrt", Text: "Konto ist gesperrt.", Answer: "Bitte versuchen Sie die Anmeldung erneut.", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal", MinScore: .7, Categories: []int64{2}}
	if rr := mutationRequest(t, h, http.MethodPost, "/api/knowledge", doc); rr.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rr.Code, rr.Body.String())
	}
	if _, ok := store.ByID(doc.ID); !ok {
		t.Fatal("created document missing")
	}
	if rr := mutationRequest(t, h, http.MethodPost, "/api/knowledge", doc); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", rr.Code)
	}

	doc.Title = "Benutzerkonto dauerhaft gesperrt"
	if rr := mutationRequest(t, h, http.MethodPut, "/api/knowledge/KB-TEST-1", doc); rr.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", rr.Code, rr.Body.String())
	}
	got, ok := store.ByID(doc.ID)
	if !ok || got.Title != doc.Title {
		t.Fatalf("updated doc = %#v, ok=%v", got, ok)
	}

	renamed := doc
	renamed.ID = "KB-RENAMED"
	if rr := mutationRequest(t, h, http.MethodPut, "/api/knowledge/KB-TEST-1", renamed); rr.Code != http.StatusConflict {
		t.Fatalf("rename update = %d, want 409", rr.Code)
	}
	if _, ok := store.ByID("KB-RENAMED"); ok {
		t.Fatal("rename unexpectedly created a second document")
	}

	if rr := mutationRequest(t, h, http.MethodDelete, "/api/knowledge/KB-TEST-1", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", rr.Code, rr.Body.String())
	}
	if _, ok := store.ByID(doc.ID); ok {
		t.Fatal("deleted document still present")
	}
}

func TestKnowledgeGetReturnsManagedState(t *testing.T) {
	h, _ := newKnowledgeTestServer(t)
	doc := model.KnowledgeDoc{ID: "KB-TEST-2", Title: "VPN", Text: "VPN Hilfe", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal", MinScore: .7}
	if rr := mutationRequest(t, h, http.MethodPost, "/api/knowledge", doc); rr.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rr.Code, rr.Body.String())
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/knowledge/KB-TEST-2", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Managed  bool               `json:"managed"`
		Document model.KnowledgeDoc `json:"document"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Managed || got.Document.ID != doc.ID {
		t.Fatalf("unexpected get response: %#v", got)
	}
}

func TestDashboardContainsControlCenterSections(t *testing.T) {
	h, _ := newKnowledgeTestServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("dashboard = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Control Center", "Verarbeitungen", "Knowledge Base", "Effektive Konfiguration", "Knowledge-Ranking", "Priorität & Eskalation"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Fatalf("dashboard missing %q", want)
		}
	}
}

func TestDiagnosticsTemplateAndKnowledgeSearch(t *testing.T) {
	h, _ := newKnowledgeTestServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/diagnostics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("diagnostics = %d: %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"Entscheidungsdiagnose", "Prioritätsentscheidung", "priority_checks", "Begründung Prioritätsanalyse"} {
		if !bytes.Contains(rr.Body.Bytes(), []byte(want)) {
			t.Fatalf("diagnostics page missing %q", want)
		}
	}
}

func TestCategoryMappingEditorFlow(t *testing.T) {
	root := t.TempDir()
	staticDir := filepath.Join(root, "knowledge")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(staticDir, 0o750); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(dataDir, "knowledge-category-map.json")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	doc := `{"id":"KB-OUTLOOK","title":"Outlook","text":"Signatur","answer":"Antwort","auto_reply":true,"categories":["Outlook"],"keywords":["Outlook"],"source":"internal-kb","language":"de-DE","communication_style":"formal"}`
	if err := os.WriteFile(filepath.Join(staticDir, "outlook.json"), []byte(doc), 0o640); err != nil {
		t.Fatal(err)
	}
	store, err := knowledge.Load(context.Background(), staticDir, dataDir, nil, false, []string{"internal-kb"}, knowledge.ScoringConfig{IndexMode: "incremental", CategoryMode: "unscoped", CategoryMapFile: mapPath})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		WebAllowAnonymous: true, KnowledgeWebEditEnabled: true,
		KnowledgeCategoryMapFile: mapPath,
		CommunicationLanguage:    "de-DE", CommunicationStyle: "formal",
		KnowledgeAllowedSources: []string{"internal-kb"},
	}
	feedback := fakeFeedback{cats: []model.Category{
		{ID: 12, Name: "Outlook", CompleteName: "E-Mail > Outlook"},
		{ID: 18, Name: "Office", CompleteName: "Software > Office"},
	}}
	srv, err := New(cfg, metrics.New(), nil, queue.New(8), store, feedback)
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/category-mappings", nil))
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte("Kategorie-Mapping")) {
		t.Fatalf("mapping page = %d: %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/category-mappings", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("mapping get = %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Editable bool `json:"editable"`
		Mapping  struct {
			ObservedCount    int `json:"observed_count"`
			UnmappedObserved int `json:"unmapped_observed"`
		} `json:"mapping"`
		GLPI []struct {
			ID int64 `json:"id"`
		} `json:"glpi"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Editable || got.Mapping.ObservedCount != 1 || got.Mapping.UnmappedObserved != 1 || len(got.GLPI) != 2 {
		t.Fatalf("unexpected mapping api response: %+v", got)
	}

	payload := map[string]any{"mappings": []map[string]any{{"label": "Outlook", "category_ids": []int64{12}}}}
	rr = mutationRequest(t, h, http.MethodPut, "/api/category-mappings", payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("mapping put = %d: %s", rr.Code, rr.Body.String())
	}
	updated, ok := store.ByID("KB-OUTLOOK")
	if !ok || len(updated.Categories) != 1 || updated.Categories[0] != 12 || !updated.AutoReply {
		t.Fatalf("mapping not applied to knowledge doc: %+v, ok=%v", updated, ok)
	}
}

func TestAnalysisDiagnosticEndpointReturnsIndependentRun(t *testing.T) {
	root := t.TempDir()
	staticDir := filepath.Join(root, "knowledge")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(staticDir, 0o750); err != nil {
		t.Fatal(err)
	}
	knowledgeStore, err := knowledge.Load(context.Background(), staticDir, dataDir, nil, false, []string{"internal-kb"})
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.Open(dataDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	analysis := model.AnalysisRun{AnalysisID: "analysis-priority-1", ParentRunID: "run-1", TicketID: 7, AnalysisType: "priority", PromptVersion: "priority-v1", InputHash: "abc", Outcome: "processed"}
	if err := stateStore.Append(model.RunRecord{RunID: "run-1", TicketID: 7, SourceVersion: "v1", Outcome: "processed", FinishedAt: time.Now(), Analyses: []model.AnalysisRun{analysis}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{WebAllowAnonymous: true, CommunicationLanguage: "de-DE", CommunicationStyle: "formal", KnowledgeAllowedSources: []string{"internal-kb"}}
	srv, err := New(cfg, metrics.New(), stateStore, queue.New(8), knowledgeStore, fakeFeedback{})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/diagnostics/analysis/analysis-priority-1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("analysis endpoint = %d: %s", rr.Code, rr.Body.String())
	}
	var got model.AnalysisRun
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AnalysisID != analysis.AnalysisID || got.AnalysisType != "priority" || got.ParentRunID != "run-1" {
		t.Fatalf("unexpected analysis response: %+v", got)
	}
}

func TestManualReprocessQueuesForcedWorkItem(t *testing.T) {
	h, _ := newKnowledgeTestServer(t)
	rr := mutationRequest(t, h, http.MethodPost, "/api/tickets/42/reprocess", map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("reprocess = %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["accepted"] != true || int64(body["ticket_id"].(float64)) != 42 {
		t.Fatalf("unexpected response: %#v", body)
	}
}

type fakeOllamaNodeProvider struct {
	statuses []model.OllamaNodeStatus
}

func (f fakeOllamaNodeProvider) NodeStatuses() []model.OllamaNodeStatus {
	return append([]model.OllamaNodeStatus(nil), f.statuses...)
}
func (f fakeOllamaNodeProvider) RoutingMode() string { return "least_inflight" }

func TestPrometheusOllamaNodeMetricTypesAreEmittedOnce(t *testing.T) {
	s := &Server{metrics: metrics.New(), ollamaNodes: fakeOllamaNodeProvider{statuses: []model.OllamaNodeStatus{
		{Name: "node-a", Healthy: true, Compatible: true, Available: true},
		{Name: "node-b", Healthy: true, Compatible: true, Available: true},
	}}}
	rr := httptest.NewRecorder()
	s.prom(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, metric := range []string{
		"glpi_agent_ollama_node_healthy", "glpi_agent_ollama_node_available",
		"glpi_agent_ollama_node_inflight", "glpi_agent_ollama_node_requests_total",
		"glpi_agent_ollama_node_failures_total", "glpi_agent_ollama_node_average_duration_ms",
		"glpi_agent_outcome_searches_total", "glpi_agent_outcome_search_hits_total",
		"glpi_agent_outcome_learning_corrected_total", "glpi_agent_outcome_learning_failed_total",
	} {
		if got := strings.Count(body, "# TYPE "+metric+" "); got != 1 {
			t.Fatalf("TYPE for %s emitted %d times:\n%s", metric, got, body)
		}
	}
}

func TestQualityReplayReportsKnowledgeAndExperienceMetrics(t *testing.T) {
	root := t.TempDir()
	staticDir := filepath.Join(root, "knowledge")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(staticDir, 0o750); err != nil {
		t.Fatal(err)
	}
	store, err := knowledge.Load(context.Background(), staticDir, dataDir, nil, false, []string{"internal-kb"})
	if err != nil {
		t.Fatal(err)
	}
	doc := model.KnowledgeDoc{ID: "KB-VPN", Title: "VPN Gateway", Text: "vpn gateway anmeldung", Answer: "VPN Client neu starten", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal"}
	if err := store.Upsert(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{WebAllowAnonymous: true, CommunicationLanguage: "de-DE", CommunicationStyle: "formal", KnowledgeAllowedSources: []string{"internal-kb"}}
	fb := fakeFeedback{outcomeRows: []model.ValidatedOutcomeEvidence{{MemoryID: "m1", Text: "Verified solution: VPN Client neu starten", Similarity: .91, Source: "glpi.outcome.accepted"}}}
	srv, err := New(cfg, metrics.New(), nil, queue.New(8), store, fb)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"cases":[{"id":"c1","query":"vpn gateway anmeldung","expected_knowledge_id":"KB-VPN","expected_solution_terms":["vpn","neu starten"],"k":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/quality/replay", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Summary map[string]any `json:"summary"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Summary["knowledge_recall_at_k"].(float64) != 1 || out.Summary["outcome_recall_at_k"].(float64) != 1 {
		t.Fatalf("unexpected summary %#v", out.Summary)
	}
}
