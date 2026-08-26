package httpapi

import (
	"net/http"
	"time"

	"neuroforge/internal/core"
)

func (s *Server) clusterRequestVote(w http.ResponseWriter, r *http.Request) {
	var req core.ClusterVoteRequest
	if err := decode(r, &req); err != nil {
		s.err(w, 400, err)
		return
	}
	resp, err := s.brain.ClusterVote(req)
	if err != nil {
		s.err(w, 409, err)
		return
	}
	s.json(w, 200, resp)
}

func (s *Server) clusterHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req core.ClusterHeartbeat
	if err := decode(r, &req); err != nil {
		s.err(w, 400, err)
		return
	}
	resp, err := s.brain.ClusterHeartbeat(req)
	if err != nil {
		s.err(w, 409, err)
		return
	}
	s.json(w, 200, resp)
}

func (s *Server) adminTierStorage(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.store.TierMemoryBodies(time.Now().UTC()))
}

func (s *Server) adminMergeIndex(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.CompactIndexSegments()
	if err != nil {
		s.err(w, 500, err)
		return
	}
	s.json(w, 200, out)
}
