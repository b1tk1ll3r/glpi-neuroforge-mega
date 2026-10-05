package brainactivity

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Hit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score,omitempty"`
}

type event struct {
	Type     string         `json:"type"`
	Source   string         `json:"source"`
	Query    string         `json:"query,omitempty"`
	Message  string         `json:"message,omitempty"`
	Hits     []Hit          `json:"hits,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

var sender = newSender()

type asyncSender struct {
	once sync.Once
	url  string
	key  string
	ch   chan event
	http *http.Client
}

func newSender() *asyncSender {
	return &asyncSender{ch: make(chan event, 128), http: &http.Client{Timeout: 3 * time.Second}}
}

// EmitSearch is fail-open and has no effect unless BRAIN_ACTIVITY_URL is set.
// It never blocks the ticket-processing path and silently drops telemetry when
// the optional visualization is unavailable or the local queue is full.
func EmitSearch(source, query string, hits []Hit, duration time.Duration) {
	sender.once.Do(sender.start)
	if sender.url == "" {
		return
	}
	query = strings.TrimSpace(query)
	if len([]rune(query)) > 4000 {
		query = string([]rune(query)[:4000])
	}
	e := event{
		Type: "knowledge.search", Source: source, Query: query,
		Message: "Wissenssuche aus " + source,
		Hits:    hits, Metadata: map[string]any{"duration_ms": duration.Milliseconds(), "result_count": len(hits)},
	}
	select {
	case sender.ch <- e:
	default:
	}
}

func (s *asyncSender) start() {
	s.url = strings.TrimSpace(os.Getenv("BRAIN_ACTIVITY_URL"))
	s.key = strings.TrimSpace(os.Getenv("BRAIN_ACTIVITY_API_KEY"))
	if s.url == "" {
		return
	}
	go func() {
		for e := range s.ch {
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(b))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			if s.key != "" {
				req.Header.Set("Authorization", "Bearer "+s.key)
			}
			resp, err := s.http.Do(req)
			if err == nil {
				// Drain a bounded amount so the keep-alive connection can be reused.
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
			}
		}
	}()
}
