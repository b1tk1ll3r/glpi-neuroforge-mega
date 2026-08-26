package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CategoryMappingEntry represents one external category label discovered in
// knowledge documents or configured in KNOWLEDGE_CATEGORY_MAP_FILE.
type CategoryMappingEntry struct {
	Label      string  `json:"label"`
	Normalized string  `json:"normalized"`
	UsageCount int     `json:"usage_count"`
	MappedIDs  []int64 `json:"mapped_ids"`
	Observed   bool    `json:"observed"`
	Configured bool    `json:"configured"`
}

// CategoryMappingState is the UI/API representation of the current external
// taxonomy -> GLPI ITIL category mapping.
type CategoryMappingState struct {
	Configured       bool                   `json:"configured"`
	Path             string                 `json:"path,omitempty"`
	Mode             string                 `json:"mode"`
	Entries          []CategoryMappingEntry `json:"entries"`
	ObservedCount    int                    `json:"observed_count"`
	MappedObserved   int                    `json:"mapped_observed"`
	UnmappedObserved int                    `json:"unmapped_observed"`
	OrphanMappings   int                    `json:"orphan_mappings"`
	LastAppliedAt    time.Time              `json:"last_applied_at,omitempty"`
}

// CategoryMappings returns all external category labels known from the active
// index plus entries that exist only in the configured mapping file.
func (s *Store) CategoryMappings() (CategoryMappingState, error) {
	if s == nil {
		return CategoryMappingState{}, fmt.Errorf("knowledge store is not initialized")
	}
	path := strings.TrimSpace(s.loadOptions.CategoryMapFile)
	state := CategoryMappingState{Configured: path != "", Path: path, Mode: s.loadOptions.CategoryMode}
	if path == "" {
		return state, nil
	}

	configured, err := readCategoryMapDisplay(path)
	if err != nil {
		return state, err
	}

	type aggregate struct {
		label      string
		count      int
		observed   bool
		configured bool
		ids        []int64
	}
	byKey := map[string]*aggregate{}

	s.mu.RLock()
	for _, d := range s.docs {
		// KNOWLEDGE_CATEGORY_MAP_FILE applies to local JSON documents. Connector-
		// backed documents (for example GLPI KB sync) already arrive normalized and
		// must not be presented as editable mappings here.
		if s.external[d.ID] != "" {
			continue
		}
		seenInDoc := map[string]struct{}{}
		for _, label := range d.ExternalCategories {
			label = strings.TrimSpace(label)
			if label == "" {
				continue
			}
			key := normalizeCategoryLabel(label)
			if _, dup := seenInDoc[key]; dup {
				continue
			}
			seenInDoc[key] = struct{}{}
			a := byKey[key]
			if a == nil {
				a = &aggregate{label: label}
				byKey[key] = a
			}
			a.observed = true
			a.count++
			if len(a.ids) == 0 {
				a.ids = append([]int64(nil), s.categoryMap[key]...)
			}
		}
	}
	s.mu.RUnlock()

	for label, ids := range configured {
		key := normalizeCategoryLabel(label)
		a := byKey[key]
		if a == nil {
			a = &aggregate{label: label}
			byKey[key] = a
		}
		// Preserve the spelling from the file for mappings that are not observed;
		// for observed labels keep the spelling found in the KB corpus.
		if !a.observed {
			a.label = label
		}
		a.configured = true
		a.ids = append([]int64(nil), ids...)
	}

	entries := make([]CategoryMappingEntry, 0, len(byKey))
	for key, a := range byKey {
		ids := uniqueInt64(a.ids)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		entries = append(entries, CategoryMappingEntry{
			Label: a.label, Normalized: key, UsageCount: a.count,
			MappedIDs: ids, Observed: a.observed, Configured: a.configured,
		})
		if a.observed {
			state.ObservedCount++
			if len(ids) > 0 {
				state.MappedObserved++
			} else {
				state.UnmappedObserved++
			}
		} else if a.configured {
			state.OrphanMappings++
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Observed != entries[j].Observed {
			return entries[i].Observed
		}
		if entries[i].UsageCount != entries[j].UsageCount {
			return entries[i].UsageCount > entries[j].UsageCount
		}
		return strings.ToLower(entries[i].Label) < strings.ToLower(entries[j].Label)
	})
	state.Entries = entries
	if info, err := os.Stat(path); err == nil {
		state.LastAppliedAt = info.ModTime()
	}
	return state, nil
}

