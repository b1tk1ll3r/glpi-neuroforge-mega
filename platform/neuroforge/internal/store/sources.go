package store

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
)

func cloneSource(src core.KnowledgeSource) core.KnowledgeSource {
	src.MemoryIDs = append([]string(nil), src.MemoryIDs...)
	return src
}

func (s *Store) UpsertSource(src *core.KnowledgeSource) error {
	if src == nil || strings.TrimSpace(src.Title) == "" {
		return errors.New("source title required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Sources == nil {
		s.state.Sources = map[string]*core.KnowledgeSource{}
	}
	now := time.Now().UTC()
	if src.ID == "" {
		src.ID = NewID("src")
	}
	if src.Status == "" {
		src.Status = "ready"
	}
	if src.CreatedAt.IsZero() {
		if old := s.state.Sources[src.ID]; old != nil {
			src.CreatedAt = old.CreatedAt
		} else {
			src.CreatedAt = now
		}
	}
	src.UpdatedAt = now
	cp := cloneSource(*src)
	s.state.Sources[src.ID] = &cp
	return s.commitLocked("source.upsert", cp)
}

func (s *Store) GetSource(id string) (*core.KnowledgeSource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	x := s.state.Sources[id]
	if x == nil {
		return nil, false
	}
	cp := cloneSource(*x)
	return &cp, true
}

func (s *Store) SourcesSnapshot(limit int) []core.KnowledgeSource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.KnowledgeSource, 0, len(s.state.Sources))
	for _, x := range s.state.Sources {
		out = append(out, cloneSource(*x))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Store) SaveSourceBlob(sourceID, fileName string, data []byte) (string, error) {
	if strings.TrimSpace(sourceID) == "" {
		return "", errors.New("source id required")
	}
	root := filepath.Join(s.dir, "sources", sourceID)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	name := filepath.Base(strings.TrimSpace(fileName))
	if name == "." || name == "" {
		name = "original.bin"
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join("sources", sourceID, name)), nil
}
