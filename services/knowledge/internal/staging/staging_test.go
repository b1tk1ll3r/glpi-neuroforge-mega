package staging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSaveAndGet(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Save("0xDEADBEEF test", "test-model", Draft{
		Title: "Testartikel", Text: "Symptom", Answer: "Lösung",
		Categories: []string{"Windows"}, Keywords: []string{"Fehler"},
	}, false, 0.78)
	if err != nil {
		t.Fatal(err)
	}
	if result.Key == "" || result.Document["auto_reply"] != false {
		t.Fatalf("unexpected result: %+v", result)
	}
	path := filepath.Join(s.Dir(), result.Key+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["id"] != result.Key || doc["source"] == "" {
		t.Fatalf("unexpected document: %+v", doc)
	}
	loaded, err := s.Get(result.Key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document["title"] != "Testartikel" {
		t.Fatalf("loaded=%+v", loaded)
	}
}

func TestListUpdateAndSoftDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	one, err := s.Save("0xFEEDFACE Netzwerk", "model", Draft{Title: "Netzwerk", Answer: "Prüfen"}, false, .78)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save("anderes", "model", Draft{Title: "Drucker", Answer: "Prüfen"}, false, .78); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(Query{Q: "FEEDFACE", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || list.Items[0].Key != one.Key {
		t.Fatalf("unexpected list: %+v", list)
	}
	one.Document["title"] = "Geprüftes Netzwerk"
	updated, err := s.Update(one.Key, one.Document)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Document["title"] != "Geprüftes Netzwerk" {
		t.Fatalf("update failed: %+v", updated.Document)
	}
	trash, err := s.Delete(one.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(trash); err != nil {
		t.Fatalf("trash file missing: %v", err)
	}
	if _, err := s.Get(one.Key); !os.IsNotExist(err) {
		t.Fatalf("deleted staging file should be gone, err=%v", err)
	}
}

func TestIntegrationKeyUpdatesExistingDraft(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.SaveFromIntegration("vpn", "NeuroForge Research", Draft{Title: "VPN", Answer: "Erste Fassung"}, false, .85, IntegrationOptions{IntegrationKey: "neuroforge-goal:g1", Metadata: map[string]any{"research_goal_id": "g1"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveFromIntegration("vpn", "NeuroForge Research", Draft{Title: "VPN", Answer: "Aktualisierte Fassung"}, false, .85, IntegrationOptions{IntegrationKey: "neuroforge-goal:g1", Metadata: map[string]any{"research_goal_id": "g1"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Key != second.Key {
		t.Fatalf("expected stable staging key, got %q then %q", first.Key, second.Key)
	}
	if second.Document["answer"] != "Aktualisierte Fassung" {
		t.Fatalf("draft was not updated: %#v", second.Document)
	}
	if second.Document["auto_reply"] != false {
		t.Fatalf("integration must remain auto_reply=false")
	}
	list, err := s.List(Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 {
		t.Fatalf("expected one active staging draft, got %d", list.Total)
	}
}

func TestIntegrationMetadataCannotOverrideComputedFields(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.SaveFromIntegration("vpn", "NeuroForge Research", Draft{Title: "VPN", Answer: "Antwort"}, false, .85, IntegrationOptions{
		IntegrationKey: "neuroforge-goal:g1",
		Metadata: map[string]any{
			"id": "KB-EVIL", "auto_reply": true, "source": "Forged", "categories": []string{"Prod"},
			"answer": "Forged", "title": "Forged", "integration_key": "other", "min_score": 0.1,
			"created_at": "1970-01-01T00:00:00Z", "language": "en-US", "research_goal_id": "g1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := result.Document
	if doc["id"] == "KB-EVIL" || doc["auto_reply"] != false || doc["source"] != "NeuroForge Research (AI-Staging)" ||
		doc["answer"] != "Antwort" || doc["title"] != "VPN" || doc["integration_key"] != "neuroforge-goal:g1" ||
		doc["created_at"] == "1970-01-01T00:00:00Z" || doc["language"] != "de-DE" {
		t.Fatalf("metadata overrode computed fields: %#v", doc)
	}
	if cats := toStrings(doc["categories"]); len(cats) != 1 || cats[0] != "AI-Staging" {
		t.Fatalf("metadata overrode categories: %#v", doc["categories"])
	}
	if score, _ := number(doc["min_score"]); score != .85 {
		t.Fatalf("metadata overrode min_score: %#v", doc["min_score"])
	}
	if doc["research_goal_id"] != "g1" {
		t.Fatalf("new metadata key was not added: %#v", doc)
	}
}

func TestIntegrationKeyConcurrentSavesDoNotDuplicate(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.SaveFromIntegration(fmt.Sprintf("vpn %d", i), "NeuroForge Research", Draft{Title: "VPN", Answer: "Antwort"}, false, .85, IntegrationOptions{IntegrationKey: "neuroforge-goal:g1"})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := s.Count(); n != 1 {
		t.Fatalf("expected one staging draft after concurrent saves, got %d", n)
	}
}
