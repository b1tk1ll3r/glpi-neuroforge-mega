package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"neuroforge/internal/core"
)

func (s *Server) clusterPrepare(w http.ResponseWriter, r *http.Request) {
	var entry core.ClusterEntry
	if err := decode(r, &entry); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.brain.ClusterPrepare(entry); err != nil {
		s.err(w, 409, err)
		return
	}
	s.json(w, 200, map[string]any{"ok": true, "entry_id": entry.ID, "index": entry.Index})
}

func (s *Server) clusterCommit(w http.ResponseWriter, r *http.Request) {
	var entry core.ClusterEntry
	if err := decode(r, &entry); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.brain.ClusterCommit(entry); err != nil {
		s.err(w, 409, err)
		return
	}
	s.json(w, 200, map[string]any{"ok": true, "entry_id": entry.ID, "index": entry.Index})
}

func (s *Server) clusterAbort(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID string `json:"id"`
	}
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	if q.ID == "" {
		s.err(w, 400, errors.New("id required"))
		return
	}
	if err := s.brain.ClusterAbort(q.ID); err != nil {
		s.err(w, 500, err)
		return
	}
	s.json(w, 200, map[string]bool{"ok": true})
}

func (s *Server) clusterProposeMemory(w http.ResponseWriter, r *http.Request) {
	var m core.Memory
	if err := decode(r, &m); err != nil {
		s.err(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.brain.ClusterProposeMemory(ctx, &m); err != nil {
		s.err(w, 503, err)
		return
	}
	s.json(w, 201, m)
}

func (s *Server) clusterDecision(w http.ResponseWriter, r *http.Request) {
	d, ok := s.store.ClusterDecision(r.PathValue("id"))
	if !ok {
		s.err(w, 404, errors.New("cluster decision not found"))
		return
	}
	s.json(w, 200, d)
}

func (s *Server) clusterStatus(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.store.ClusterStatus())
}

func (s *Server) adminClusterRepair(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	s.json(w, 200, s.brain.RepairCluster(ctx))
}

func (s *Server) adminCompactSegments(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.CompactMemorySegments()
	if err != nil {
		s.err(w, 500, err)
		return
	}
	if err := s.store.ForceCheckpoint(); err != nil {
		s.err(w, 500, err)
		return
	}
	s.json(w, 200, stats)
}

func (s *Server) adminStorageStatus(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, map[string]any{
		"wal":             s.store.WALStatus(),
		"memory_segments": s.store.SegmentStats(),
		"index_snapshot":  s.store.IndexSnapshotStatus(),
		"tiering":         s.store.TieringStatus(),
		"cluster_log":     s.store.ClusterLogStats(),
	})
}
