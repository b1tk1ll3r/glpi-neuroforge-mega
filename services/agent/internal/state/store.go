package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/example/glpi-ai-agent/internal/model"
)

type Store struct {
	mu                sync.RWMutex
	path              string
	indexPath         string
	processedVersions map[int64]string
	escalationKeys    map[string]struct{}
	runs              []model.RunRecord
	runIndex          map[string]int
	analysisIndex     map[string]model.AnalysisRun
	maxRuns           int
}

type durableIndex struct {
	ProcessedVersions map[string]string `json:"processed_versions"`
	EscalationKeys    []string          `json:"escalation_keys"`
}

func Open(dir string, maxRuns int) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	s := &Store{
		path:              filepath.Join(dir, "runs.jsonl"),
		indexPath:         filepath.Join(dir, "state-index.json"),
		processedVersions: map[int64]string{},
		escalationKeys:    map[string]struct{}{},
		runIndex:          map[string]int{},
		analysisIndex:     map[string]model.AnalysisRun{},
		maxRuns:           maxRuns,
	}
	// Fail fast during startup if the persistent data path is not writable.
	// A read-only/root-owned Docker volume must not be discovered only after the first ticket.
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("state directory %q is not writable: %w", dir, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close state write probe: %w", err)
	}
	if err := s.loadDurableIndex(); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) Seen(id int64, version string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return version != "" && s.processedVersions[id] == version
}
func (s *Store) ProcessedVersionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.processedVersions)
}

func (s *Store) Append(r model.RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	if err = enc.Encode(r); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	s.absorbDurableStateLocked(r)
	s.runs = append(s.runs, r)
	if len(s.runs) > s.maxRuns {
		s.runs = s.runs[len(s.runs)-s.maxRuns:]
	}
	s.rebuildIndexesLocked()
	if err := s.persistDurableIndexLocked(); err != nil {
		return fmt.Errorf("persist durable state index: %w", err)
	}
	if info, statErr := os.Stat(s.path); statErr == nil && info.Size() > 64<<20 {
		if compactErr := s.compactLocked(); compactErr != nil {
			return fmt.Errorf("compact state: %w", compactErr)
		}
	}
	return nil
}
func (s *Store) Recent(limit int) []model.RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.runs) {
		limit = len(s.runs)
	}
	out := make([]model.RunRecord, limit)
	for i := 0; i < limit; i++ {
		out[i] = s.runs[len(s.runs)-1-i]
	}
	return out
}

func (s *Store) FindRun(runID string) (model.RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.runIndex[runID]
	if !ok || i < 0 || i >= len(s.runs) {
		return model.RunRecord{}, false
	}
	return s.runs[i], true
}

func marksTicketVersionProcessed(r model.RunRecord) bool {
	if r.Outcome == "error" || r.Error != "" || strings.EqualFold(r.Trigger, "scheduled_escalation") {
		return false
	}
	return r.SourceVersion != ""
}

func (s *Store) FindAnalysis(analysisID string) (model.AnalysisRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.analysisIndex[analysisID]
	return a, ok
}

func (s *Store) HasEscalationKey(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.escalationKeys[strings.TrimSpace(key)]
	return ok
}

func (s *Store) load() error {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	sc.Buffer(buf, 2*1024*1024)
	for sc.Scan() {
		var r model.RunRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			s.absorbDurableStateLocked(r)
			s.runs = append(s.runs, r)
		}
	}
	if len(s.runs) > s.maxRuns {
		s.runs = s.runs[len(s.runs)-s.maxRuns:]
	}
	sort.SliceStable(s.runs, func(i, j int) bool { return s.runs[i].FinishedAt.Before(s.runs[j].FinishedAt) })
	s.rebuildIndexesLocked()
	return sc.Err()
}

func (s *Store) rebuildIndexesLocked() {
	s.runIndex = make(map[string]int, len(s.runs))
	s.analysisIndex = make(map[string]model.AnalysisRun)
	for i, r := range s.runs {
		s.runIndex[r.RunID] = i
		for _, a := range r.Analyses {
			s.analysisIndex[a.AnalysisID] = a
		}
	}
}

func (s *Store) LatestTicketRun(ticketID int64, excludedTriggers ...string) (model.RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	excluded := make(map[string]struct{}, len(excludedTriggers))
	for _, trigger := range excludedTriggers {
		excluded[strings.ToLower(strings.TrimSpace(trigger))] = struct{}{}
	}
	for i := len(s.runs) - 1; i >= 0; i-- {
		r := s.runs[i]
		if r.TicketID != ticketID {
			continue
		}
		if _, skip := excluded[strings.ToLower(strings.TrimSpace(r.Trigger))]; skip {
			continue
		}
		return r, true
	}
	return model.RunRecord{}, false
}

func (s *Store) absorbDurableStateLocked(r model.RunRecord) {
	if marksTicketVersionProcessed(r) {
		s.processedVersions[r.TicketID] = r.SourceVersion
	}
	for _, a := range r.Analyses {
		if a.AnalysisType != "escalation" {
			continue
		}
		// New escalation plans persist every successfully executed step independently.
		// This keeps idempotency intact even when a later step in the same plan fails.
		for _, step := range a.Action.Steps {
			if !step.Executed {
				continue
			}
			if key := escalationKeyFromResult(step.Result); key != "" {
				s.escalationKeys[key] = struct{}{}
			}
		}
		// Preserve compatibility with historical single-action audit records.
		if a.Action.Executed {
			if key := escalationKeyFromResult(a.Action.Result); key != "" {
				s.escalationKeys[key] = struct{}{}
			}
		}
	}
}

func escalationKeyFromResult(result string) string {
	parts := map[string]string{}
	for _, part := range strings.Split(result, ";") {
		part = strings.TrimSpace(part)
		for _, name := range []string{"ticket", "level", "action", "target"} {
			prefix := name + "="
			if strings.HasPrefix(part, prefix) && len(part) > len(prefix) {
				parts[name] = part
			}
		}
	}
	if parts["ticket"] == "" || parts["level"] == "" {
		return ""
	}
	key := parts["ticket"] + ";" + parts["level"]
	if parts["action"] != "" {
		key += ";" + parts["action"]
	}
	if parts["target"] != "" {
		key += ";" + parts["target"]
	}
	return key
}

func (s *Store) loadDurableIndex() error {
	b, err := os.ReadFile(s.indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read durable state index: %w", err)
	}
	var idx durableIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return fmt.Errorf("decode durable state index: %w", err)
	}
	for rawID, version := range idx.ProcessedVersions {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err == nil && id > 0 && version != "" {
			s.processedVersions[id] = version
		}
	}
	for _, key := range idx.EscalationKeys {
		key = strings.TrimSpace(key)
		if key != "" {
			s.escalationKeys[key] = struct{}{}
		}
	}
	return nil
}

func (s *Store) persistDurableIndexLocked() error {
	idx := durableIndex{ProcessedVersions: make(map[string]string, len(s.processedVersions)), EscalationKeys: make([]string, 0, len(s.escalationKeys))}
	for id, version := range s.processedVersions {
		if id > 0 && version != "" {
			idx.ProcessedVersions[strconv.FormatInt(id, 10)] = version
		}
	}
	for key := range s.escalationKeys {
		idx.EscalationKeys = append(idx.EscalationKeys, key)
	}
	sort.Strings(idx.EscalationKeys)
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.indexPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.indexPath)
}

func (s *Store) compactLocked() error {
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, r := range s.runs {
		if err := enc.Encode(r); err != nil {
			f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}
