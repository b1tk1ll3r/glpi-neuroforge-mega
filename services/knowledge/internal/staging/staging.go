package staging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var safeKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,179}$`)

type Draft struct {
	Title      string   `json:"title"`
	Text       string   `json:"text"`
	Answer     string   `json:"answer"`
	Categories []string `json:"categories"`
	Keywords   []string `json:"keywords"`
}

type Result struct {
	Key      string         `json:"key"`
	Document map[string]any `json:"document"`
	Meta     map[string]any `json:"meta"`
}

// IntegrationOptions carries idempotency and provenance metadata for machine-generated
// proposals. IntegrationKey is stable for one producer/goal and causes active staging
// to be updated instead of creating a new draft every research cycle.
type IntegrationOptions struct {
	IntegrationKey string
	Metadata       map[string]any
}

type Summary struct {
	Key                string   `json:"key"`
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	AutoReply          *bool    `json:"auto_reply,omitempty"`
	MinScore           *float64 `json:"min_score,omitempty"`
	Language           string   `json:"language"`
	CommunicationStyle string   `json:"communication_style"`
	Source             string   `json:"source"`
	Keywords           []string `json:"keywords"`
	Categories         []string `json:"categories"`
	RelPath            string   `json:"rel_path"`
	ModifiedAt         string   `json:"modified_at"`
	Size               int64    `json:"size"`
	Checksum           string   `json:"checksum"`
	Staging            bool     `json:"staging"`
}

type Query struct {
	Q                  string `json:"q"`
	AutoReply          string `json:"auto_reply"`
	Language           string `json:"language"`
	CommunicationStyle string `json:"communication_style"`
	Source             string `json:"source"`
	Page               int    `json:"page"`
	PageSize           int    `json:"page_size"`
}

type ListResult struct {
	Items      []Summary `json:"items"`
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	PageSize   int       `json:"page_size"`
	TotalPages int       `json:"total_pages"`
}

type Store struct {
	mu  sync.Mutex
	dir string
}

func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("staging directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	return &Store{dir: abs}, nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) Count() int {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			count++
		}
	}
	return count
}

func (s *Store) Save(query, model string, draft Draft, autoReply bool, minScore float64) (Result, error) {
	return s.SaveFromSource(query, fmt.Sprintf("Ollama / %s", strings.TrimSpace(model)), draft, autoReply, minScore)
}

// SaveFromSource stores a proposal in the human-review staging area while
// preserving the system that produced it. It never promotes into production.
func (s *Store) SaveFromSource(query, source string, draft Draft, autoReply bool, minScore float64) (Result, error) {
	return s.SaveFromIntegration(query, source, draft, autoReply, minScore, IntegrationOptions{})
}

// SaveFromIntegration stores or refreshes an active machine-generated staging draft.
// A stable IntegrationKey makes the operation idempotent across autonomous cycles.
func (s *Store) SaveFromIntegration(query, source string, draft Draft, autoReply bool, minScore float64, opts IntegrationOptions) (Result, error) {
	draft.Title = clampString(draft.Title, 320)
	draft.Text = clampString(draft.Text, 16000)
	draft.Answer = clampString(draft.Answer, 32000)
	draft.Categories = clampStrings(draft.Categories, 16, 120)
	draft.Keywords = clampStrings(draft.Keywords, 48, 120)
	if draft.Title == "" || draft.Answer == "" {
		return Result{}, errors.New("AI draft is missing title or answer")
	}
	if minScore < 0 || minScore > 1 {
		minScore = 0.78
	}
	source = clampString(source, 240)
	if source == "" {
		source = "External Research"
	}

	now := time.Now().UTC()
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(query)) + "\x00" + now.Format(time.RFC3339Nano)))
	id := fmt.Sprintf("KB-AI-STAGING-%s-%s-%s", now.Format("20060102"), now.Format("150405"), strings.ToUpper(hex.EncodeToString(sum[:4])))

	categories := uniqueStrings(append([]string{"AI-Staging"}, draft.Categories...))
	keywords := uniqueStrings(draft.Keywords)
	for _, token := range extractUsefulQueryTokens(query) {
		keywords = uniqueStrings(append(keywords, token))
	}

	doc := map[string]any{
		"id":                  id,
		"title":               draft.Title,
		"text":                draft.Text,
		"answer":              draft.Answer,
		"auto_reply":          autoReply,
		"min_score":           minScore,
		"categories":          categories,
		"keywords":            keywords,
		"source":              source + " (AI-Staging)",
		"source_uri":          "",
		"language":            "de-DE",
		"communication_style": "formal",
	}
	key := strings.TrimSpace(opts.IntegrationKey)
	if key != "" {
		doc["integration_key"] = clampString(key, 240)
	}
	// Metadata may only add new provenance keys; it must never override the
	// fields computed above or the timestamps managed below.
	for k, v := range opts.Metadata {
		k = strings.TrimSpace(k)
		if k == "" || k == "id" || k == "auto_reply" || k == "created_at" || k == "updated_at" {
			continue
		}
		if _, exists := doc[k]; exists {
			continue
		}
		doc[k] = v
	}
	// Hold the lock across lookup and write so concurrent calls with the same
	// IntegrationKey cannot both miss the lookup and create duplicate drafts.
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != "" {
		if existing, ok := s.findByIntegrationKey(key); ok {
			doc["id"] = existing.Key
			if oldCreated, exists := existing.Document["created_at"]; exists {
				doc["created_at"] = oldCreated
			}
			doc["updated_at"] = now.Format(time.RFC3339)
			if _, err := s.updateLocked(existing.Key, doc); err != nil {
				return Result{}, err
			}
			result, err := s.Get(existing.Key)
			if err == nil {
				result.Meta["integration_action"] = "updated"
			}
			return result, err
		}
	}
	doc["created_at"] = now.Format(time.RFC3339)
	doc["updated_at"] = now.Format(time.RFC3339)
	if err := s.writeNewLocked(id, doc); err != nil {
		return Result{}, err
	}
	result, err := s.Get(id)
	if err != nil {
		return Result{}, err
	}
	result.Meta["generated_at"] = now.Format(time.RFC3339)
	result.Meta["integration_action"] = "created"
	return result, nil
}

func (s *Store) findByIntegrationKey(key string) (Result, bool) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return Result{}, false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		result, err := s.Get(id)
		if err != nil {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(result.Document["integration_key"])) == strings.TrimSpace(key) {
			return result, true
		}
	}
	return Result{}, false
}

func (s *Store) Get(key string) (Result, error) {
	key = strings.TrimSpace(key)
	path, err := s.pathForKey(key)
	if err != nil {
		return Result{}, os.ErrNotExist
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return Result{}, fmt.Errorf("invalid staging JSON: %w", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(b)
	return Result{
		Key:      key,
		Document: doc,
		Meta: map[string]any{
			"rel_path":    filepath.ToSlash(filepath.Join("staging", filepath.Base(path))),
			"staging":     true,
			"modified_at": st.ModTime().Format(time.RFC3339),
			"size":        st.Size(),
			"checksum":    fmt.Sprintf("%x", sum[:8]),
		},
	}, nil
}

func (s *Store) List(q Query) (ListResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 50
	}
	if q.PageSize > 500 {
		q.PageSize = 500
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return ListResult{}, err
	}
	items := make([]Summary, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !safeKeyPattern.MatchString(key) {
			continue
		}
		result, err := s.Get(key)
		if err != nil {
			return ListResult{}, fmt.Errorf("load staging %s: %w", entry.Name(), err)
		}
		summary := summarize(result)
		if matches(summary, result.Document, q) {
			items = append(items, summary)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ModifiedAt != items[j].ModifiedAt {
			return items[i].ModifiedAt > items[j].ModifiedAt
		}
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	total := len(items)
	totalPages := 0
	if total > 0 {
		totalPages = (total + q.PageSize - 1) / q.PageSize
		if q.Page > totalPages {
			q.Page = totalPages
		}
	}
	start := (q.Page - 1) * q.PageSize
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + q.PageSize
	if end > total {
		end = total
	}
	return ListResult{Items: items[start:end], Total: total, Page: q.Page, PageSize: q.PageSize, TotalPages: totalPages}, nil
}

func (s *Store) Update(key string, doc map[string]any) (Result, error) {
	if doc == nil {
		return Result{}, errors.New("JSON root must be an object")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(key, doc)
}

// updateLocked is Update without locking; the caller must hold s.mu.
func (s *Store) updateLocked(key string, doc map[string]any) (Result, error) {
	path, err := s.pathForKey(key)
	if err != nil {
		return Result{}, os.ErrNotExist
	}
	st, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return Result{}, err
	}
	payload = append(payload, '\n')
	if err := atomicWrite(path, payload, st.Mode().Perm()); err != nil {
		return Result{}, err
	}
	return s.Get(key)
}

// Delete moves a staging file into .trash instead of irreversibly removing it.
func (s *Store) Delete(key string) (string, error) {
	return s.archive(key, ".trash")
}

// ArchiveApproved removes a reviewed item from active staging while keeping the original
// draft for audit purposes below .approved.
func (s *Store) ArchiveApproved(key string) (string, error) {
	return s.archive(key, ".approved")
}

func (s *Store) archive(key, bucket string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pathForKey(key)
	if err != nil {
		return "", os.ErrNotExist
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	archiveDir := filepath.Join(s.dir, bucket)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s.json", time.Now().UTC().Format("20060102-150405.000000000"), key)
	dst := filepath.Join(archiveDir, name)
	if err := os.Rename(path, dst); err != nil {
		return "", fmt.Errorf("move staging file to %s: %w", bucket, err)
	}
	rollbackRename := func(cause error) error {
		if err := os.Rename(dst, path); err != nil {
			return fmt.Errorf("%v; staging archive rollback failed: %w", cause, err)
		}
		if err := syncDir(s.dir); err != nil {
			return fmt.Errorf("%v; staging rollback source sync failed: %w", cause, err)
		}
		if err := syncDir(archiveDir); err != nil {
			return fmt.Errorf("%v; staging rollback archive sync failed: %w", cause, err)
		}
		return cause
	}
	if err := syncDir(s.dir); err != nil {
		return "", rollbackRename(fmt.Errorf("sync staging directory: %w", err))
	}
	if err := syncDir(archiveDir); err != nil {
		return "", rollbackRename(fmt.Errorf("sync staging archive: %w", err))
	}
	return dst, nil
}

// writeNewLocked creates a new staging file; the caller must hold s.mu.
func (s *Store) writeNewLocked(key string, doc map[string]any) error {
	path, err := s.pathForKey(key)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("staging target already exists: %s", filepath.Base(path))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return atomicWrite(path, payload, 0o644)
}

func (s *Store) pathForKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if !safeKeyPattern.MatchString(key) {
		return "", errors.New("invalid staging key")
	}
	return filepath.Join(s.dir, key+".json"), nil
}

func atomicWrite(path string, payload []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".staging-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	// Windows cannot fsync directory handles; NTFS persists renames without it.
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func summarize(result Result) Summary {
	doc := result.Document
	meta := result.Meta
	var autoReply *bool
	if v, ok := doc["auto_reply"].(bool); ok {
		vv := v
		autoReply = &vv
	}
	var minScore *float64
	if v, ok := number(doc["min_score"]); ok {
		vv := v
		minScore = &vv
	}
	return Summary{
		Key:                result.Key,
		ID:                 str(doc["id"]),
		Title:              str(doc["title"]),
		AutoReply:          autoReply,
		MinScore:           minScore,
		Language:           str(doc["language"]),
		CommunicationStyle: str(doc["communication_style"]),
		Source:             str(doc["source"]),
		Keywords:           toStrings(doc["keywords"]),
		Categories:         toStrings(doc["categories"]),
		RelPath:            str(meta["rel_path"]),
		ModifiedAt:         str(meta["modified_at"]),
		Size:               int64Number(meta["size"]),
		Checksum:           str(meta["checksum"]),
		Staging:            true,
	}
}

func matches(summary Summary, doc map[string]any, q Query) bool {
	if text := strings.ToLower(strings.TrimSpace(q.Q)); text != "" {
		search := strings.ToLower(strings.Join([]string{
			summary.ID, summary.Title, str(doc["text"]), str(doc["answer"]), summary.Source,
			strings.Join(summary.Keywords, " "), strings.Join(summary.Categories, " "),
		}, "\n"))
		for _, term := range strings.Fields(text) {
			if !strings.Contains(search, term) {
				return false
			}
		}
	}
	if v := strings.TrimSpace(q.AutoReply); v != "" && v != "any" {
		expected, err := strconv.ParseBool(v)
		if err != nil || summary.AutoReply == nil || *summary.AutoReply != expected {
			return false
		}
	}
	if q.Language != "" && !strings.EqualFold(summary.Language, q.Language) {
		return false
	}
	if q.CommunicationStyle != "" && !strings.EqualFold(summary.CommunicationStyle, q.CommunicationStyle) {
		return false
	}
	if q.Source != "" && !strings.Contains(strings.ToLower(summary.Source), strings.ToLower(q.Source)) {
		return false
	}
	return true
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func int64Number(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	default:
		return 0
	}
}

func toStrings(v any) []string {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if value, ok := item.(string); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		return []string{}
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	if out == nil {
		return []string{}
	}
	return out
}

func clampString(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes]))
}

func clampStrings(values []string, maxItems, maxRunes int) []string {
	out := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		value = clampString(value, maxRunes)
		if value == "" {
			continue
		}
		out = append(out, value)
		if len(out) >= maxItems {
			break
		}
	}
	return uniqueStrings(out)
}

func extractUsefulQueryTokens(query string) []string {
	fields := strings.Fields(query)
	out := make([]string, 0, 6)
	for _, field := range fields {
		field = strings.Trim(field, `.,;:!?()[]{}"'`)
		if len(field) < 3 {
			continue
		}
		if strings.HasPrefix(strings.ToLower(field), "0x") || len(field) >= 5 {
			out = append(out, field)
		}
		if len(out) >= 6 {
			break
		}
	}
	return uniqueStrings(out)
}
