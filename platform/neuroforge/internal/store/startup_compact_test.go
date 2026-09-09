package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"neuroforge/internal/core"
)

func TestCompactLegacyTerminalRelinkCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st := core.PersistedState{Config: core.DefaultConfig(), Jobs: map[string]*core.Job{}, Synapses: map[string]*core.Synapse{}, Goals: map[string]*core.Goal{}}
	st.Jobs["done"] = &core.Job{ID: "done", Type: "vector.relink", Status: "done", Payload: json.RawMessage(`{"target":[1,2,3]}`), Result: json.RawMessage(`{"ok":true}`)}
	st.Jobs["queued"] = &core.Job{ID: "queued", Type: "vector.relink", Status: "queued", Payload: json.RawMessage(`{"target":[4,5,6]}`)}
	st.Jobs["other"] = &core.Job{ID: "other", Type: "model.chat", Status: "done", Payload: json.RawMessage(`{"prompt":"keep"}`), Result: json.RawMessage(`{"text":"keep"}`)}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}

	n, err := compactLegacyTerminalRelinkCheckpoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("compacted=%d want 1", n)
	}

	var got core.PersistedState
	bb, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bb, &got); err != nil {
		t.Fatal(err)
	}
	cleared := func(b json.RawMessage) bool { return len(b) == 0 || string(b) == "null" }
	if !cleared(got.Jobs["done"].Payload) || !cleared(got.Jobs["done"].Result) {
		t.Fatalf("done relink blobs not cleared: payload=%q result=%q", got.Jobs["done"].Payload, got.Jobs["done"].Result)
	}
	if len(got.Jobs["queued"].Payload) == 0 {
		t.Fatalf("queued relink payload must be retained")
	}
	if len(got.Jobs["other"].Payload) == 0 || len(got.Jobs["other"].Result) == 0 {
		t.Fatalf("unrelated job blobs must be retained")
	}

	if n2, err := compactLegacyTerminalRelinkCheckpoint(dir); err != nil || n2 != 0 {
		t.Fatalf("second migration = %d, %v", n2, err)
	}
}
