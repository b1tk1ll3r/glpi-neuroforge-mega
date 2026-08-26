package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

func TestLoadSearchesOnlyAllowedSources(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	internal := `{"id":"I1","title":"VPN intern","text":"gateway vpn","answer":"x","source":"internal-kb","language":"de-DE","communication_style":"formal"}`
	vendor := `{"id":"V1","title":"VPN vendor","text":"gateway vpn","answer":"x","source":"vendor-docs","language":"de-DE","communication_style":"formal"}`
	if err := os.WriteFile(filepath.Join(dir, "internal.json"), []byte(internal), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor.json"), []byte(vendor), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(context.Background(), dir, data, nil, false, []string{"internal-kb"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 {
		t.Fatalf("count=%d", s.Count())
	}
	hits, err := s.Search(context.Background(), "vpn gateway", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Doc.Source != "internal-kb" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}

func TestLoadRequiresSourceMetadata(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"id":"I1","title":"Missing source"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), dir, data, nil, false, []string{"internal-kb"}); err == nil {
		t.Fatal("expected missing source to fail")
	}
}

func TestLoadMissingDirectoryReturnsHelpfulError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := Load(context.Background(), missing, t.TempDir(), nil, false, []string{"internal-kb"})
	if err == nil {
		t.Fatal("expected missing knowledge directory to fail")
	}
	if got := err.Error(); !strings.Contains(got, "read knowledge directory") || !strings.Contains(got, missing) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNilStoreHelpersDoNotPanic(t *testing.T) {
	var s *Store
	if got := s.Count(); got != 0 {
		t.Fatalf("Count()=%d, want 0", got)
	}
	if _, ok := s.ByID("KB1"); ok {
		t.Fatal("nil store unexpectedly returned a document")
	}
	if _, err := s.Search(context.Background(), "vpn", 1); err == nil {
		t.Fatal("expected Search on nil store to return an error")
	}
}

