package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"neuroforge/internal/brain"
	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

func (s *Server) goalsList(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.store.GoalsSnapshot())
}

func (s *Server) goalsCreate(w http.ResponseWriter, r *http.Request) {
	var g core.Goal
	if err := decode(r, &g); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.store.UpsertGoal(&g); err != nil {
		if errors.Is(err, store.ErrDuplicateGoal) {
			s.err(w, http.StatusConflict, err)
			return
		}
		s.err(w, 400, err)
		return
	}
	s.json(w, http.StatusCreated, g)
}

func (s *Server) goalsGet(w http.ResponseWriter, r *http.Request) {
	g, ok := s.store.GetGoal(r.PathValue("id"))
	if !ok {
		s.err(w, 404, errors.New("goal not found"))
		return
	}
	s.json(w, 200, g)
}

func (s *Server) goalsPut(w http.ResponseWriter, r *http.Request) {
	var g core.Goal
	if err := decode(r, &g); err != nil {
		s.err(w, 400, err)
		return
	}
	g.ID = r.PathValue("id")
	if old, ok := s.store.GetGoal(g.ID); ok && g.CreatedAt.IsZero() {
		g.CreatedAt = old.CreatedAt
	}
	if err := s.store.UpsertGoal(&g); err != nil {
		s.err(w, 400, err)
		return
	}
	s.json(w, 200, g)
}

func (s *Server) goalsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	goal, _ := s.store.GetGoal(id)
	if err := s.store.DeleteGoal(id); err != nil {
		s.err(w, 404, err)
		return
	}
	meta := map[string]string{"goal_id": id}
	if goal != nil {
		meta["title"] = goal.Title
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "goal.deleted", Summary: "Goal deleted", Reason: "administrator/user deleted goal", Actor: "goal-control", Metadata: meta})
	s.json(w, 200, map[string]bool{"ok": true})
}

func (s *Server) goalPause(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.PauseGoal(r.PathValue("id"))
	if err != nil {
		s.err(w, 400, err)
		return
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "goal.paused", Summary: "Goal paused: " + g.Title, Reason: "manual pause", Actor: "goal-control", Metadata: map[string]string{"goal_id": g.ID}})
	s.json(w, 200, g)
}

func (s *Server) goalResume(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.ResumeGoal(r.PathValue("id"))
	if err != nil {
		s.err(w, 400, err)
		return
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "goal.resumed", Summary: "Goal resumed: " + g.Title, Reason: "manual resume", Actor: "goal-control", Metadata: map[string]string{"goal_id": g.ID}})
	s.json(w, 200, g)
}

func (s *Server) goalCycle(w http.ResponseWriter, r *http.Request) {
	cycle, err := s.brain.RunGoalCycle(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, brain.ErrGoalCycleInProgress) {
			s.err(w, http.StatusConflict, err)
			return
		}
		s.err(w, http.StatusBadRequest, err)
		return
	}
	s.json(w, 200, cycle)
}

func (s *Server) learningCycles(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	s.json(w, 200, s.store.RecentLearningCycles(limit))
}

func (s *Server) conflicts(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.store.ConflictsSnapshot())
}

func (s *Server) adminRetention(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.RunRetention(time.Now().UTC())
	if err != nil {
		s.err(w, 500, err)
		return
	}
	s.json(w, 200, out)
}

func (s *Server) adminAutonomy(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.brain.RunAutonomy(r.Context()))
}

func (s *Server) adminRebalance(w http.ResponseWriter, r *http.Request) {
	var q struct {
		DryRun bool `json:"dry_run"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &q); err != nil {
			s.err(w, 400, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	s.json(w, 200, s.brain.RebalanceShards(ctx, q.DryRun))
}

func (s *Server) adminCheckpoint(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ForceCheckpoint(); err != nil {
		s.err(w, 500, err)
		return
	}
	s.json(w, 200, map[string]any{"ok": true, "wal": s.store.WALStatus()})
}

func (s *Server) adminWAL(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.store.WALStatus())
}

func (s *Server) adminResolveConflict(w http.ResponseWriter, r *http.Request) {
	var q struct {
		TruthKey string `json:"truth_key"`
		WinnerID string `json:"winner_id"`
	}
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	if q.TruthKey == "" || q.WinnerID == "" {
		s.err(w, 400, errors.New("truth_key and winner_id are required"))
		return
	}
	if err := s.store.ResolveConflict(q.TruthKey, q.WinnerID); err != nil {
		s.err(w, 400, err)
		return
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "conflict.resolved", MemoryID: q.WinnerID, Summary: "Knowledge conflict resolved by administrator", Reason: "winner selected for truth key", Actor: "admin", Metadata: map[string]string{"truth_key": q.TruthKey}})
	s.json(w, 200, map[string]bool{"ok": true})
}
