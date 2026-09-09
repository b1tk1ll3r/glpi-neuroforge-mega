package glpikb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/metrics"
	"github.com/example/glpi-ai-agent/internal/model"
)

type fakeSource struct{}

func (fakeSource) DiscoverKnowledgeBasePath(context.Context, string) (string, error) {
	return "/Knowledge/KnowbaseItem", nil
}
func (fakeSource) ListKnowledgeBaseItems(context.Context, string, int, string) ([]model.GLPIKnowledgeItem, error) {
	return []model.GLPIKnowledgeItem{{ID: 12, Title: "Benutzerkonto gesperrt", Content: "<p>Bitte entsperren.</p>", CategoryIDs: []int64{9}, ModifiedAt: "now"}}, nil
}
func (fakeSource) GetCategories(context.Context) ([]model.Category, error) {
	return []model.Category{{ID: 2, Name: "Active Directory", KnowbaseCategoryID: 9}}, nil
}

type fakeStore struct{ docs []model.KnowledgeDoc }

func (f *fakeStore) ReplaceExternalSource(_ context.Context, _ string, d []model.KnowledgeDoc) error {
	f.docs = append([]model.KnowledgeDoc(nil), d...)
	return nil
}
func (f *fakeStore) Count() int { return len(f.docs) }

func TestSyncMapsGLPIKBAndAutoReplyWhitelist(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), GLPIKBEnabled: true, GLPIKBPath: "auto", GLPIKBLimit: 50, GLPIKBSyncInterval: time.Minute, GLPIKBSource: "glpi-kb", GLPIKBAutoReply: true, GLPIKBAutoReplyCategoryIDs: []int64{9}, CommunicationLanguage: "de-DE", CommunicationStyle: "formal", GLPITimeout: time.Second}
	st := &fakeStore{}
	m := metrics.New()
	s := New(cfg, fakeSource{}, st, m)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 1 {
		t.Fatalf("docs=%d", len(st.docs))
	}
	d := st.docs[0]
	if d.ID != "GLPI-KB-12" || !d.AutoReply || len(d.Categories) != 1 || d.Categories[0] != 2 || d.Text != "Bitte entsperren." || d.AnswerHTML != "<p>Bitte entsperren.</p>" {
		t.Fatalf("unexpected doc: %+v", d)
	}
	if d.AutoReplyDecision != "glpi_kb_auto_reply_approved" || d.AutoReplyDetail == "" {
		t.Fatalf("expected auditable approval reason, got decision=%q detail=%q", d.AutoReplyDecision, d.AutoReplyDetail)
	}
	if _, err := filepath.Abs(cfg.DataDir); err != nil {
		t.Fatal(err)
	}
}

func TestSyncDoesNotUseMappedITILCategoryForAutoReplyApproval(t *testing.T) {
	cfg := config.Config{
		DataDir: t.TempDir(), GLPIKBEnabled: true, GLPIKBPath: "auto", GLPIKBLimit: 50,
		GLPIKBSyncInterval: time.Minute, GLPIKBSource: "glpi-kb", GLPIKBAutoReply: true,
		GLPIKBAutoReplyITILCategoryIDs: []int64{2}, CommunicationLanguage: "de-DE",
		CommunicationStyle: "formal", GLPITimeout: time.Second,
	}
	st := &fakeStore{}
	s := New(cfg, fakeSource{}, st, metrics.New())
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 1 || st.docs[0].AutoReply {
		t.Fatalf("mapped ITIL category must not approve auto reply: %+v", st.docs)
	}
	if got := st.docs[0].AutoReplyDecision; got != "glpi_kb_auto_reply_whitelist_empty" {
		t.Fatalf("decision=%q", got)
	}
}