// SaveCategoryMappings atomically writes KNOWLEDGE_CATEGORY_MAP_FILE and then
// forces a metadata-only re-evaluation of all local KB files. Existing
// embeddings are reused because category mappings do not change embedding
// input text.
func (s *Store) SaveCategoryMappings(ctx context.Context, mappings map[string][]int64) error {
	if s == nil {
		return fmt.Errorf("knowledge store is not initialized")
	}
	s.categoryMapWriteMu.Lock()
	defer s.categoryMapWriteMu.Unlock()
	if strings.EqualFold(strings.TrimSpace(s.scoring.IndexMode), "readonly") {
		return fmt.Errorf("category mappings cannot be changed while KNOWLEDGE_INDEX_MODE=readonly")
	}
	path := strings.TrimSpace(s.loadOptions.CategoryMapFile)
	if path == "" {
		return fmt.Errorf("KNOWLEDGE_CATEGORY_MAP_FILE is not configured")
	}

	clean := make(map[string][]int64, len(mappings))
	display := make(map[string]string, len(mappings))
	for label, ids := range mappings {
		label = strings.TrimSpace(label)
		if label == "" {
			return fmt.Errorf("category mapping label must not be empty")
		}
		key := normalizeCategoryLabel(label)
		ids = uniqueInt64(ids)
		if len(ids) == 0 {
			// An empty assignment means "remove mapping" and is intentionally not
			// persisted. The external category remains visible as unmapped.
			continue
		}
		for _, id := range ids {
			if id <= 0 {
				return fmt.Errorf("category mapping %q contains invalid GLPI id %d", label, id)
			}
		}
		if prev, exists := clean[key]; exists {
			clean[key] = uniqueInt64(append(prev, ids...))
		} else {
			clean[key] = append([]int64(nil), ids...)
			display[key] = label
		}
	}

	if strings.EqualFold(strings.TrimSpace(s.loadOptions.CategoryMode), "strict") {
		s.mu.RLock()
		missing := []string{}
		for _, d := range s.docs {
			if s.external[d.ID] != "" {
				continue
			}
			for _, label := range d.ExternalCategories {
				if len(clean[normalizeCategoryLabel(label)]) == 0 {
					missing = appendUniqueString(missing, label)
				}
			}
		}
		s.mu.RUnlock()
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("KNOWLEDGE_CATEGORY_MODE=strict requires mappings for all observed external categories; missing: %s", strings.Join(missing, ", "))
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create category mapping directory: %w", err)
	}
	if err := writeCategoryMapAtomic(path, clean, display); err != nil {
		return err
	}

	// Coordinate with periodic delta scans: update the in-memory mapping and
	// invalidate local manifest metadata under initMu, then let SyncLocal do the
	// normal safe rebuild/reuse path.
	s.initMu.Lock()
	s.mu.Lock()
	s.categoryMap = cloneCategoryMap(clean)
	for key, rec := range s.manifest {
		if strings.HasPrefix(key, "static/") || strings.HasPrefix(key, "managed/") {
			rec.Size = -1
			rec.ModTimeUnixNano = -1
			rec.RawHash = ""
			s.manifest[key] = rec
		}
	}
	s.mu.Unlock()
	s.initMu.Unlock()

	if err := s.SyncLocal(ctx); err != nil {
		return fmt.Errorf("category mapping saved but re-evaluation failed: %w", err)
	}
	return nil
}

func readCategoryMapDisplay(path string) (map[string][]int64, error) {
	out := map[string][]int64{}
	path = strings.TrimSpace(path)
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("read knowledge category map %q: %w", path, err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("parse knowledge category map %q: %w", path, err)
	}
	if nested, ok := root["mappings"]; ok {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(nested, &m); err != nil {
			return nil, fmt.Errorf("parse mappings in %q: %w", path, err)
		}
		root = m
	}
	for label, rv := range root {
		ids, err := parseMappingIDs(rv)
		if err != nil {
			return nil, fmt.Errorf("category mapping %q: %w", label, err)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("category mapping %q contains no positive GLPI ids", label)
		}
		out[strings.TrimSpace(label)] = uniqueInt64(ids)
	}
	return out, nil
}

func writeCategoryMapAtomic(path string, normalized map[string][]int64, display map[string]string) error {
	keys := make([]string, 0, len(normalized))
	for key := range normalized {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return strings.ToLower(display[keys[i]]) < strings.ToLower(display[keys[j]])
	})

	// Build deterministic, human-readable JSON. A single mapping is written as
	// a number; multiple targets are written as an array. Both formats are
	// already supported by the loader.
	var b strings.Builder
	b.WriteString("{\n")
	for i, key := range keys {
		labelJSON, _ := json.Marshal(display[key])
		b.WriteString("  ")
		b.Write(labelJSON)
		b.WriteString(": ")
		ids := append([]int64(nil), normalized[key]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) == 1 {
			fmt.Fprintf(&b, "%d", ids[0])
		} else {
			encoded, _ := json.Marshal(ids)
			b.Write(encoded)
		}
		if i < len(keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n")

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o640); err != nil {
		return fmt.Errorf("write category mapping temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace category mapping file: %w", err)
	}
	return nil
}

func cloneCategoryMap(in map[string][]int64) map[string][]int64 {
	out := make(map[string][]int64, len(in))
	for key, ids := range in {
		out[key] = append([]int64(nil), ids...)
	}
	return out
}
