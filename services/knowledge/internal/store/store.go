package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu        sync.RWMutex
	dataDir   string
	backupDir string
	records   map[string]*record
	order     []string
}

type record struct {
	Key      string
	RelPath  string
	Path     string
	Doc      map[string]any
	ModTime  time.Time
	Size     int64
	Checksum string
	Search   string
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

type SearchHit struct {
	Summary
	Excerpt string `json:"excerpt"`
	Score   int    `json:"score"`
}

type SearchResult struct {
	Items      []SearchHit `json:"items"`
	Total      int         `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
	Query      string      `json:"query"`
}

type Facet struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Facets struct {
	Categories []Facet `json:"categories"`
	Keywords   []Facet `json:"keywords"`
	Sources    []Facet `json:"sources"`
}

type FindReplace struct {
	Fields        []string `json:"fields"`
	Find          string   `json:"find"`
	Replace       string   `json:"replace"`
	Regex         bool     `json:"regex"`
	CaseSensitive bool     `json:"case_sensitive"`
}

type BulkPatch struct {
	SetAutoReply          *bool        `json:"set_auto_reply,omitempty"`
	SetMinScore           *float64     `json:"set_min_score,omitempty"`
	SetLanguage           *string      `json:"set_language,omitempty"`
	SetCommunicationStyle *string      `json:"set_communication_style,omitempty"`
	SetSource             *string      `json:"set_source,omitempty"`
	SetSourceURI          *string      `json:"set_source_uri,omitempty"`
	AddKeywords           []string     `json:"add_keywords,omitempty"`
	RemoveKeywords        []string     `json:"remove_keywords,omitempty"`
	AddCategories         []string     `json:"add_categories,omitempty"`
	RemoveCategories      []string     `json:"remove_categories,omitempty"`
	FindReplace           *FindReplace `json:"find_replace,omitempty"`
}

type BulkResult struct {
	Targeted int       `json:"targeted"`
	Changed  int       `json:"changed"`
	Skipped  int       `json:"skipped"`
	Keys     []string  `json:"keys"`
	Sample   []Summary `json:"sample"`
	Backup   string    `json:"backup,omitempty"`
	DryRun   bool      `json:"dry_run"`
}

func New(dataDir string) (*Store, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	backup := strings.TrimSpace(os.Getenv("BACKUP_DIR"))
	if backup == "" {
		backup = filepath.Join(filepath.Dir(abs), ".kb-editor-backups")
	}
	if !filepath.IsAbs(backup) {
		backup, err = filepath.Abs(backup)
		if err != nil {
			return nil, err
		}
	}

	s := &Store{dataDir: abs, backupDir: backup, records: make(map[string]*record)}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) DataDir() string   { return s.dataDir }
func (s *Store) BackupDir() string { return s.backupDir }

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

func (s *Store) Reload() error {
	records := make(map[string]*record)
	order := make([]string, 0)

	err := filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if samePath(path, s.backupDir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".json") {
			return nil
		}
		rec, err := s.readRecord(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		if _, exists := records[rec.Key]; exists {
			return fmt.Errorf("duplicate internal key for %s", rec.RelPath)
		}
		records[rec.Key] = rec
		order = append(order, rec.Key)
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(order, func(i, j int) bool {
		a, b := records[order[i]], records[order[j]]
		at, bt := strings.ToLower(str(a.Doc["title"])), strings.ToLower(str(b.Doc["title"]))
		if at == bt {
			return a.RelPath < b.RelPath
		}
		return at < bt
	})

	s.mu.Lock()
	s.records = records
	s.order = order
	s.mu.Unlock()
	return nil
}

func (s *Store) readRecord(path string) (*record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if doc == nil {
		return nil, errors.New("JSON root must be an object")
	}
	rel, err := filepath.Rel(s.dataDir, path)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	rec := &record{
		Key:      encodeKey(rel),
		RelPath:  rel,
		Path:     path,
		Doc:      doc,
		ModTime:  st.ModTime(),
		Size:     st.Size(),
		Checksum: fmt.Sprintf("%x", sum[:8]),
	}
	rec.Search = buildSearch(doc, rel)
	return rec, nil
}

func encodeKey(rel string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(rel))
}

func (s *Store) Get(key string) (map[string]any, Summary, error) {
	s.mu.RLock()
	rec, ok := s.records[key]
	if !ok {
		s.mu.RUnlock()
		return nil, Summary{}, os.ErrNotExist
	}
	doc := cloneMap(rec.Doc)
	summary := summarize(rec)
	s.mu.RUnlock()
	return doc, summary, nil
}

func (s *Store) List(q Query) ListResult {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 50
	}
	if q.PageSize > 500 {
		q.PageSize = 500
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	matches := make([]string, 0)
	for _, key := range s.order {
		rec := s.records[key]
		if match(rec, q) {
			matches = append(matches, key)
		}
	}

	total := len(matches)
	totalPages := 0
	if total > 0 {
		totalPages = (total + q.PageSize - 1) / q.PageSize
	}
	if totalPages > 0 && q.Page > totalPages {
		q.Page = totalPages
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
	items := make([]Summary, 0, end-start)
	for _, key := range matches[start:end] {
		items = append(items, summarize(s.records[key]))
	}
	return ListResult{Items: items, Total: total, Page: q.Page, PageSize: q.PageSize, TotalPages: totalPages}
}

func (s *Store) Search(q Query) SearchResult {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 100 {
		q.PageSize = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type ranked struct {
		rec   *record
		score int
	}
	matches := make([]ranked, 0)
	for _, key := range s.order {
		rec := s.records[key]
		if !match(rec, q) {
			continue
		}
		matches = append(matches, ranked{rec: rec, score: relevanceScore(rec, q.Q)})
	}

	if strings.TrimSpace(q.Q) != "" {
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].score != matches[j].score {
				return matches[i].score > matches[j].score
			}
			ai := strings.ToLower(str(matches[i].rec.Doc["title"]))
			aj := strings.ToLower(str(matches[j].rec.Doc["title"]))
			if ai != aj {
				return ai < aj
			}
			return matches[i].rec.RelPath < matches[j].rec.RelPath
		})
	}

	total := len(matches)
	totalPages := 0
	if total > 0 {
		totalPages = (total + q.PageSize - 1) / q.PageSize
	}
	if totalPages > 0 && q.Page > totalPages {
		q.Page = totalPages
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

	items := make([]SearchHit, 0, end-start)
	for _, item := range matches[start:end] {
		items = append(items, SearchHit{
			Summary: summarize(item.rec),
			Excerpt: searchExcerpt(item.rec.Doc, q.Q),
			Score:   item.score,
		})
	}
	return SearchResult{
		Items: items, Total: total, Page: q.Page, PageSize: q.PageSize,
		TotalPages: totalPages, Query: strings.TrimSpace(q.Q),
	}
}

func (s *Store) Facets(limit int) Facets {
	if limit < 1 {
		limit = 8
	}
	if limit > 30 {
		limit = 30
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	categories := make(map[string]int)
	keywords := make(map[string]int)
	sources := make(map[string]int)
	ignoredKeywords := map[string]struct{}{
		"microsoft": {}, "windows": {}, "fehler": {}, "fehlercode": {}, "error": {}, "status": {},
	}
	for _, rec := range s.records {
		for _, category := range toStrings(rec.Doc["categories"]) {
			category = strings.TrimSpace(category)
			if category != "" {
				categories[category]++
			}
		}
		for _, keyword := range toStrings(rec.Doc["keywords"]) {
			keyword = strings.TrimSpace(keyword)
			lower := strings.ToLower(keyword)
			if keyword == "" || strings.HasPrefix(lower, "0x") {
				continue
			}
			if _, ignored := ignoredKeywords[lower]; ignored {
				continue
			}
			keywords[keyword]++
		}
		source := strings.TrimSpace(str(rec.Doc["source"]))
		if source != "" {
			sources[source]++
		}
	}
	return Facets{
		Categories: topFacets(categories, limit),
		Keywords:   topFacets(keywords, limit),
		Sources:    topFacets(sources, limit),
	}
}

func topFacets(values map[string]int, limit int) []Facet {
	out := make([]Facet, 0, len(values))
	for name, count := range values {
		out = append(out, Facet{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func relevanceScore(rec *record, query string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0
	}
	terms := strings.Fields(query)
	fields := []struct {
		value  string
		weight int
	}{
		{strings.ToLower(str(rec.Doc["id"])), 28},
		{strings.ToLower(str(rec.Doc["title"])), 24},
		{strings.ToLower(strings.Join(toStrings(rec.Doc["keywords"]), " ")), 18},
		{strings.ToLower(strings.Join(toStrings(rec.Doc["categories"]), " ")), 10},
		{strings.ToLower(str(rec.Doc["text"])), 7},
		{strings.ToLower(str(rec.Doc["answer"])), 5},
		{strings.ToLower(str(rec.Doc["source"])), 2},
	}

	score := 0
	for idx, field := range fields {
		if field.value == query {
			score += field.weight * 8
		}
		if strings.Contains(field.value, query) {
			score += field.weight * 3
		}
		for _, term := range terms {
			if field.value == term {
				score += field.weight * 4
			} else if strings.Contains(field.value, term) {
				score += field.weight
			}
		}
		if idx == 1 && strings.HasPrefix(field.value, query) {
			score += 40
		}
	}
	return score
}

func searchExcerpt(doc map[string]any, query string) string {
	candidates := []string{str(doc["text"]), str(doc["answer"])}
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	for _, candidate := range candidates {
		candidate = cleanExcerpt(candidate)
		if candidate == "" {
			continue
		}
		lower := strings.ToLower(candidate)
		pos := -1
		for _, term := range terms {
			if i := strings.Index(lower, term); i >= 0 && (pos < 0 || i < pos) {
				pos = i
			}
		}
		if pos < 0 {
			return truncateRunes(candidate, 300)
		}
		runes := []rune(candidate)
		prefixRunes := []rune(candidate[:pos])
		start := len(prefixRunes) - 90
		if start < 0 {
			start = 0
		}
		end := start + 320
		if end > len(runes) {
			end = len(runes)
		}
		excerpt := strings.TrimSpace(string(runes[start:end]))
		if start > 0 {
			excerpt = "… " + excerpt
		}
		if end < len(runes) {
			excerpt += " …"
		}
		return excerpt
	}
	return ""
}

func cleanExcerpt(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + " …"
}

func (s *Store) MatchingKeys(q Query) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0)
	for _, key := range s.order {
		if match(s.records[key], q) {
			keys = append(keys, key)
		}
	}
	return keys
}

func match(rec *record, q Query) bool {
	if text := strings.TrimSpace(strings.ToLower(q.Q)); text != "" {
		for _, term := range strings.Fields(text) {
			if !strings.Contains(rec.Search, term) {
				return false
			}
		}
	}
	if v := strings.TrimSpace(q.AutoReply); v != "" && v != "any" {
		expected, err := strconv.ParseBool(v)
		actual, ok := rec.Doc["auto_reply"].(bool)
		if err != nil || !ok || actual != expected {
			return false
		}
	}
	if q.Language != "" && !strings.EqualFold(str(rec.Doc["language"]), q.Language) {
		return false
	}
	if q.CommunicationStyle != "" && !strings.EqualFold(str(rec.Doc["communication_style"]), q.CommunicationStyle) {
		return false
	}
	if q.Source != "" && !strings.Contains(strings.ToLower(str(rec.Doc["source"])), strings.ToLower(q.Source)) {
		return false
	}
	return true
}

func (s *Store) Save(key string, doc map[string]any) (Summary, string, error) {
	if doc == nil {
		return Summary{}, "", errors.New("JSON root must be an object")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key]
	if !ok {
		return Summary{}, "", os.ErrNotExist
	}
	if docsEqual(rec.Doc, doc) {
		return summarize(rec), "", nil
	}
	if err := verifyUnchanged(rec); err != nil {
		return Summary{}, "", err
	}
	backupBatch, err := s.newBackupBatch()
	if err != nil {
		return Summary{}, "", err
	}
	if err := s.backupRecord(rec, backupBatch); err != nil {
		return Summary{}, "", err
	}
	newRec, err := s.writeRecord(rec, doc)
	if err != nil {
		return Summary{}, "", err
	}
	s.records[key] = newRec
	s.resortLocked()
	return summarize(newRec), backupBatch, nil
}

// ImportDocument creates a new production JSON file without overwriting an existing entry.
// It is used when a reviewed staging article is promoted into the productive knowledge base.
func (s *Store) ImportDocument(doc map[string]any, preferredBase string) (Summary, error) {
	if doc == nil {
		return Summary{}, errors.New("JSON root must be an object")
	}
	id := strings.TrimSpace(str(doc["id"]))
	if id == "" {
		id = strings.TrimSpace(preferredBase)
		doc = cloneMap(doc)
		doc["id"] = id
	}
	base := safeFilenameBase(id)
	if base == "" {
		base = safeFilenameBase(preferredBase)
	}
	if base == "" {
		return Summary{}, errors.New("cannot derive a safe production filename from document id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.records {
		if strings.EqualFold(strings.TrimSpace(str(rec.Doc["id"])), id) {
			return Summary{}, fmt.Errorf("knowledge entry with id %q already exists", id)
		}
	}
	rel := base + ".json"
	path := filepath.Join(s.dataDir, rel)
	if _, err := os.Stat(path); err == nil {
		return Summary{}, fmt.Errorf("production target already exists: %s", rel)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Summary{}, err
	}
	payload, err := marshalDocument(doc)
	if err != nil {
		return Summary{}, err
	}
	tmp, err := os.CreateTemp(s.dataDir, ".kb-import-*.tmp")
	if err != nil {
		return Summary{}, err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return Summary{}, err
	}
	if _, err := tmp.Write(payload); err != nil {
		cleanup()
		return Summary{}, err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return Summary{}, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return Summary{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return Summary{}, err
	}
	if err := syncDir(s.dataDir); err != nil {
		_ = os.Remove(path)
		return Summary{}, err
	}
	rec, err := s.readRecord(path)
	if err != nil {
		_ = os.Remove(path)
		return Summary{}, err
	}
	s.records[rec.Key] = rec
	s.order = append(s.order, rec.Key)
	s.resortLocked()
	return summarize(rec), nil
}

// RollbackImported removes exactly the production record created by ImportDocument.
// The checksum guard prevents rollback from deleting a file that was changed after
// import. It is used to keep staging promotion transactional.
func (s *Store) RollbackImported(summary Summary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[summary.Key]
	if !ok {
		return os.ErrNotExist
	}
	if summary.Checksum == "" || rec.Checksum != summary.Checksum {
		return fmt.Errorf("refusing rollback: production record %s changed after import", summary.RelPath)
	}
	if err := os.Remove(rec.Path); err != nil {
		return err
	}
	// The namespace mutation already happened once Remove succeeded. Reflect it
	// in memory even when the durability fsync below reports an I/O error;
	// otherwise this process would serve a record whose file no longer exists.
	delete(s.records, summary.Key)
	for i, key := range s.order {
		if key == summary.Key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if err := syncDir(s.dataDir); err != nil {
		return fmt.Errorf("sync production directory after rollback: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func safeFilenameBase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), ".-_ ")
	if len(out) > 180 {
		out = out[:180]
	}
	return out
}

func (s *Store) ApplyBulk(keys []string, patch BulkPatch, dryRun bool) (BulkResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys = unique(keys)
	result := BulkResult{Targeted: len(keys), DryRun: dryRun}
	type pending struct {
		key string
		doc map[string]any
	}
	changes := make([]pending, 0)
	for _, key := range keys {
		rec, ok := s.records[key]
		if !ok {
			result.Skipped++
			continue
		}
		doc := cloneMap(rec.Doc)
		changed, err := applyPatch(doc, patch)
		if err != nil {
			return result, fmt.Errorf("patch %s: %w", rec.RelPath, err)
		}
		if !changed {
			result.Skipped++
			continue
		}
		changes = append(changes, pending{key: key, doc: doc})
		result.Changed++
		result.Keys = append(result.Keys, key)
		if len(result.Sample) < 20 {
			preview := *rec
			preview.Doc = doc
			preview.Search = buildSearch(doc, rec.RelPath)
			result.Sample = append(result.Sample, summarize(&preview))
		}
	}
	if dryRun || len(changes) == 0 {
		return result, nil
	}

	for _, c := range changes {
		if err := verifyUnchanged(s.records[c.key]); err != nil {
			return result, err
		}
	}

	batch, err := s.newBackupBatch()
	if err != nil {
		return result, err
	}
	result.Backup = batch

	for _, c := range changes {
		rec := s.records[c.key]
		if err := s.backupRecord(rec, batch); err != nil {
			return result, fmt.Errorf("backup %s: %w", rec.RelPath, err)
		}
	}
	for _, c := range changes {
		rec := s.records[c.key]
		newRec, err := s.writeRecord(rec, c.doc)
		if err != nil {
			return result, fmt.Errorf("write %s: %w", rec.RelPath, err)
		}
		s.records[c.key] = newRec
	}
	s.resortLocked()
	return result, nil
}

func applyPatch(doc map[string]any, patch BulkPatch) (bool, error) {
	before, _ := json.Marshal(doc)
	if patch.SetAutoReply != nil {
		doc["auto_reply"] = *patch.SetAutoReply
	}
	if patch.SetMinScore != nil {
		doc["min_score"] = *patch.SetMinScore
	}
	if patch.SetLanguage != nil {
		doc["language"] = *patch.SetLanguage
	}
	if patch.SetCommunicationStyle != nil {
		doc["communication_style"] = *patch.SetCommunicationStyle
	}
	if patch.SetSource != nil {
		doc["source"] = *patch.SetSource
	}
	if patch.SetSourceURI != nil {
		doc["source_uri"] = *patch.SetSourceURI
	}
	if len(patch.AddKeywords) > 0 || len(patch.RemoveKeywords) > 0 {
		doc["keywords"] = mutateStringList(toStrings(doc["keywords"]), patch.AddKeywords, patch.RemoveKeywords)
	}
	if len(patch.AddCategories) > 0 || len(patch.RemoveCategories) > 0 {
		doc["categories"] = mutateStringList(toStrings(doc["categories"]), patch.AddCategories, patch.RemoveCategories)
	}
	if fr := patch.FindReplace; fr != nil && fr.Find != "" {
		fields := fr.Fields
		if len(fields) == 0 {
			fields = []string{"title", "text", "answer"}
		}
		var re *regexp.Regexp
		var err error
		if fr.Regex {
			pattern := fr.Find
			if !fr.CaseSensitive {
				pattern = "(?i)" + pattern
			}
			re, err = regexp.Compile(pattern)
			if err != nil {
				return false, fmt.Errorf("invalid regular expression: %w", err)
			}
		}
		for _, field := range fields {
			old, ok := doc[field].(string)
			if !ok {
				continue
			}
			var next string
			if fr.Regex {
				next = re.ReplaceAllString(old, fr.Replace)
			} else if fr.CaseSensitive {
				next = strings.ReplaceAll(old, fr.Find, fr.Replace)
			} else {
				next = replaceAllFold(old, fr.Find, fr.Replace)
			}
			doc[field] = next
		}
	}
	after, _ := json.Marshal(doc)
	return !bytes.Equal(before, after), nil
}

func replaceAllFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	lowerS := strings.ToLower(s)
	lowerOld := strings.ToLower(old)
	var b strings.Builder
	pos := 0
	for {
		idx := strings.Index(lowerS[pos:], lowerOld)
		if idx < 0 {
			b.WriteString(s[pos:])
			break
		}
		idx += pos
		b.WriteString(s[pos:idx])
		b.WriteString(repl)
		pos = idx + len(old)
	}
	return b.String()
}

func mutateStringList(existing, add, remove []string) []string {
	rm := make(map[string]struct{})
	for _, v := range remove {
		rm[strings.ToLower(strings.TrimSpace(v))] = struct{}{}
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(existing)+len(add))
	appendOne := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		k := strings.ToLower(v)
		if _, bad := rm[k]; bad {
			return
		}
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	for _, v := range existing {
		appendOne(v)
	}
	for _, v := range add {
		appendOne(v)
	}
	return out
}

func (s *Store) writeRecord(rec *record, doc map[string]any) (*record, error) {
	payload, err := marshalDocument(doc)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(rec.Path)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(rec.Path), ".kb-editor-*.tmp")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(st.Mode().Perm()); err != nil {
		cleanup()
		return nil, err
	}
	if _, err := tmp.Write(payload); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := os.Rename(tmpName, rec.Path); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := syncDir(filepath.Dir(rec.Path)); err != nil {
		return nil, err
	}
	return s.readRecord(rec.Path)
}

func (s *Store) newBackupBatch() (string, error) {
	stamp := time.Now().Format("20060102-150405.000000000")
	dir := filepath.Join(s.backupDir, stamp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	return dir, nil
}

func (s *Store) backupRecord(rec *record, batch string) error {
	dst := filepath.Join(batch, filepath.FromSlash(rec.RelPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(rec.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *Store) resortLocked() {
	sort.Slice(s.order, func(i, j int) bool {
		a, b := s.records[s.order[i]], s.records[s.order[j]]
		at, bt := strings.ToLower(str(a.Doc["title"])), strings.ToLower(str(b.Doc["title"]))
		if at == bt {
			return a.RelPath < b.RelPath
		}
		return at < bt
	})
}

func summarize(rec *record) Summary {
	var ar *bool
	if v, ok := rec.Doc["auto_reply"].(bool); ok {
		vv := v
		ar = &vv
	}
	var ms *float64
	if v, ok := number(rec.Doc["min_score"]); ok {
		vv := v
		ms = &vv
	}
	return Summary{
		Key:                rec.Key,
		ID:                 str(rec.Doc["id"]),
		Title:              str(rec.Doc["title"]),
		AutoReply:          ar,
		MinScore:           ms,
		Language:           str(rec.Doc["language"]),
		CommunicationStyle: str(rec.Doc["communication_style"]),
		Source:             str(rec.Doc["source"]),
		Keywords:           toStrings(rec.Doc["keywords"]),
		Categories:         toStrings(rec.Doc["categories"]),
		RelPath:            rec.RelPath,
		ModifiedAt:         rec.ModTime.Format(time.RFC3339),
		Size:               rec.Size,
		Checksum:           rec.Checksum,
	}
}

func buildSearch(doc map[string]any, rel string) string {
	parts := []string{rel}
	for _, key := range []string{"id", "title", "text", "answer", "source", "source_uri", "language", "communication_style"} {
		parts = append(parts, str(doc[key]))
	}
	parts = append(parts, toStrings(doc["keywords"])...)
	parts = append(parts, toStrings(doc["categories"])...)
	return strings.ToLower(strings.Join(parts, "\n"))
}

func cloneMap(in map[string]any) map[string]any {
	b, _ := json.Marshal(in)
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	_ = dec.Decode(&out)
	return out
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

func toStrings(v any) []string {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return []string{}
	}
}

func marshalDocument(doc map[string]any) ([]byte, error) {
	preferred := []string{"id", "title", "text", "answer", "auto_reply", "min_score", "categories", "keywords", "source", "source_uri", "language", "communication_style"}
	seen := make(map[string]struct{}, len(doc))
	keys := make([]string, 0, len(doc))
	for _, key := range preferred {
		if _, ok := doc[key]; ok {
			keys = append(keys, key)
			seen[key] = struct{}{}
		}
	}
	extra := make([]string, 0)
	for key := range doc {
		if _, ok := seen[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	keys = append(keys, extra...)

	var out bytes.Buffer
	out.WriteString("{\n")
	for i, key := range keys {
		kb, _ := json.Marshal(key)
		vb, err := json.MarshalIndent(doc[key], "", "  ")
		if err != nil {
			return nil, err
		}
		vb = bytes.ReplaceAll(vb, []byte("\n"), []byte("\n  "))
		out.WriteString("  ")
		out.Write(kb)
		out.WriteString(": ")
		out.Write(vb)
		if i < len(keys)-1 {
			out.WriteByte(',')
		}
		out.WriteByte('\n')
	}
	out.WriteString("}\n")
	return out.Bytes(), nil
}

func docsEqual(a, b map[string]any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

func verifyUnchanged(rec *record) error {
	b, err := os.ReadFile(rec.Path)
	if err != nil {
		return fmt.Errorf("Datei vor dem Speichern erneut lesen: %w", err)
	}
	sum := sha256.Sum256(b)
	current := fmt.Sprintf("%x", sum[:8])
	if current != rec.Checksum {
		return fmt.Errorf("%s wurde außerhalb des Editors verändert; bitte zuerst neu einlesen", rec.RelPath)
	}
	return nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(aa) == filepath.Clean(bb)
}

func unique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// ExportDocument is an immutable snapshot used by read-only exporters.
type ExportDocument struct {
	Document map[string]any `json:"document"`
	Summary  Summary        `json:"summary"`
}

// ExportDocuments returns a consistent copy of the complete canonical
// knowledge base without exposing mutable in-memory maps to exporters.
func (s *Store) ExportDocuments() []ExportDocument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ExportDocument, 0, len(s.order))
	for _, key := range s.order {
		rec := s.records[key]
		out = append(out, ExportDocument{Document: cloneMap(rec.Doc), Summary: summarize(rec)})
	}
	return out
}