func TestRAGRequiresEmbedderWhenDocumentsExist(t *testing.T) {
	dir := t.TempDir()
	doc := `{"id":"I1","title":"VPN intern","text":"gateway vpn","answer":"x","source":"internal-kb","language":"de-DE","communication_style":"formal"}`
	if err := os.WriteFile(filepath.Join(dir, "internal.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), dir, t.TempDir(), nil, true, []string{"internal-kb"}); err == nil {
		t.Fatal("expected RAG without embedder to fail")
	}
}

func TestUpsertDelete(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	s, err := Load(context.Background(), dir, data, nil, false, []string{"internal-kb"})
	if err != nil {
		t.Fatal(err)
	}
	d := model.KnowledgeDoc{ID: "KB-1", Title: "Test", Text: "Wissen", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal", MinScore: 0.8}
	if err := s.Upsert(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 {
		t.Fatalf("count=%d", s.Count())
	}
	if _, ok := s.ByID("KB-1"); !ok {
		t.Fatal("missing")
	}
	if err := s.Delete("KB-1"); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Fatalf("count=%d", s.Count())
	}
}

func TestExternalKnowledgeIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	s, err := Load(context.Background(), dir, data, nil, false, []string{"internal-kb", "glpi-kb"})
	if err != nil {
		t.Fatal(err)
	}
	d := model.KnowledgeDoc{ID: "GLPI-KB-1", Title: "Extern", Text: "Wissen", Source: "glpi-kb", Language: "de-DE", CommunicationStyle: "formal"}
	if err := s.ReplaceExternalSource(context.Background(), "glpi-kb", []model.KnowledgeDoc{d}); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 || s.Origin("GLPI-KB-1") != "glpi-kb" {
		t.Fatalf("unexpected external store state")
	}
	if err := s.Upsert(context.Background(), d); err == nil {
		t.Fatal("expected external document to be read-only")
	}
}

type semanticTestEmbedder struct{}

func (semanticTestEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, text := range texts {
		s := strings.ToLower(text)
		v := []float64{0, 0, 0, 0}
		if strings.Contains(s, "benutzerkonto") || strings.Contains(s, "konto gesperrt") || strings.Contains(s, "gesperrt") {
			v[0] = 1
		}
		if strings.Contains(s, "anmeld") || strings.Contains(s, "login") || strings.Contains(s, "authent") {
			v[1] = 1
		}
		if strings.Contains(s, "drucker") {
			v[2] = 1
		}
		if strings.Contains(s, "allgemein") || strings.Contains(s, "hinweis") {
			v[3] = 1
		}
		if v[0]+v[1]+v[2]+v[3] == 0 {
			v[3] = .1
		}
		out[i] = v
	}
	return out, nil
}

func TestHybridScoringUsesChunksTitleKeywordsAndCategoryHints(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	body := strings.Repeat("Allgemeine technische Hinweise ohne Bezug zum Benutzer. ", 80) +
		" Wenn ein Benutzerkonto gesperrt ist und die Anmeldung nicht möglich ist, muss die Kontosperre geprüft werden. " +
		strings.Repeat("Weitere allgemeine Hinweise. ", 80)
	doc := model.KnowledgeDoc{ID: "KB-AD-1", Title: "Benutzerkonto gesperrt", Text: body, Answer: "x", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal", Categories: []int64{2}, Keywords: []string{"Konto gesperrt", "Anmeldung", "Login"}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "ad.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(context.Background(), dir, data, semanticTestEmbedder{}, true, []string{"internal-kb"}, ScoringConfig{SemanticWeight: .5, TitleWeight: .25, KeywordWeight: .15, CategoryWeight: .10, ChunkWords: 40, ChunkOverlap: 10, MaxChunksPerDoc: 24})
	if err != nil {
		t.Fatal(err)
	}
	cats := []model.Category{{ID: 2, Name: "Active Directory", Hints: []string{"Benutzerkonto gesperrt", "Anmeldung Login Authentifizierung"}, Examples: []string{"Mein Benutzerkonto ist gesperrt und ich kann mich nicht anmelden"}}}
	hits, err := s.Search(context.Background(), "Benutzerkonto gesperrt, Anmeldung nicht möglich", 1, cats)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits=%d", len(hits))
	}
	h := hits[0]
	if h.Score < .75 {
		t.Fatalf("hybrid score too low: %+v", h)
	}
	if h.SemanticScore < .8 {
		t.Fatalf("expected strong best-chunk semantic score: %+v", h)
	}
	if h.TitleScore < .7 {
		t.Fatalf("expected strong title score: %+v", h)
	}
	if h.KeywordScore <= 0 || h.CategoryScore <= 0 {
		t.Fatalf("expected keyword/category contributions: %+v", h)
	}
	if !strings.Contains(strings.ToLower(h.BestChunkExcerpt), "benutzerkonto") {
		t.Fatalf("wrong best chunk: %q", h.BestChunkExcerpt)
	}
}

type hashTestEmbedder struct{}

func (hashTestEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, text := range texts {
		// Deterministic fixed-width vector: identical text -> identical vector;
		// unrelated text is unlikely to point in the same direction.
		v := make([]float64, 128)
		for pos, r := range []byte(strings.ToLower(strings.Join(strings.Fields(text), " "))) {
			idx := (int(r) + pos*31) % len(v)
			if (int(r)+pos)%2 == 0 {
				v[idx] += 1
			} else {
				v[idx] -= 1
			}
		}
		out[i] = v
	}
	return out, nil
}

func TestLongQueryIsChunkedAndCanMatchIdenticalKnowledgeSection(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	parts := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		parts = append(parts, "Benutzerkonto Anmeldung Sperrung Active Directory Diagnose Schritt")
	}
	body := strings.Join(parts, " ")
	doc := model.KnowledgeDoc{ID: "KB-LONG", Title: "Benutzerkonto gesperrt", Text: body, Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal"}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "long.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(context.Background(), dir, data, hashTestEmbedder{}, true, []string{"internal-kb"}, ScoringConfig{SemanticWeight: 1, ChunkWords: 80, ChunkOverlap: 20, MaxChunksPerDoc: 24})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(context.Background(), "Benutzerkonto gesperrt\n"+body, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits=%d", len(hits))
	}
	h := hits[0]
	if h.QueryChunkCount <= 1 || h.DocumentChunkCount <= 1 {
		t.Fatalf("expected both sides to be chunked: %+v", h)
	}
	if h.SemanticScore < 0.999999 {
		t.Fatalf("identical long body should contain an exact chunk match, got semantic=%f", h.SemanticScore)
	}
	if h.Score < 0.999999 {
		t.Fatalf("semantic-only hybrid should be ~1, got %f", h.Score)
	}
}

func TestRegressionAnmeldeproblemZeroMetadataDoesNotDragScoreBelowThreshold(t *testing.T) {
	cfg := DefaultScoringConfig()
	title := titleSimilarity("Anmeldeproblem", "Benutzeranmeldung, Anmeldeprobleme, Passwort vergessen")
	if title < .90 {
		t.Fatalf("expected strong fuzzy/asymmetric title match, got %f", title)
	}
	lex := title
	// Raw cosine copied from a real-world regression case. Keyword/category
	// metadata had zero overlap and must therefore not count as negative evidence.
	total := weightedScore(cfg,
		scorePart{.5025152998747112, cfg.SemanticWeight, true},
		scorePart{title, cfg.TitleWeight, true},
		scorePart{lex, cfg.LexicalWeight, true},
		scorePart{0, cfg.KeywordWeight, false},
		scorePart{0, cfg.CategoryWeight, false},
	)
	if total < .70 {
		t.Fatalf("obvious login KB regression should clear 0.70 evidence threshold, got %f (title=%f)", total, title)
	}
}

func TestEmbeddingGemmaRetrievalPrompts(t *testing.T) {
	q := formatQueryEmbeddings([]string{"Seit heute funktioniert die Anmeldung nicht"}, "embeddinggemma")
	if len(q) != 1 || !strings.HasPrefix(q[0], "task: search result | query: ") {
		t.Fatalf("unexpected query prompt: %#v", q)
	}
	d := formatDocumentEmbedding("Benutzeranmeldung", "Bei unbekanntem Benutzer LDAP prüfen", "embeddinggemma")
	if !strings.HasPrefix(d, "title: Benutzeranmeldung | text: ") {
		t.Fatalf("unexpected document prompt: %q", d)
	}
	if got := ResolveEmbeddingProfile("auto", "embeddinggemma:latest"); got != "embeddinggemma" {
		t.Fatalf("resolved profile=%q", got)
	}
	if got := ResolveEmbeddingProfile("auto", "qwen3-embedding:0.6b"); got != "plain" {
		t.Fatalf("resolved non-gemma profile=%q", got)
	}
}

func TestRegressionShortGermanLoginTicketGetsStrongLexicalEvidence(t *testing.T) {
	d := model.KnowledgeDoc{
		Title: "Benutzeranmeldung, Anmeldeprobleme, Passwort vergessen",
		Text:  "Bei Problemen mit der Benutzeranmeldung und Domänenkonten prüfen Sie Active Directory. Ein unbekannter Benutzer kann auf ein Anmelde- oder Synchronisationsproblem hinweisen.",
	}
	got := lexicalSimilarity("Problem mit Nutzerkonto\nKann mich nicht anmelden", d)
	if got < .55 {
		t.Fatalf("short German login request should have useful lexical evidence, got %f", got)
	}
	if sim := tokenSimilarity("anmelden", "Benutzeranmeldung"); sim < .85 {
		t.Fatalf("anmelden/Benutzeranmeldung should match through a German support stem, got %f", sim)
	}
}

func TestExternalStringCategoryLoadsUnscoped(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	body := `{"id":"KB-EXT-1","title":"Docker Test","text":"Docker Fehler","answer":"Pruefen","auto_reply":false,"min_score":0.7,"categories":["Docker","Security"],"keywords":["docker"],"source":"internal-kb","language":"de-DE","communication_style":"formal"}`
	if err := os.WriteFile(filepath.Join(dir, "ext.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(context.Background(), dir, data, nil, false, []string{"internal-kb"}, ScoringConfig{CategoryMode: "unscoped"})
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := s.ByID("KB-EXT-1")
	if !ok {
		t.Fatal("document not loaded")
	}
	if len(doc.Categories) != 0 {
		t.Fatalf("expected no GLPI ids, got %v", doc.Categories)
	}
	if len(doc.ExternalCategories) != 2 {
		t.Fatalf("external categories=%v", doc.ExternalCategories)
	}
	stats := s.LoadStats()
	if stats.UnmappedCategoryFiles != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestExternalStringCategoryMapsToGLPI(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	mapPath := filepath.Join(data, "category-map.json")
	if err := os.WriteFile(mapPath, []byte(`{"Docker":[12,13],"Security":7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"KB-EXT-2","title":"Docker Test","text":"Docker Fehler","answer":"Pruefen","auto_reply":false,"min_score":0.7,"categories":["Docker","Security"],"keywords":["docker"],"source":"internal-kb","language":"de-DE","communication_style":"formal"}`
	if err := os.WriteFile(filepath.Join(dir, "ext.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(context.Background(), dir, data, nil, false, []string{"internal-kb"}, ScoringConfig{CategoryMode: "unscoped", CategoryMapFile: mapPath})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := s.ByID("KB-EXT-2")
	want := []int64{12, 13, 7}
	if len(doc.Categories) != len(want) {
		t.Fatalf("categories=%v", doc.Categories)
	}
	for _, id := range want {
		found := false
		for _, got := range doc.Categories {
			if got == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing id %d in %v", id, doc.Categories)
		}
	}
	if s.LoadStats().UnmappedCategoryFiles != 0 {
		t.Fatalf("unexpected unmapped stats: %+v", s.LoadStats())
	}
}

func TestKnowledgeIgnoreGlobs(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	bad := `{"id":"KB-BAD","title":"Foreign","text":"x","answer":"x","auto_reply":false,"min_score":0.7,"categories":[{"unsupported":true}],"source":"internal-kb","language":"de-DE","communication_style":"formal"}`
	if err := os.WriteFile(filepath.Join(dir, "KB-SEC-ATTCK-AN-0001.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(context.Background(), dir, data, nil, false, []string{"internal-kb"}, ScoringConfig{CategoryMode: "strict", IgnoreGlobs: []string{"KB-SEC-ATTCK-*.json"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Fatalf("count=%d", s.Count())
	}
	if s.LoadStats().IgnoredFiles != 1 {
		t.Fatalf("stats=%+v", s.LoadStats())
	}
}

func TestNewStoreSupportsBackgroundInitialization(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	for i := 0; i < 250; i++ {
		doc := model.KnowledgeDoc{ID: fmt.Sprintf("KB-%04d", i), Title: fmt.Sprintf("Artikel %d", i), Text: "Testwissen Anmeldung", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal"}
		b, _ := json.Marshal(doc)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("kb-%04d.json", i)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewStore(dir, data, nil, false, []string{"internal-kb"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Ready() {
		t.Fatal("new store must not be ready before Initialize")
	}
	if st := s.InitStatus(); st.State != "waiting" {
		t.Fatalf("state=%q want waiting", st.State)
	}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := s.InitStatus()
	if !s.Ready() || st.State != "ready" || st.ProcessedFiles != 250 || st.LoadedDocs != 250 || s.Count() != 250 {
		t.Fatalf("unexpected init status: %+v count=%d", st, s.Count())
	}
}

type countingEmbedder struct{ calls int }

func (e *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	e.calls++
	out := make([][]float64, len(texts))
	for i, text := range texts {
		v := make([]float64, 8)
		for j, b := range []byte(strings.ToLower(text)) {
			v[(int(b)+j)%len(v)] += 1
		}
		out[i] = v
	}
	return out, nil
}

func TestPersistentIndexLoadsWithoutReembeddingAndSyncsDelta(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	path := filepath.Join(dir, "kb.json")
	writeDoc := func(title, text string) {
		t.Helper()
		d := model.KnowledgeDoc{ID: "KB-1", Title: title, Text: text, Answer: "Antwort", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal"}
		b, _ := json.Marshal(d)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeDoc("Anmeldung", "Benutzer kann sich nicht anmelden")
	cfg := ScoringConfig{EmbeddingIdentity: "test-embed", EmbeddingProfile: "plain", ChunkWords: 40, ChunkOverlap: 10, MaxChunksPerDoc: 8, MaxQueryChunks: 8, IndexMode: "incremental", EmbedBatchSize: 8}

	firstEmbed := &countingEmbedder{}
	first, err := Load(context.Background(), dir, data, firstEmbed, true, []string{"internal-kb"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if firstEmbed.calls == 0 {
		t.Fatal("expected initial embedding calls")
	}
	if _, err := os.Stat(filepath.Join(data, "knowledge-index", "snapshot.gob")); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	if !first.Ready() {
		t.Fatal("first store not ready")
	}

	secondEmbed := &countingEmbedder{}
	second, err := Load(context.Background(), dir, data, secondEmbed, true, []string{"internal-kb"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if secondEmbed.calls != 0 {
		t.Fatalf("snapshot startup unexpectedly re-embedded: calls=%d", secondEmbed.calls)
	}
	if !second.InitStatus().SnapshotLoaded {
		t.Fatal("expected persistent snapshot to be loaded")
	}
	if second.Count() != 1 {
		t.Fatalf("count=%d", second.Count())
	}

	// Ensure mtime changes even on filesystems with coarse timestamp resolution.
	time.Sleep(20 * time.Millisecond)
	writeDoc("Anmeldung geändert", "Benutzer kann sich weiterhin nicht anmelden")
	if err := second.SyncLocal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if secondEmbed.calls == 0 {
		t.Fatal("expected changed document to be re-embedded")
	}
	got, ok := second.ByID("KB-1")
	if !ok || got.Title != "Anmeldung geändert" {
		t.Fatalf("delta update not applied: %+v", got)
	}
	callsAfterChange := secondEmbed.calls
	if err := second.SyncLocal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if secondEmbed.calls != callsAfterChange {
		t.Fatalf("unchanged delta scan re-embedded document: before=%d after=%d", callsAfterChange, secondEmbed.calls)
	}
}

func TestReadonlyIndexRequiresSnapshot(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	s, err := NewStore(dir, data, nil, false, []string{"internal-kb"}, ScoringConfig{IndexMode: "readonly"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(context.Background()); err == nil {
		t.Fatal("expected readonly mode without snapshot to fail")
	}
}

func TestCategoryMappingEditorReappliesWithoutReembedding(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	mapPath := filepath.Join(data, "knowledge-category-map.json")
	doc := `{
  "id":"KB-MAP-1",
  "title":"Outlook Signatur",
  "text":"Signatur kann nicht angelegt werden",
  "answer":"Bitte Einstellungen prüfen.",
  "auto_reply":true,
  "min_score":0.7,
  "categories":["Outlook","Signatur"],
  "keywords":["Outlook","Signatur"],
  "source":"internal-kb",
  "language":"de-DE",
  "communication_style":"formal"
}`
	if err := os.WriteFile(filepath.Join(dir, "kb.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ScoringConfig{
		EmbeddingIdentity: "test-embed", EmbeddingProfile: "plain",
		ChunkWords: 40, ChunkOverlap: 10, MaxChunksPerDoc: 8, MaxQueryChunks: 8,
		IndexMode: "incremental", EmbedBatchSize: 8,
		CategoryMode: "unscoped", CategoryMapFile: mapPath,
	}
	embed := &countingEmbedder{}
	s, err := Load(context.Background(), dir, data, embed, true, []string{"internal-kb"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if embed.calls == 0 {
		t.Fatal("expected initial embeddings")
	}
	beforeCalls := embed.calls
	before, ok := s.ByID("KB-MAP-1")
	if !ok {
		t.Fatal("knowledge document missing")
	}
	if before.AutoReply {
		t.Fatal("unmapped external categories should make auto_reply fail closed")
	}
	if len(before.UnmappedExternalCategories) != 2 {
		t.Fatalf("unmapped=%v", before.UnmappedExternalCategories)
	}

	state, err := s.CategoryMappings()
	if err != nil {
		t.Fatal(err)
	}
	if state.ObservedCount != 2 || state.UnmappedObserved != 2 {
		t.Fatalf("unexpected mapping state before save: %+v", state)
	}

	if err := s.SaveCategoryMappings(context.Background(), map[string][]int64{
		"Outlook":  {12},
		"Signatur": {12, 18},
	}); err != nil {
		t.Fatal(err)
	}
	if embed.calls != beforeCalls {
		t.Fatalf("category remap unexpectedly re-embedded: before=%d after=%d", beforeCalls, embed.calls)
	}
	after, ok := s.ByID("KB-MAP-1")
	if !ok {
		t.Fatal("knowledge document missing after remap")
	}
	if !after.AutoReply {
		t.Fatal("fully mapped document should restore auto_reply from source JSON")
	}
	if len(after.UnmappedExternalCategories) != 0 {
		t.Fatalf("still unmapped: %v", after.UnmappedExternalCategories)
	}
	if got := fmt.Sprint(after.Categories); got != "[12 18]" {
		t.Fatalf("mapped categories=%s", got)
	}
	b, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"Outlook": 12`) || !strings.Contains(string(b), `"Signatur": [12,18]`) {
		t.Fatalf("unexpected mapping file:\n%s", b)
	}

	state, err = s.CategoryMappings()
	if err != nil {
		t.Fatal(err)
	}
	if state.MappedObserved != 2 || state.UnmappedObserved != 0 {
		t.Fatalf("unexpected mapping state after save: %+v", state)
	}
}

func TestFilterHitsBySourcesSeparatesCategoryOnlyKnowledge(t *testing.T) {
	hits := []model.KnowledgeHit{
		{Doc: model.KnowledgeDoc{ID: "CAT", Source: "internal-category"}, Score: .9},
		{Doc: model.KnowledgeDoc{ID: "REPLY", Source: "internal-kb"}, Score: .8},
	}
	category := FilterHitsBySources(hits, []string{"internal-category"}, 0)
	reply := FilterHitsBySources(hits, []string{"internal-kb"}, 0)
	if len(category) != 1 || category[0].Doc.ID != "CAT" {
		t.Fatalf("category hits=%+v", category)
	}
	if len(reply) != 1 || reply[0].Doc.ID != "REPLY" {
		t.Fatalf("reply hits=%+v", reply)
	}
}

type semanticBackendStub struct {
	hits      []SemanticHit
	searchErr error
	searches  int
	upserts   int
	deletes   int
}

func (b *semanticBackendStub) Name() string { return "stub" }
func (b *semanticBackendStub) UpsertDocument(context.Context, model.KnowledgeDoc, []string, [][]float64) error {
	b.upserts++
	return nil
}
func (b *semanticBackendStub) DeleteDocument(context.Context, string) error {
	b.deletes++
	return nil
}
func (b *semanticBackendStub) Search(context.Context, []float64, int) ([]SemanticHit, error) {
	b.searches++
	if b.searchErr != nil {
		return nil, b.searchErr
	}
	return append([]SemanticHit(nil), b.hits...), nil
}
func (b *semanticBackendStub) Health(context.Context) error { return nil }

func TestNeuroForgeSemanticBackendIsEvidenceNotPolicy(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	doc := model.KnowledgeDoc{ID: "KB-REMOTE", Title: "VPN Zugang", Text: "VPN Gateway und Token prüfen", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal"}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "remote.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ScoringConfig{SemanticWeight: 1, ChunkWords: 80, ChunkOverlap: 20, MaxChunksPerDoc: 8, MaxQueryChunks: 4}
	s, err := Load(context.Background(), dir, data, hashTestEmbedder{}, true, []string{"internal-kb"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	backend := &semanticBackendStub{hits: []SemanticHit{{DocumentID: doc.ID, ChunkIndex: 0, Text: "remote canonical chunk", Similarity: .83}}}
	if err := s.SetSemanticBackend(backend, "neuroforge", 32, false); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(context.Background(), "VPN Zugang\nToken prüfen", 1)
	if err != nil {
		t.Fatal(err)
	}
	if backend.searches == 0 || len(hits) != 1 {
		t.Fatalf("backend searches=%d hits=%d", backend.searches, len(hits))
	}
	if hits[0].Doc.ID != doc.ID || hits[0].SemanticScore != .83 {
		t.Fatalf("remote semantic evidence was not preserved: %+v", hits[0])
	}
	if hits[0].BestChunkExcerpt != "remote canonical chunk" {
		t.Fatalf("unexpected semantic chunk: %q", hits[0].BestChunkExcerpt)
	}
}

func TestNeuroForgeSearchFailureHonorsFailOpenPolicy(t *testing.T) {
	dir := t.TempDir()
	data := t.TempDir()
	doc := model.KnowledgeDoc{ID: "KB-FALLBACK", Title: "Anmeldung", Text: "Anmeldung Passwort Benutzerkonto", Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal"}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "fallback.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(context.Background(), dir, data, hashTestEmbedder{}, true, []string{"internal-kb"}, ScoringConfig{SemanticWeight: 1, ChunkWords: 80, ChunkOverlap: 20, MaxChunksPerDoc: 8, MaxQueryChunks: 4})
	if err != nil {
		t.Fatal(err)
	}
	backend := &semanticBackendStub{searchErr: errors.New("backend unavailable")}
	if err := s.SetSemanticBackend(backend, "neuroforge", 32, true); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(context.Background(), "Anmeldung Passwort", 1)
	if err != nil {
		t.Fatalf("fail-open search should use local evidence: %v", err)
	}
	if len(hits) != 1 || hits[0].SemanticScore <= 0 {
		t.Fatalf("expected local semantic fallback, hits=%+v", hits)
	}

	if err := s.SetSemanticBackend(backend, "neuroforge", 32, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(context.Background(), "Anmeldung Passwort", 1); err == nil {
		t.Fatal("fail-closed search must surface backend failure")
	}
}
