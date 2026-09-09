package glpikb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/metrics"
	"github.com/example/glpi-ai-agent/internal/model"
)

type Source interface {
	DiscoverKnowledgeBasePath(context.Context, string) (string, error)
	ListKnowledgeBaseItems(context.Context, string, int, string) ([]model.GLPIKnowledgeItem, error)
	GetCategories(context.Context) ([]model.Category, error)
}

type LinkedItemSource interface {
	ListKnowledgeBaseLinkedItems(context.Context, []int64, int) (map[int64][]model.LinkedItem, error)
}

type Store interface {
	ReplaceExternalSource(context.Context, string, []model.KnowledgeDoc) error
	Count() int
}

type Syncer struct {
	cfg       config.Config
	glpi      Source
	store     Store
	metrics   *metrics.Metrics
	cachePath string

	mu           sync.RWMutex
	resolvedPath string
	lastSync     time.Time
	lastError    string
	count        int
}

const (
	cachePolicyVersion        = 2
	defaultKnowledgeSyncLimit = 30 * time.Minute
)

// SyncTimeout is the total budget for one complete GLPI knowledge refresh,
// including embedding/re-indexing. GLPI_TIMEOUT remains the per-request HTTP
// timeout; using it as the total sync budget aborts larger knowledge bases long
// before their embeddings can finish.
func SyncTimeout(cfg config.Config) time.Duration {
	if cfg.GLPIKBSyncTimeout > 0 {
		return cfg.GLPIKBSyncTimeout
	}
	return defaultKnowledgeSyncLimit
}

type cacheFile struct {
	PolicyVersion int                  `json:"policy_version"`
	ApprovalHash  string               `json:"approval_hash"`
	SyncedAt      time.Time            `json:"synced_at"`
	Path          string               `json:"path"`
	Documents     []model.KnowledgeDoc `json:"documents"`
}

