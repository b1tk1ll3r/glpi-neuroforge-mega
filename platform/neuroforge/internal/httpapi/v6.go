package httpapi

import "net/http"

func (s *Server) adminDiskANNStatus(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.store.DiskANNStatus())
}

func (s *Server) adminDiskANNBuild(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.RebuildDiskANN()
	if err != nil {
		s.err(w, 500, err)
		return
	}
	s.json(w, 200, out)
}
