package learning

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type TicketOutcome struct {
	ID             string    `json:"id"`
	RunID          string    `json:"run_id"`
	TicketID       int64     `json:"ticket_id"`
	Decision       string    `json:"decision"` // accepted | corrected
	TicketInput    string    `json:"ticket_input"`
	ProposedReply  string    `json:"proposed_reply,omitempty"`
	ConfirmedReply string    `json:"confirmed_reply"`
	CategoryID     int64     `json:"category_id,omitempty"`
	CategoryName   string    `json:"category_name,omitempty"`
	KnowledgeID    string    `json:"knowledge_id,omitempty"`
	Actor          string    `json:"actor"`
	Note           string    `json:"note,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	NeuroForgeID   string    `json:"neuroforge_memory_id,omitempty"`
	SyncStatus     string    `json:"sync_status"` // pending | learned | failed
	SyncError      string    `json:"sync_error,omitempty"`
	SupersedesID   string    `json:"supersedes_id,omitempty"`
}

type OutcomeStore struct {
	mu    sync.RWMutex
	path  string
	max   int
	items []TicketOutcome
}

func OpenOutcomes(dataDir string, max int) (*OutcomeStore, error) {
	if max < 1 {
		max = 2000
	}
	s := &OutcomeStore{path: filepath.Join(dataDir, "ticket-outcomes.json"), max: max}
	if b, err := os.ReadFile(s.path); err == nil {
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, fmt.Errorf("parse ticket outcomes: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if len(s.items) > s.max {
		s.items = s.items[len(s.items)-s.max:]
	}
	return s, nil
}

func (s *OutcomeStore) Add(x TicketOutcome) (TicketOutcome, error) {
	x.RunID = strings.TrimSpace(x.RunID)
	x.Decision = strings.ToLower(strings.TrimSpace(x.Decision))
	x.TicketInput = strings.TrimSpace(x.TicketInput)
	x.ProposedReply = strings.TrimSpace(x.ProposedReply)
	x.ConfirmedReply = strings.TrimSpace(x.ConfirmedReply)
	x.CategoryName = strings.TrimSpace(x.CategoryName)
	x.KnowledgeID = strings.TrimSpace(x.KnowledgeID)
	x.Actor = strings.TrimSpace(x.Actor)
	x.Note = strings.TrimSpace(x.Note)
	if x.RunID == "" || x.TicketID <= 0 || x.TicketInput == "" || x.ConfirmedReply == "" || x.Actor == "" {
		return x, fmt.Errorf("run, ticket, input, confirmed reply and actor are required")
	}
	if x.Decision != "accepted" && x.Decision != "corrected" {
		return x, fmt.Errorf("decision must be accepted or corrected")
	}
	if x.Decision == "accepted" && x.ProposedReply == "" {
		return x, fmt.Errorf("accepted outcome requires proposed reply")
	}
	if x.ID == "" {
		x.ID = outcomeID()
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now().UTC()
	}
	if x.SyncStatus == "" {
		x.SyncStatus = "pending"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.items) - 1; i >= 0; i-- {
		prev := s.items[i]
		if prev.RunID != x.RunID {
			continue
		}
		// Repeating the exact same human decision is idempotent. This also
		// lets a previously failed NeuroForge sync be retried without
		// manufacturing a second human decision.
		if prev.Decision == x.Decision && strings.TrimSpace(prev.ConfirmedReply) == x.ConfirmedReply {
			return prev, nil
		}
		// A later correction/confirmation is a new immutable audit record.
		// Preserve the previous decision and make the revision chain explicit.
		x.SupersedesID = prev.ID
		break
	}
	s.items = append(s.items, x)
	if len(s.items) > s.max {
		s.items = s.items[len(s.items)-s.max:]
	}
	return x, s.saveLocked()
}

func (s *OutcomeStore) UpdateSync(id, status, memoryID, syncErr string) (TicketOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		s.items[i].SyncStatus = status
		s.items[i].NeuroForgeID = memoryID
		s.items[i].SyncError = syncErr
		return s.items[i], s.saveLocked()
	}
	return TicketOutcome{}, os.ErrNotExist
}

func (s *OutcomeStore) List() []TicketOutcome {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]TicketOutcome(nil), s.items...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (s *OutcomeStore) saveLocked() error {
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, b, 0o640)
}
func outcomeID() string { b := make([]byte, 12); _, _ = rand.Read(b); return hex.EncodeToString(b) }

type OutcomeSink interface {
	LearnOutcome(context.Context, TicketOutcome) (string, error)
}

type OutcomeEvidence struct {
	MemoryID    string
	OutcomeID   string
	Decision    string
	Text        string
	Similarity  float64
	Source      string
	TicketID    string
	KnowledgeID string
}

type OutcomeRetriever interface {
	SearchOutcomes(context.Context, string, int, float64) ([]OutcomeEvidence, error)
}

type NeuroForgeOutcomeSink struct {
	baseURL, apiKey string
	http            *http.Client
}

func NewNeuroForgeOutcomeSink(baseURL, apiKey string, timeout time.Duration) (*NeuroForgeOutcomeSink, error) {
	raw := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid neuroforge URL %q", raw)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &NeuroForgeOutcomeSink{baseURL: raw, apiKey: strings.TrimSpace(apiKey), http: &http.Client{Timeout: timeout}}, nil
}
func (c *NeuroForgeOutcomeSink) LearnOutcome(ctx context.Context, x TicketOutcome) (string, error) {
	body, err := json.Marshal(map[string]any{"outcome_id": x.ID, "run_id": x.RunID, "ticket_id": x.TicketID, "decision": x.Decision, "ticket_input": x.TicketInput, "proposed_reply": x.ProposedReply, "confirmed_reply": x.ConfirmedReply, "category_id": x.CategoryID, "category_name": x.CategoryName, "knowledge_id": x.KnowledgeID, "supersedes_id": x.SupersedesID, "actor": x.Actor, "note": x.Note})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/integrations/outcomes", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", fmt.Errorf("neuroforge outcome learning failed: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Memory struct {
			ID string `json:"id"`
		} `json:"memory"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Memory.ID == "" {
		return "", fmt.Errorf("neuroforge returned no memory id")
	}
	return out.Memory.ID, nil
}

