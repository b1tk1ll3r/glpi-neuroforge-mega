package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDoc(t *testing.T, dir, name string, doc map[string]any) {
	t.Helper()
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSearchSaveAndBulk(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "a.json", map[string]any{
		"id": "KB-1", "title": "Fehler 0x80070005", "text": "Zugriff verweigert",
		"answer": "Prüfen", "auto_reply": true, "min_score": 0.78,
		"keywords": []string{"Windows", "0x80070005"}, "categories": []string{},
		"language": "de-DE", "communication_style": "formal", "source": "Microsoft Learn",
		"custom_field": "must survive",
	})
	writeDoc(t, dir, "b.json", map[string]any{
		"id": "KB-2", "title": "Setup", "auto_reply": false, "keywords": []string{"Setup"},
	})

	t.Setenv("BACKUP_DIR", filepath.Join(dir, "backups"))
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 2 {
		t.Fatalf("count=%d", s.Count())
	}

	got := s.List(Query{Q: "80070005", Page: 1, PageSize: 10})
	if got.Total != 1 || got.Items[0].ID != "KB-1" {
		t.Fatalf("unexpected list: %+v", got)
	}

	key := got.Items[0].Key
	doc, _, err := s.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	doc["answer"] = "Neue Lösung"
	if _, _, err := s.Save(key, doc); err != nil {
		t.Fatal(err)
	}

	reloaded, _, err := s.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded["custom_field"] != "must survive" {
		t.Fatal("unknown field was lost")
	}
	if reloaded["answer"] != "Neue Lösung" {
		t.Fatalf("answer=%v", reloaded["answer"])
	}

	yes := true
	patch := BulkPatch{SetAutoReply: &yes, AddKeywords: []string{"Geprüft"}, FindReplace: &FindReplace{
		Fields: []string{"title"}, Find: "setup", Replace: "Upgrade", CaseSensitive: false,
	}}
	all := s.MatchingKeys(Query{Page: 1, PageSize: 100})
	preview, err := s.ApplyBulk(all, patch, true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Changed != 2 {
		t.Fatalf("preview changed=%d", preview.Changed)
	}
	applied, err := s.ApplyBulk(all, patch, false)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Changed != 2 || applied.Backup == "" {
		t.Fatalf("applied=%+v", applied)
	}

	setup := s.List(Query{Q: "upgrade", Page: 1, PageSize: 10})
	if setup.Total != 1 {
		t.Fatalf("upgrade total=%d", setup.Total)
	}
}

func TestExternalChangeIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "a.json", map[string]any{"id": "KB-1", "title": "Original", "auto_reply": true})
	t.Setenv("BACKUP_DIR", filepath.Join(dir, "..", "backups-external"))
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	item := s.List(Query{Page: 1, PageSize: 10}).Items[0]
	doc, _, err := s.Get(item.Key)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an external writer after the editor indexed the file.
	writeDoc(t, dir, "a.json", map[string]any{"id": "KB-1", "title": "Extern geändert", "auto_reply": true})
	doc["title"] = "Editor geändert"
	if _, _, err := s.Save(item.Key, doc); err == nil {
		t.Fatal("expected external-change conflict")
	}
}

func TestPreferredJSONFieldOrder(t *testing.T) {
	payload, err := marshalDocument(map[string]any{
		"language": "de-DE", "answer": "A", "id": "KB-1", "title": "T", "custom_z": 1, "custom_a": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(payload)
	wantOrder := []string{`"id"`, `"title"`, `"answer"`, `"language"`, `"custom_a"`, `"custom_z"`}
	last := -1
	for _, needle := range wantOrder {
		idx := strings.Index(s, needle)
		if idx <= last {
			t.Fatalf("field %s out of order in %s", needle, s)
		}
		last = idx
	}
}

func TestSearchRanksExactIdentifiersAndBuildsExcerpt(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "exact.json", map[string]any{
		"id": "KB-0x80070005", "title": "Fehler 0x80070005", "text": "Zugriff verweigert. Prüfe die Berechtigungen.",
		"keywords": []string{"0x80070005", "Access denied"},
	})
	writeDoc(t, dir, "answer.json", map[string]any{
		"id": "KB-2", "title": "Allgemeine Reparatur", "answer": "Diese Anleitung erwähnt 0x80070005 nur als Beispiel.",
	})
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	result := s.Search(Query{Q: "0x80070005", Page: 1, PageSize: 10})
	if result.Total != 2 {
		t.Fatalf("total=%d", result.Total)
	}
	if result.Items[0].ID != "KB-0x80070005" {
		t.Fatalf("unexpected ranking: %+v", result.Items)
	}
	if result.Items[0].Score <= result.Items[1].Score {
		t.Fatalf("expected first score to be higher: %+v", result.Items)
	}
	if !strings.Contains(strings.ToLower(result.Items[0].Excerpt), "zugriff") {
		t.Fatalf("unexpected excerpt: %q", result.Items[0].Excerpt)
	}
}

func TestImportDocumentCreatesNewFileAndRejectsDuplicateID(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"id": "KB-AI-STAGING-TEST-001", "title": "Reviewed", "answer": "Lösung",
		"auto_reply": false, "categories": []any{"AI-Staging"},
	}
	created, err := s.ImportDocument(doc, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "KB-AI-STAGING-TEST-001" || s.Count() != 1 {
		t.Fatalf("unexpected created item: %+v count=%d", created, s.Count())
	}
	if _, err := os.Stat(filepath.Join(dir, "KB-AI-STAGING-TEST-001.json")); err != nil {
		t.Fatalf("production file missing: %v", err)
	}
	if _, err := s.ImportDocument(doc, "fallback"); err == nil {
		t.Fatal("expected duplicate ID to be rejected")
	}
}