func TestSyncExplainsBlockedAutoReply(t *testing.T) {
	cfg := config.Config{
		DataDir: t.TempDir(), GLPIKBEnabled: true, GLPIKBPath: "auto", GLPIKBLimit: 50,
		GLPIKBSyncInterval: time.Minute, GLPIKBSource: "glpi-kb", GLPIKBAutoReply: true,
		GLPIKBAutoReplyCategoryIDs: []int64{99}, CommunicationLanguage: "de-DE",
		CommunicationStyle: "formal", GLPITimeout: time.Second,
	}
	st := &fakeStore{}
	s := New(cfg, fakeSource{}, st, metrics.New())
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 1 || st.docs[0].AutoReply {
		t.Fatalf("non-matching whitelist must keep auto reply disabled: %+v", st.docs)
	}
	if got := st.docs[0].AutoReplyDecision; got != "glpi_kb_category_not_whitelisted" {
		t.Fatalf("decision=%q", got)
	}
	if got := st.docs[0].AutoReplyDetail; !strings.Contains(got, "GLPI-KB-Kategorien: [9]") || !strings.Contains(got, "freigegebene GLPI-KB-Kategorien: [99]") {
		t.Fatalf("detail should expose source and configured IDs, got %q", got)
	}
}

type categoryUnavailableSource struct{ fakeSource }

func (categoryUnavailableSource) GetCategories(context.Context) ([]model.Category, error) {
	return nil, errors.New("category endpoint unavailable")
}

func TestSyncApprovesByKBCategoryWhenITILCategoriesAreUnavailable(t *testing.T) {
	cfg := config.Config{
		DataDir: t.TempDir(), GLPIKBEnabled: true, GLPIKBPath: "auto", GLPIKBLimit: 50,
		GLPIKBSyncInterval: time.Minute, GLPIKBSource: "glpi-kb", GLPIKBAutoReply: true,
		GLPIKBAutoReplyCategoryIDs: []int64{9}, CommunicationLanguage: "de-DE",
		CommunicationStyle: "formal", GLPITimeout: time.Second,
	}
	st := &fakeStore{}
	s := New(cfg, categoryUnavailableSource{}, st, metrics.New())
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 1 || !st.docs[0].AutoReply {
		t.Fatalf("KB category approval must not depend on ITIL category loading: %+v", st.docs)
	}
	if len(st.docs[0].Categories) != 0 {
		t.Fatalf("optional ITIL mapping should be absent after category endpoint failure: %v", st.docs[0].Categories)
	}
}

func TestLoadCacheIgnoresOlderAutoReplyPolicy(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, GLPIKBSource: "glpi-kb"}
	legacy := cacheFile{
		PolicyVersion: 0,
		SyncedAt:      time.Now(),
		Path:          "/Knowledge/KnowbaseItem",
		Documents: []model.KnowledgeDoc{{
			ID: "GLPI-KB-1", Title: "Legacy", AutoReply: true, Source: "glpi-kb",
		}},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "glpi-kb-cache.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	st := &fakeStore{}
	s := New(cfg, fakeSource{}, st, metrics.New())
	if err := s.LoadCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 0 {
		t.Fatalf("legacy approval cache must be ignored: %+v", st.docs)
	}
}

type uncategorizedSource struct{}

func TestLoadCacheIgnoresChangedAutoReplyApprovalConfig(t *testing.T) {
	dir := t.TempDir()
	oldCfg := config.Config{
		DataDir: dir, GLPIKBSource: "glpi-kb", GLPIKBAutoReply: true,
		GLPIKBAutoReplyCategoryIDs: []int64{9},
	}
	cf := cacheFile{
		PolicyVersion: cachePolicyVersion,
		ApprovalHash:  approvalConfigHash(oldCfg),
		SyncedAt:      time.Now(),
		Path:          "/Knowledge/KnowbaseItem",
		Documents: []model.KnowledgeDoc{{
			ID: "GLPI-KB-1", Title: "Old config", AutoReply: true, Source: "glpi-kb",
		}},
	}
	b, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "glpi-kb-cache.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	newCfg := oldCfg
	newCfg.GLPIKBAutoReplyCategoryIDs = []int64{10}
	st := &fakeStore{}
	s := New(newCfg, fakeSource{}, st, metrics.New())
	if err := s.LoadCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 0 {
		t.Fatalf("cache approved under a different allowlist must be ignored: %+v", st.docs)
	}
}

func (uncategorizedSource) DiscoverKnowledgeBasePath(context.Context, string) (string, error) {
	return "/Knowledge/KnowbaseItem", nil
}
func (uncategorizedSource) ListKnowledgeBaseItems(context.Context, string, int, string) ([]model.GLPIKnowledgeItem, error) {
	return []model.GLPIKnowledgeItem{{ID: 21, Title: "Allgemeine Druckerhilfe", Content: "<p>Drucker neu verbinden.</p>", CategoryIDs: nil, ModifiedAt: "now"}}, nil
}
func (uncategorizedSource) GetCategories(context.Context) ([]model.Category, error) {
	return []model.Category{{ID: 67, Name: "Arbeitsplatzdrucker"}, {ID: 68, Name: "Multifunktionsgerät"}}, nil
}

