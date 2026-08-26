package brain

import (
	"strings"
	"time"

	"neuroforge/internal/core"
)

type researchTrace struct {
	e      *Engine
	runID  string
	goalID string
}

func (e *Engine) newResearchTrace(goal *core.Goal) (*researchTrace, *core.ResearchRun, error) {
	run, err := e.store.StartResearchRun(goal.ID, goal.Title)
	if err != nil {
		return nil, nil, err
	}
	t := &researchTrace{e: e, runID: run.ID, goalID: goal.ID}
	t.emit(core.ResearchEvent{Type: "run.started", Phase: "run", Status: "running", Title: goal.Title, Message: "Research-Lauf gestartet"})
	return t, run, nil
}

func (t *researchTrace) emit(ev core.ResearchEvent) {
	if t == nil || t.e == nil || t.runID == "" {
		return
	}
	if ev.RunID == "" {
		ev.RunID = t.runID
	}
	if ev.GoalID == "" {
		ev.GoalID = t.goalID
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	if ev.Status == "" {
		ev.Status = "ok"
	}
	_, _ = t.e.store.AddResearchEvent(t.runID, ev)
}

func (t *researchTrace) finish(status, lastError string) {
	if t == nil || t.e == nil || t.runID == "" {
		return
	}
	msg := "Research-Lauf abgeschlossen"
	evStatus := "ok"
	if status == "failed" || status == "cancelled" {
		msg = "Research-Lauf beendet: " + status
		evStatus = "error"
	} else if status == "completed_with_errors" {
		msg = "Research-Lauf mit Warnungen abgeschlossen"
		evStatus = "warn"
	}
	if strings.TrimSpace(lastError) != "" {
		msg += " · " + shortPreview(lastError, 220)
	}
	t.emit(core.ResearchEvent{Type: "run.finished", Phase: "run", Status: evStatus, Message: msg})
	_, _ = t.e.store.FinishResearchRun(t.runID, status, lastError)
}

func shortPreview(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if max <= 0 {
		max = 240
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func claimPreview(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" {
		return ""
	}
	// Prefer the first complete sentence when it is informative, otherwise use
	// a bounded excerpt. This is a transparent claim candidate, not an LLM-made
	// fact assertion; verification still comes from dedup/corroboration.
	r := []rune(s)
	cut := -1
	for i, ch := range r {
		if i >= 60 && (ch == '.' || ch == '!' || ch == '?') {
			cut = i + 1
			break
		}
		if i >= 260 {
			break
		}
	}
	if cut > 0 {
		return string(r[:cut])
	}
	return shortPreview(s, 260)
}