func (c *NeuroForgeOutcomeSink) SearchOutcomes(ctx context.Context, text string, k int, minSimilarity float64) ([]OutcomeEvidence, error) {
	if k <= 0 {
		k = 8
	}
	body, err := json.Marshal(map[string]any{"text": strings.TrimSpace(text), "k": k, "min_similarity": minSimilarity})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/integrations/outcomes/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("neuroforge outcome search failed: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var raw []struct {
		Memory struct {
			ID         string   `json:"id"`
			Text       string   `json:"text"`
			Tags       []string `json:"tags"`
			Provenance struct {
				Source   string `json:"source"`
				SourceID string `json:"source_id"`
			} `json:"provenance"`
		} `json:"memory"`
		Similarity float64 `json:"similarity"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]OutcomeEvidence, 0, len(raw))
	for _, h := range raw {
		e := OutcomeEvidence{MemoryID: h.Memory.ID, OutcomeID: h.Memory.Provenance.SourceID, Text: h.Memory.Text, Similarity: h.Similarity, Source: h.Memory.Provenance.Source}
		for _, tag := range h.Memory.Tags {
			switch {
			case strings.HasPrefix(tag, "outcome:"):
				e.Decision = strings.TrimPrefix(tag, "outcome:")
			case strings.HasPrefix(tag, "ticket:"):
				e.TicketID = strings.TrimPrefix(tag, "ticket:")
			case strings.HasPrefix(tag, "knowledge:"):
				e.KnowledgeID = strings.TrimPrefix(tag, "knowledge:")
			}
		}
		out = append(out, e)
	}
	return out, nil
}
