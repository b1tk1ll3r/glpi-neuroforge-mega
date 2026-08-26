package httpapi

import (
	"net/http"
	"strconv"
)

func (s *Server) goalResearchLive(w http.ResponseWriter, r *http.Request) {
	goalID := r.PathValue("id")
	run, ok := s.store.LatestResearchRun(goalID)
	if !ok {
		s.json(w, http.StatusOK, map[string]any{"run": nil, "events": []any{}, "reset": false})
		return
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	clientRunID := r.URL.Query().Get("run_id")
	reset := clientRunID != "" && clientRunID != run.ID
	if reset {
		after = 0
	}
	events := run.Events
	if after > 0 {
		filtered := events[:0:0]
		for _, ev := range events {
			if ev.Seq > after {
				filtered = append(filtered, ev)
			}
		}
		events = filtered
	}
	// Return metadata separately from the event delta so a 1s UI poll stays
	// bounded even when the persisted run keeps a larger audit tail.
	run.Events = nil
	s.json(w, http.StatusOK, map[string]any{"run": run, "events": events, "reset": reset})
}

func (s *Server) goalResearchHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	runs := s.store.ResearchRunsSnapshot(r.PathValue("id"), limit)
	for i := range runs {
		// History cards need summary/stats, not hundreds of event rows. The live
		// endpoint exposes the latest run's detailed trace on demand.
		runs[i].Events = nil
	}
	s.json(w, http.StatusOK, runs)
}