type Status struct {
	Enabled   bool      `json:"enabled"`
	Path      string    `json:"path,omitempty"`
	LastSync  time.Time `json:"last_sync,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	Documents int       `json:"documents"`
}

func New(cfg config.Config, g Source, store Store, m *metrics.Metrics) *Syncer {
	if len(cfg.GLPIKBAutoReplyITILCategoryIDs) > 0 {
		slog.Warn(
			"GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS is deprecated and ignored",
			"ids", cfg.GLPIKBAutoReplyITILCategoryIDs,
			"hint", "approve categorized articles with GLPI_KB_AUTO_REPLY_CATEGORY_IDS or uncategorized articles with GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS",
		)
	}
	return &Syncer{cfg: cfg, glpi: g, store: store, metrics: m, cachePath: filepath.Join(cfg.DataDir, "glpi-kb-cache.json")}
}

func (s *Syncer) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Status{Enabled: s.cfg.GLPIKBEnabled, Path: s.resolvedPath, LastSync: s.lastSync, LastError: s.lastError, Documents: s.count}
}

func (s *Syncer) LoadCache(ctx context.Context) error {
	b, err := os.ReadFile(s.cachePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read GLPI KB cache: %w", err)
	}
	var cf cacheFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return fmt.Errorf("decode GLPI KB cache: %w", err)
	}
	if cf.PolicyVersion != cachePolicyVersion {
		slog.Warn(
			"ignoring GLPI knowledge cache created with an older Auto-Reply policy",
			"path", s.cachePath,
			"cache_policy_version", cf.PolicyVersion,
			"required_policy_version", cachePolicyVersion,
		)
		return nil
	}
	currentApprovalHash := approvalConfigHash(s.cfg)
	if cf.ApprovalHash == "" || cf.ApprovalHash != currentApprovalHash {
		slog.Warn(
			"ignoring GLPI knowledge cache because Auto-Reply approval configuration changed",
			"path", s.cachePath,
		)
		return nil
	}
	if err := s.store.ReplaceExternalSource(ctx, s.cfg.GLPIKBSource, cf.Documents); err != nil {
		return fmt.Errorf("load GLPI KB cache into knowledge store: %w", err)
	}
	s.mu.Lock()
	s.resolvedPath = cf.Path
	s.lastSync = cf.SyncedAt
	s.count = len(cf.Documents)
	s.lastError = ""
	s.mu.Unlock()
	s.metrics.SetGLPIKBStatus(true, len(cf.Documents), cf.SyncedAt, "")
	s.metrics.SetKnowledgeDocs(s.store.Count())
	return nil
}

func (s *Syncer) Sync(ctx context.Context) error {
	path := s.currentPath()
	if path == "" {
		var err error
		path, err = s.glpi.DiscoverKnowledgeBasePath(ctx, s.cfg.GLPIKBPath)
		if err != nil {
			s.fail(err)
			return err
		}
	}
	items, err := s.glpi.ListKnowledgeBaseItems(ctx, path, s.cfg.GLPIKBLimit, s.cfg.GLPIKBFilter)
	if err != nil {
		s.fail(err)
		return fmt.Errorf("list GLPI knowledge base: %w", err)
	}
	if linkedSource, ok := s.glpi.(LinkedItemSource); ok {
		articleIDs := make([]int64, 0, len(items))
		for _, item := range items {
			articleIDs = append(articleIDs, item.ID)
		}
		linkLimit := s.cfg.GLPIKBLimit * 20
		if linkLimit < 1000 {
			linkLimit = 1000
		}
		links, linkErr := linkedSource.ListKnowledgeBaseLinkedItems(ctx, articleIDs, linkLimit)
		if linkErr != nil {
			slog.Warn("GLPI knowledge linked items unavailable", "error", linkErr, "impact", "articles still synchronize; Obsidian export will omit unavailable GLPI object relations")
		} else {
			for i := range items {
				items[i].LinkedItems = append([]model.LinkedItem(nil), links[items[i].ID]...)
			}
		}
	}
	cats, err := s.glpi.GetCategories(ctx)
	if err != nil {
		slog.Warn(
			"GLPI ITIL categories unavailable for optional KB relevance mapping",
			"error", err,
			"impact", "KB synchronization and Auto-Reply approval continue using GLPI knowledge-base categories",
		)
		cats = nil
	}
	docs := s.normalize(items, cats)
	autoReplyApproved, autoReplyBlocked := autoReplyCounts(docs)
	if err := s.store.ReplaceExternalSource(ctx, s.cfg.GLPIKBSource, docs); err != nil {
		s.fail(err)
		return fmt.Errorf("replace GLPI knowledge source: %w", err)
	}
	now := time.Now()
	cf := cacheFile{PolicyVersion: cachePolicyVersion, ApprovalHash: approvalConfigHash(s.cfg), SyncedAt: now, Path: path, Documents: docs}
	if err := writeAtomicJSON(s.cachePath, cf); err != nil {
		s.fail(err)
		return fmt.Errorf("persist GLPI KB cache: %w", err)
	}
	s.mu.Lock()
	s.resolvedPath = path
	s.lastSync = now
	s.lastError = ""
	s.count = len(docs)
	s.mu.Unlock()
	s.metrics.SetGLPIKBStatus(true, len(docs), now, "")
	s.metrics.SetKnowledgeDocs(s.store.Count())
	slog.Info(
		"GLPI knowledge base synchronized",
		"documents", len(docs),
		"auto_reply_approved", autoReplyApproved,
		"auto_reply_blocked", autoReplyBlocked,
		"path", path,
	)
	return nil
}

func approvalConfigHash(cfg config.Config) string {
	kbIDs := append([]int64(nil), cfg.GLPIKBAutoReplyCategoryIDs...)
	articleIDs := append([]int64(nil), cfg.GLPIKBAutoReplyUncategorizedArticleIDs...)
	sort.Slice(kbIDs, func(i, j int) bool { return kbIDs[i] < kbIDs[j] })
	sort.Slice(articleIDs, func(i, j int) bool { return articleIDs[i] < articleIDs[j] })
	payload, _ := json.Marshal(struct {
		Enabled            bool    `json:"enabled"`
		Source             string  `json:"source"`
		KBCategoryIDs      []int64 `json:"kb_category_ids"`
		AllowUncategorized bool    `json:"allow_uncategorized"`
		ArticleIDs         []int64 `json:"article_ids"`
	}{
		Enabled:            cfg.GLPIKBAutoReply,
		Source:             strings.ToLower(strings.TrimSpace(cfg.GLPIKBSource)),
		KBCategoryIDs:      kbIDs,
		AllowUncategorized: cfg.GLPIKBAutoReplyAllowUncategorized,
		ArticleIDs:         articleIDs,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func autoReplyCounts(docs []model.KnowledgeDoc) (approved, blocked int) {
	for _, doc := range docs {
		if doc.AutoReply {
			approved++
		} else {
			blocked++
		}
	}
	return approved, blocked
}

func (s *Syncer) Start(ctx context.Context) {
	if !s.cfg.GLPIKBEnabled {
		return
	}
	go func() {
		t := time.NewTicker(s.cfg.GLPIKBSyncInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c, cancel := context.WithTimeout(ctx, SyncTimeout(s.cfg))
				if err := s.Sync(c); err != nil {
					slog.Error("GLPI knowledge base sync failed", "error", err)
				}
				cancel()
			}
		}
	}()
}

func (s *Syncer) normalize(items []model.GLPIKnowledgeItem, cats []model.Category) []model.KnowledgeDoc {
	kbToITIL := map[int64][]model.Category{}
	itilCategoryIDs := map[int64]struct{}{}
	for _, c := range cats {
		if c.ID > 0 {
			itilCategoryIDs[c.ID] = struct{}{}
		}
		if c.KnowbaseCategoryID > 0 {
			kbToITIL[c.KnowbaseCategoryID] = append(kbToITIL[c.KnowbaseCategoryID], c)
		}
	}
	autoKBCats := map[int64]struct{}{}
	for _, id := range s.cfg.GLPIKBAutoReplyCategoryIDs {
		autoKBCats[id] = struct{}{}
	}
	autoUncategorizedArticles := map[int64]struct{}{}
	for _, id := range s.cfg.GLPIKBAutoReplyUncategorizedArticleIDs {
		autoUncategorizedArticles[id] = struct{}{}
	}
	warnLikelyITILIDs(s.cfg.GLPIKBAutoReply, s.cfg.GLPIKBAutoReplyCategoryIDs, items, itilCategoryIDs)
	out := make([]model.KnowledgeDoc, 0, len(items))
	for _, item := range items {
		text := cleanHTML(item.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		itilSeen := map[int64]struct{}{}
		var itilIDs []int64
		var keywords []string
		for _, kbID := range item.CategoryIDs {
			for _, c := range kbToITIL[kbID] {
				if _, ok := itilSeen[c.ID]; !ok {
					itilSeen[c.ID] = struct{}{}
					itilIDs = append(itilIDs, c.ID)
				}
				if c.CompleteName != "" {
					keywords = append(keywords, c.CompleteName)
				} else if c.Name != "" {
					keywords = append(keywords, c.Name)
				}
			}
		}
		sort.Slice(itilIDs, func(i, j int) bool { return itilIDs[i] < itilIDs[j] })
		auto, autoDecision, autoDetail := autoReplyApproval(
			s.cfg.GLPIKBAutoReply,
			s.cfg.GLPIKBAutoReplyAllowUncategorized,
			item.ID,
			item.CategoryIDs,
			itilIDs,
			autoKBCats,
			autoUncategorizedArticles,
		)
		language := strings.TrimSpace(item.Language)
		if language == "" {
			language = s.cfg.CommunicationLanguage
		}
		out = append(out, model.KnowledgeDoc{
			ID:    "GLPI-KB-" + strconv.FormatInt(item.ID, 10),
			Title: strings.TrimSpace(item.Title), Text: text, Answer: text, AnswerHTML: strings.TrimSpace(item.Content),
			AutoReply: auto, AutoReplyDecision: autoDecision, AutoReplyDetail: autoDetail,
			MinScore: 0, Categories: itilIDs, Keywords: uniqueStrings(keywords),
			Source: s.cfg.GLPIKBSource, SourceURI: "glpi://KnowbaseItem/" + strconv.FormatInt(item.ID, 10),
			SourceCategoryIDs: append([]int64(nil), item.CategoryIDs...), SourceModifiedAt: item.ModifiedAt,
			Language: language, CommunicationStyle: s.cfg.CommunicationStyle,
			LinkedItems: append([]model.LinkedItem(nil), item.LinkedItems...),
		})
	}
	return out
}

func autoReplyApproval(enabled, allowUncategorized bool, articleID int64, sourceCategoryIDs, itilCategoryIDs []int64, allowedSourceCategories, allowedUncategorizedArticles map[int64]struct{}) (bool, string, string) {
	detail := fmt.Sprintf(
		"GLPI-Artikel-ID: %d; GLPI-KB-Kategorien: %v; gemappte ITIL-Kategorien (nur fachliches Signal): %v; freigegebene GLPI-KB-Kategorien: %v; freigegebene kategorielose Artikel: %v",
		articleID,
		sourceCategoryIDs,
		itilCategoryIDs,
		sortedSetIDs(allowedSourceCategories),
		sortedSetIDs(allowedUncategorizedArticles),
	)
	if !enabled {
		return false, "glpi_kb_auto_reply_disabled", detail + "; GLPI_KB_AUTO_REPLY=false"
	}
	if len(sourceCategoryIDs) == 0 {
		if !allowUncategorized {
			return false, "glpi_kb_article_without_category", detail + "; der Artikel besitzt keine GLPI-KB-Kategorie und GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=false"
		}
		if _, ok := allowedUncategorizedArticles[articleID]; !ok {
			return false, "glpi_kb_uncategorized_article_not_whitelisted", detail + fmt.Sprintf("; GLPI-KB-Artikel #%d ist nicht in GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS freigegeben", articleID)
		}
		return true, "glpi_kb_uncategorized_article_approved", detail + "; Freigabe über die konkrete GLPI-Artikel-ID; die fachliche Eignung für das Ticket wird separat geprüft"
	}
	if intersectsSet(sourceCategoryIDs, allowedSourceCategories) {
		return true, "glpi_kb_auto_reply_approved", detail + "; Freigabe über eine GLPI-Knowledge-Base-Kategorie"
	}
	if len(allowedSourceCategories) == 0 {
		return false, "glpi_kb_auto_reply_whitelist_empty", detail + "; keine GLPI-Knowledge-Base-Kategorie für Auto-Reply freigegeben"
	}
	return false, "glpi_kb_category_not_whitelisted", detail + "; keine Artikel-Knowledge-Base-Kategorie ist in GLPI_KB_AUTO_REPLY_CATEGORY_IDS freigegeben"
}

func intersectsSet(ids []int64, allowed map[int64]struct{}) bool {
	for _, id := range ids {
		if _, ok := allowed[id]; ok {
			return true
		}
	}
	return false
}

func sortedSetIDs(values map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(values))
	for id := range values {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func warnLikelyITILIDs(enabled bool, configured []int64, items []model.GLPIKnowledgeItem, itilCategoryIDs map[int64]struct{}) {
	if !enabled || len(configured) == 0 {
		return
	}
	sourceIDs := map[int64]struct{}{}
	for _, item := range items {
		for _, id := range item.CategoryIDs {
			sourceIDs[id] = struct{}{}
		}
	}
	var suspicious []int64
	for _, id := range configured {
		_, existsAsSource := sourceIDs[id]
		_, existsAsITIL := itilCategoryIDs[id]
		if !existsAsSource && existsAsITIL {
			suspicious = append(suspicious, id)
		}
	}
	if len(suspicious) > 0 {
		sort.Slice(suspicious, func(i, j int) bool { return suspicious[i] < suspicious[j] })
		slog.Warn(
			"GLPI_KB_AUTO_REPLY_CATEGORY_IDS appears to contain ITIL/ticket category IDs",
			"ids", suspicious,
			"hint", "configure the actual GLPI knowledge-base category IDs; ITIL/ticket category IDs do not release articles for Auto-Reply",
		)
	}
}

func (s *Syncer) currentPath() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.resolvedPath }
func (s *Syncer) fail(err error) {
	s.mu.Lock()
	s.lastError = err.Error()
	count := s.count
	last := s.lastSync
	s.mu.Unlock()
	s.metrics.SetGLPIKBStatus(false, count, last, err.Error())
}

var tagRE = regexp.MustCompile(`(?s)<[^>]*>`)
var spaceRE = regexp.MustCompile(`[\t\r\n ]+`)

func cleanHTML(v string) string {
	v = strings.ReplaceAll(v, "<br>", "\n")
	v = strings.ReplaceAll(v, "<br/>", "\n")
	v = strings.ReplaceAll(v, "<br />", "\n")
	v = strings.ReplaceAll(v, "</p>", "\n")
	v = strings.ReplaceAll(v, "</li>", "\n")
	v = tagRE.ReplaceAllString(v, " ")
	v = stdhtml.UnescapeString(v)
	return strings.TrimSpace(spaceRE.ReplaceAllString(v, " "))
}
func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		k := strings.ToLower(v)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	return out
}
func writeAtomicJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