func TestSyncConditionallyApprovesUncategorizedArticle(t *testing.T) {
	cfg := config.Config{
		DataDir: t.TempDir(), GLPIKBEnabled: true, GLPIKBPath: "auto", GLPIKBLimit: 50,
		GLPIKBSyncInterval: time.Minute, GLPIKBSource: "glpi-kb", GLPIKBAutoReply: true,
		GLPIKBAutoReplyAllowUncategorized:      true,
		GLPIKBAutoReplyUncategorizedArticleIDs: []int64{21},
		CommunicationLanguage:                  "de-DE", CommunicationStyle: "formal", GLPITimeout: time.Second,
	}
	st := &fakeStore{}
	s := New(cfg, uncategorizedSource{}, st, metrics.New())
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 1 {
		t.Fatalf("docs=%d", len(st.docs))
	}
	d := st.docs[0]
	if !d.AutoReply || d.AutoReplyDecision != "glpi_kb_uncategorized_article_approved" {
		t.Fatalf("uncategorized article should be explicitly approved: %+v", d)
	}
	if got := strings.TrimSpace(d.AutoReplyDetail); !strings.Contains(got, "konkrete GLPI-Artikel-ID") {
		t.Fatalf("detail should explain article-ID approval, got %q", got)
	}
	if len(d.Categories) != 0 {
		t.Fatalf("uncategorized article must not pretend to have semantic ITIL categories: %v", d.Categories)
	}
}

func TestSyncKeepsUncategorizedArticleBlockedByDefault(t *testing.T) {
	cfg := config.Config{
		DataDir: t.TempDir(), GLPIKBEnabled: true, GLPIKBPath: "auto", GLPIKBLimit: 50,
		GLPIKBSyncInterval: time.Minute, GLPIKBSource: "glpi-kb", GLPIKBAutoReply: true,
		CommunicationLanguage: "de-DE", CommunicationStyle: "formal", GLPITimeout: time.Second,
	}
	st := &fakeStore{}
	s := New(cfg, uncategorizedSource{}, st, metrics.New())
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 1 || st.docs[0].AutoReply {
		t.Fatalf("uncategorized article must remain blocked without explicit opt-in: %+v", st.docs)
	}
	if got := st.docs[0].AutoReplyDecision; got != "glpi_kb_article_without_category" {
		t.Fatalf("decision=%q", got)
	}
}

type linkedFakeSource struct{ fakeSource }

func (linkedFakeSource) ListKnowledgeBaseLinkedItems(context.Context, []int64, int) (map[int64][]model.LinkedItem, error) {
	return map[int64][]model.LinkedItem{12: {{ItemType: "Computer", ID: 42, Name: "NB-042"}}}, nil
}

func TestSyncPreservesGLPIKnowledgeLinkedItems(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), GLPIKBEnabled: true, GLPIKBPath: "auto", GLPIKBLimit: 50, GLPIKBSyncInterval: time.Minute, GLPIKBSource: "glpi-kb", CommunicationLanguage: "de-DE", CommunicationStyle: "formal", GLPITimeout: time.Second}
	st := &fakeStore{}
	s := New(cfg, linkedFakeSource{}, st, metrics.New())
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.docs) != 1 || len(st.docs[0].LinkedItems) != 1 {
		t.Fatalf("linked items lost during sync: %#v", st.docs)
	}
	got := st.docs[0].LinkedItems[0]
	if got.ItemType != "Computer" || got.ID != 42 || got.Name != "NB-042" {
		t.Fatalf("unexpected linked item: %#v", got)
	}
}

func TestSyncTimeoutUsesDedicatedKnowledgeBudget(t *testing.T) {
	if got := SyncTimeout(config.Config{}); got != 30*time.Minute {
		t.Fatalf("default sync timeout=%s, want 30m", got)
	}
	if got := SyncTimeout(config.Config{GLPIKBSyncTimeout: 7 * time.Minute}); got != 7*time.Minute {
		t.Fatalf("configured sync timeout=%s, want 7m", got)
	}
}
