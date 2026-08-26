package store

import (
	"errors"
	"time"

	"neuroforge/internal/core"
)

const (
	ClusterFollower  = "follower"
	ClusterCandidate = "candidate"
	ClusterLeader    = "leader"
)

func (s *Store) EffectiveLeaderID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state.Config.Cluster.AutoElection {
		return s.state.Cluster.LeaderID
	}
	if s.state.Config.Cluster.LeaderID != "" {
		return s.state.Config.Cluster.LeaderID
	}
	return s.state.Cluster.LeaderID
}

func (s *Store) initializeClusterRoleLocked() {
	cfg := s.state.Config.Cluster
	if !cfg.Enabled {
		return
	}
	if s.state.Cluster.Term < cfg.Term {
		s.state.Cluster.Term = cfg.Term
	}
	if s.state.Cluster.Role == "" {
		if !cfg.AutoElection && cfg.NodeID == cfg.LeaderID {
			s.state.Cluster.Role = ClusterLeader
			s.state.Cluster.LeaderID = cfg.NodeID
		} else {
			s.state.Cluster.Role = ClusterFollower
			if !cfg.AutoElection {
				s.state.Cluster.LeaderID = cfg.LeaderID
			}
		}
	}
}

func (s *Store) StartElection() (core.ClusterVoteRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.state.Config.Cluster
	if !cfg.Enabled || !cfg.AutoElection {
		return core.ClusterVoteRequest{}, errors.New("automatic cluster election is disabled")
	}
	s.state.Cluster.Term++
	s.state.Cluster.Role = ClusterCandidate
	s.state.Cluster.VotedFor = cfg.NodeID
	s.state.Cluster.LeaderID = ""
	s.state.Cluster.LastHeartbeat = time.Now().UTC()
	if err := s.commitLocked("cluster.state", s.state.Cluster); err != nil {
		return core.ClusterVoteRequest{}, err
	}
	return core.ClusterVoteRequest{Term: s.state.Cluster.Term, CandidateID: cfg.NodeID, LastLogIndex: s.state.Cluster.LastIndex}, nil
}

func (s *Store) GrantVote(req core.ClusterVoteRequest) (core.ClusterVoteResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.state.Config.Cluster
	resp := core.ClusterVoteResponse{Term: s.state.Cluster.Term, VoterID: cfg.NodeID}
	if !cfg.Enabled || !cfg.AutoElection || req.CandidateID == "" || req.Term == 0 {
		return resp, nil
	}
	changed := false
	if req.Term > s.state.Cluster.Term {
		s.state.Cluster.Term = req.Term
		s.state.Cluster.Role = ClusterFollower
		s.state.Cluster.VotedFor = ""
		s.state.Cluster.LeaderID = ""
		changed = true
	}
	if req.Term == s.state.Cluster.Term && req.LastLogIndex >= s.state.Cluster.LastIndex && (s.state.Cluster.VotedFor == "" || s.state.Cluster.VotedFor == req.CandidateID) {
		s.state.Cluster.VotedFor = req.CandidateID
		s.state.Cluster.LastHeartbeat = time.Now().UTC()
		resp.VoteGranted = true
		changed = true
	}
	resp.Term = s.state.Cluster.Term
	if changed {
		return resp, s.commitLocked("cluster.state", s.state.Cluster)
	}
	return resp, nil
}

func (s *Store) AcceptHeartbeat(h core.ClusterHeartbeat) (core.ClusterHeartbeatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.state.Config.Cluster
	resp := core.ClusterHeartbeatResponse{Term: s.state.Cluster.Term, NodeID: cfg.NodeID, LastIndex: s.state.Cluster.LastIndex, CommitIndex: s.state.Cluster.CommitIndex}
	if !cfg.Enabled || h.LeaderID == "" || h.Term == 0 {
		return resp, nil
	}
	if h.Term < s.state.Cluster.Term {
		return resp, nil
	}
	higherTerm := h.Term > s.state.Cluster.Term
	persist := higherTerm || s.state.Cluster.Role != ClusterFollower || s.state.Cluster.LeaderID != h.LeaderID
	s.state.Cluster.Term = h.Term
	s.state.Cluster.Role = ClusterFollower
	s.state.Cluster.LeaderID = h.LeaderID
	if higherTerm {
		s.state.Cluster.VotedFor = ""
	}
	s.state.Cluster.LastHeartbeat = time.Now().UTC()
	resp.Term = h.Term
	resp.Accepted = true
	if persist {
		if err := s.commitLocked("cluster.state", s.state.Cluster); err != nil {
			return resp, err
		}
	}
	return resp, nil
}

func (s *Store) BecomeLeader(term uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.state.Config.Cluster
	if !cfg.Enabled || !cfg.AutoElection {
		return errors.New("automatic cluster election is disabled")
	}
	if term != s.state.Cluster.Term {
		return errors.New("cannot become leader for stale term")
	}
	s.state.Cluster.Role = ClusterLeader
	s.state.Cluster.LeaderID = cfg.NodeID
	s.state.Cluster.VotedFor = cfg.NodeID
	s.state.Cluster.LastHeartbeat = time.Now().UTC()
	return s.commitLocked("cluster.state", s.state.Cluster)
}

func (s *Store) StepDown(term uint64, leaderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if term < s.state.Cluster.Term {
		return nil
	}
	s.state.Cluster.Term = term
	s.state.Cluster.Role = ClusterFollower
	s.state.Cluster.LeaderID = leaderID
	s.state.Cluster.VotedFor = ""
	s.state.Cluster.LastHeartbeat = time.Now().UTC()
	return s.commitLocked("cluster.state", s.state.Cluster)
}

func (s *Store) TouchLeaderHeartbeat() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Cluster.Role != ClusterLeader {
		return nil
	}
	s.state.Cluster.LastHeartbeat = time.Now().UTC()
	return nil // heartbeat timestamps are ephemeral on leaders; avoid WAL churn.
}
