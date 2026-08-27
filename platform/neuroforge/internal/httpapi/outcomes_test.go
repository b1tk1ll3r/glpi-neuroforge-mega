package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidatedOutcomeLearnsTrustedProvenance(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer fake.Close()

	s, _ := newMetricsTestServer(t)
	cfg := s.store.Config()
	cfg.Ollama[0].BaseURL = fake.URL
	cfg.Brain.ExternalRelinkWorker = false
	cfg.Brain.LearningPolicy.SourceTrust["glpi.outcome.accepted"] = 1
	cfg.Brain.LearningPolicy.SourceTrust["glpi.outcome.corrected"] = 1
	cfg.Brain.LearningPolicy.LearnChatResponses = false
	if err := s.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	sec := s.store.Secrets()

	body := `{"outcome_id":"out-1","run_id":"run-1","ticket_id":42,"decision":"accepted","ticket_input":"VPN verbindet nicht","proposed_reply":"VPN Client neu starten","confirmed_reply":"VPN Client neu starten","category_id":5,"category_name":"VPN","knowledge_id":"kb-vpn","actor":"tech-a"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/outcomes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sec.IntegrationToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Memory struct {
			ID         string `json:"id"`
			Provenance struct {
				Source   string `json:"source"`
				Actor    string `json:"actor"`
				SourceID string `json:"source_id"`
			} `json:"provenance"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Memory.ID == "" || out.Memory.Provenance.Source != "glpi.outcome.accepted" || out.Memory.Provenance.Actor != "tech-a" || out.Memory.Provenance.SourceID != "out-1" {
		t.Fatalf("unexpected outcome memory: %#v body=%s", out, rr.Body.String())
	}
}

func TestValidatedOutcomeRejectsUnconfirmedDecision(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	sec := s.store.Secrets()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/outcomes", strings.NewReader(`{"outcome_id":"o","run_id":"r","ticket_id":1,"decision":"rejected","ticket_input":"x","confirmed_reply":"y","actor":"tech"}`))
	req.Header.Set("Authorization", "Bearer "+sec.IntegrationToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestValidatedOutcomeCorrectionSupersedesPriorMemoryAndSearchesOnlyActiveRevision(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := strings.ToLower(strings.TrimSpace(fmt.Sprint(body["input"])))
		vec := []float32{1, 0, 0}
		if strings.Contains(text, "korrigierte loesung") {
			vec = []float32{0, 1, 0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{vec}})
	}))
	defer fake.Close()

	s, _ := newMetricsTestServer(t)
	cfg := s.store.Config()
	cfg.Ollama[0].BaseURL = fake.URL
	cfg.Brain.ExternalRelinkWorker = false
	cfg.Brain.LearningPolicy.DuplicateSimilarity = 0.99999
	cfg.Brain.LearningPolicy.SourceTrust["glpi.outcome.accepted"] = 1
	cfg.Brain.LearningPolicy.SourceTrust["glpi.outcome.corrected"] = 1
	if err := s.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	sec := s.store.Secrets()
	post := func(body string) map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/outcomes", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+sec.IntegrationToken)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	oldOut := post(`{"outcome_id":"old","run_id":"r1","ticket_id":7,"decision":"accepted","ticket_input":"Drucker druckt nicht","proposed_reply":"Treiber neu starten","confirmed_reply":"Treiber neu starten","actor":"tech"}`)
	oldID := oldOut["memory"].(map[string]any)["id"].(string)
	newOut := post(`{"outcome_id":"new","run_id":"r2","ticket_id":7,"decision":"corrected","ticket_input":"Drucker druckt nicht","proposed_reply":"Treiber neu starten","confirmed_reply":"Korrigierte Loesung: Printserver Queue bereinigen","supersedes_id":"old","actor":"tech"}`)
	newID := newOut["memory"].(map[string]any)["id"].(string)
	if oldID == newID {
		t.Fatal("correction must create a distinct memory")
	}
	var oldStatus string
	for _, m := range s.store.MemoriesSnapshot() {
		if m.ID == oldID {
			oldStatus = m.Status
		}
	}
	if oldStatus != "superseded" {
		t.Fatalf("old status=%q, want superseded", oldStatus)
	}
	newMem, ok := s.store.GetMemory(newID)
	if !ok {
		t.Fatal("corrected memory missing")
	}
	if strings.Contains(newMem.Text, "Treiber neu starten") {
		t.Fatalf("superseded AI proposal leaked into active corrected memory: %q", newMem.Text)
	}

	search := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/outcomes/search", strings.NewReader(`{"text":"Drucker korrigierte Loesung","k":10,"min_similarity":0}`))
	search.Header.Set("Authorization", "Bearer "+sec.IntegrationToken)
	search.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, search)
	if rr.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", rr.Code, rr.Body.String())
	}
	var hits []struct {
		Memory struct {
			ID string `json:"id"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Memory.ID == oldID {
			t.Fatal("superseded outcome leaked into active retrieval")
		}
	}
	found := false
	for _, h := range hits {
		found = found || h.Memory.ID == newID
	}
	if !found {
		t.Fatalf("corrected outcome not found; hits=%#v", hits)
	}
}
