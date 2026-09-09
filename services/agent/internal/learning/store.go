package learning

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

type Store struct {
	mu       sync.RWMutex
	path     string
	max      int
	examples []model.LearningExample
}

func Open(dataDir string, max int) (*Store, error) {
	if max < 1 {
		max = 500
	}
	s := &Store{path: filepath.Join(dataDir, "category-learning.json"), max: max}
	if b, err := os.ReadFile(s.path); err == nil {
		if err := json.Unmarshal(b, &s.examples); err != nil {
			return nil, fmt.Errorf("parse category learning: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if len(s.examples) > s.max {
		s.examples = s.examples[len(s.examples)-s.max:]
	}
	return s, nil
}

func (s *Store) Add(ex model.LearningExample) (model.LearningExample, error) {
	if s == nil {
		return ex, fmt.Errorf("learning store is not initialized")
	}
	ex.Subject = strings.TrimSpace(ex.Subject)
	ex.Text = strings.TrimSpace(ex.Text)
	ex.CategoryName = strings.TrimSpace(ex.CategoryName)
	if ex.CategoryID <= 0 || ex.CategoryName == "" || (ex.Subject == "" && ex.Text == "") {
		return ex, fmt.Errorf("category, category name and ticket text are required")
	}
	if ex.ID == "" {
		ex.ID = newID()
	}
	if ex.CreatedAt.IsZero() {
		ex.CreatedAt = time.Now().UTC()
	}
	if ex.Source == "" {
		ex.Source = "human-confirmed"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Replace feedback for the same run instead of teaching contradictory examples.
	if ex.RunID != "" {
		for i := range s.examples {
			if s.examples[i].RunID == ex.RunID {
				s.examples[i] = ex
				return ex, s.saveLocked()
			}
		}
	}
	s.examples = append(s.examples, ex)
	if len(s.examples) > s.max {
		s.examples = s.examples[len(s.examples)-s.max:]
	}
	return ex, s.saveLocked()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.examples[:0]
	found := false
	for _, ex := range s.examples {
		if ex.ID == id {
			found = true
			continue
		}
		out = append(out, ex)
	}
	if !found {
		return os.ErrNotExist
	}
	s.examples = append([]model.LearningExample(nil), out...)
	return s.saveLocked()
}

func (s *Store) List() []model.LearningExample {
	if s == nil {
		return []model.LearningExample{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]model.LearningExample{}, s.examples...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *Store) ExamplesFor(categoryID int64, limit int) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for i := len(s.examples) - 1; i >= 0; i-- {
		ex := s.examples[i]
		if ex.CategoryID != categoryID {
			continue
		}
		text := strings.TrimSpace(ex.Subject)
		if ex.Text != "" {
			text += " — " + compact(ex.Text, 180)
		}
		out = append(out, text)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Store) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.examples)
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.examples, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
func newID() string { b := make([]byte, 8); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func compact(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
