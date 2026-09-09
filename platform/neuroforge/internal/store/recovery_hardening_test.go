package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neuroforge/internal/core"
)

func TestNewFailsClosedOnCorruptAuthoritativeState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, v161JobCompactionMarker), []byte("compacted=0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"config":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil || !strings.Contains(err.Error(), "load authoritative state checkpoint") {
		t.Fatalf("New error=%v, want authoritative state corruption failure", err)
	}
}

func TestNewFailsClosedOnCorruptAuthoritativeSecrets(t *testing.T) {
	dir := t.TempDir()
	st := core.PersistedState{
		Config:       core.DefaultConfig(),
		Memories:     map[string]*core.Memory{},
		Synapses:     map[string]*core.Synapse{},
		Jobs:         map[string]*core.Job{},
		Goals:        map[string]*core.Goal{},
		Sources:      map[string]*core.KnowledgeSource{},
		ResearchRuns: map[string]*core.ResearchRun{},
	}
	if err := writeAtomic(filepath.Join(dir, "state.json"), 0600, &st); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.json"), []byte(`{"admin_token":"unterminated}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil || !strings.Contains(err.Error(), "load authoritative secrets") {
		t.Fatalf("New error=%v, want authoritative secrets corruption failure", err)
	}
}

func TestLoadJSONRejectsTrailingJSONValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"revision":1}{"revision":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Store{}
	var st core.PersistedState
	if err := s.loadJSON(path, &st); err == nil || !strings.Contains(err.Error(), "unexpected trailing JSON token") {
		t.Fatalf("loadJSON error=%v, want trailing JSON rejection", err)
	}
}

func TestReplayWALCompactsCompletedRelinkPayloadImmediately(t *testing.T) {
	dir := t.TempDir()
	walDir := filepath.Join(dir, "wal")
	if err := os.MkdirAll(walDir, 0700); err != nil {
		t.Fatal(err)
	}
	job := core.Job{
		ID:        "job_done_relink",
		Type:      "vector.relink",
		Status:    "done",
		Payload:   json.RawMessage(`{"target":{"vector":[1,2,3]},"candidates":[{"vector":[4,5,6]}]}`),
		Result:    json.RawMessage(`{"edges":[{"a":"a","b":"b","weight":0.9}]}`),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	ev := walEvent{Revision: 1, Time: time.Now().UTC(), Type: "job.upsert", Data: data}
	f, err := os.OpenFile(filepath.Join(walDir, "wal-active.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	bw := bufio.NewWriter(f)
	if err := json.NewEncoder(bw).Encode(ev); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, ok := s.Job(job.ID)
	if !ok {
		t.Fatal("replayed job missing")
	}
	if len(got.Payload) != 0 || len(got.Result) != 0 {
		t.Fatalf("replayed completed relink retained vector blobs: payload=%d result=%d", len(got.Payload), len(got.Result))
	}
}

func TestFreshDirectoryDoesNotConsumeV161CompactionMarker(t *testing.T) {
	dir := t.TempDir()
	if n, err := compactLegacyTerminalRelinkCheckpoint(dir); err != nil || n != 0 {
		t.Fatalf("compact fresh dir = %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, v161JobCompactionMarker)); !os.IsNotExist(err) {
		t.Fatalf("fresh directory unexpectedly created migration marker: %v", err)
	}
}
