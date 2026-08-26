package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"neuroforge/internal/core"
)

type clusterLogRecord struct {
	Kind     string             `json:"kind"`
	Entry    *core.ClusterEntry `json:"entry,omitempty"`
	Decision *ClusterDecision   `json:"decision,omitempty"`
	Time     time.Time          `json:"time"`
}

type ClusterLogStats struct {
	Segments  int    `json:"segments"`
	Bytes     int64  `json:"bytes"`
	Records   int    `json:"records"`
	Entries   int    `json:"entries"`
	Decisions int    `json:"decisions"`
	Active    string `json:"active,omitempty"`
	LastIndex uint64 `json:"last_index"`
	LastTerm  uint64 `json:"last_term"`
}

type ClusterLog struct {
	mu           sync.Mutex
	dir          string
	maxBytes     int64
	seq          int
	active       string
	size         int64
	stats        ClusterLogStats
	seenEntry    map[string]bool
	seenDecision map[string]string
}

func openClusterLog(dir string, maxBytes int64) (*ClusterLog, error) {
	if maxBytes < 1<<20 {
		maxBytes = 64 << 20
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	l := &ClusterLog{dir: dir, maxBytes: maxBytes, seenEntry: map[string]bool{}, seenDecision: map[string]string{}}
	if err := l.scan(); err != nil {
		return nil, err
	}
	return l, nil
}

func clusterLogName(seq int) string { return fmt.Sprintf("log-%06d.jsonl", seq) }
func parseClusterLogSeq(name string) (int, bool) {
	if !strings.HasPrefix(name, "log-") || !strings.HasSuffix(name, ".jsonl") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "log-"), ".jsonl"))
	return n, err == nil && n > 0
}

func (l *ClusterLog) scan() error {
	ents, err := os.ReadDir(l.dir)
	if err != nil {
		return err
	}
	type item struct {
		seq  int
		path string
	}
	var items []item
	for _, e := range ents {
		if !e.IsDir() {
			if n, ok := parseClusterLogSeq(e.Name()); ok {
				items = append(items, item{n, filepath.Join(l.dir, e.Name())})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].seq < items[j].seq })
	for _, it := range items {
		f, err := os.Open(it.path)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 32<<20)
		for sc.Scan() {
			var r clusterLogRecord
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				_ = f.Close()
				return err
			}
			l.observe(r)
		}
		if err := sc.Err(); err != nil {
			_ = f.Close()
			return err
		}
		_ = f.Close()
		if st, err := os.Stat(it.path); err == nil {
			l.stats.Bytes += st.Size()
		}
		l.stats.Segments++
		l.seq = it.seq
	}
	if l.seq == 0 {
		l.seq = 1
	}
	l.active = filepath.Join(l.dir, clusterLogName(l.seq))
	if st, err := os.Stat(l.active); err == nil {
		l.size = st.Size()
	}
	if l.size >= l.maxBytes {
		l.seq++
		l.active = filepath.Join(l.dir, clusterLogName(l.seq))
		l.size = 0
	}
	l.stats.Active = filepath.Base(l.active)
	return nil
}

func (l *ClusterLog) observe(r clusterLogRecord) {
	l.stats.Records++
	if r.Entry != nil {
		l.stats.Entries++
		l.seenEntry[r.Entry.ID] = true
		if r.Entry.Index > l.stats.LastIndex || (r.Entry.Index == l.stats.LastIndex && r.Entry.Term > l.stats.LastTerm) {
			l.stats.LastIndex, l.stats.LastTerm = r.Entry.Index, r.Entry.Term
		}
	}
	if r.Decision != nil {
		l.stats.Decisions++
		l.seenDecision[r.Decision.EntryID] = r.Decision.Decision
	}
}

func (l *ClusterLog) append(r clusterLogRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.Entry != nil && l.seenEntry[r.Entry.ID] {
		return nil
	}
	if r.Decision != nil {
		if d, ok := l.seenDecision[r.Decision.EntryID]; ok && d == r.Decision.Decision {
			return nil
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if l.size > 0 && l.size+int64(len(b)) > l.maxBytes {
		l.seq++
		l.active = filepath.Join(l.dir, clusterLogName(l.seq))
		l.size = 0
		l.stats.Segments++
	}
	f, err := os.OpenFile(l.active, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if l.size == 0 && l.stats.Segments == 0 {
		l.stats.Segments = 1
	}
	l.size += int64(len(b))
	l.stats.Bytes += int64(len(b))
	l.stats.Active = filepath.Base(l.active)
	l.observe(r)
	return nil
}
func (l *ClusterLog) AppendEntry(e core.ClusterEntry) error {
	return l.append(clusterLogRecord{Kind: "entry", Entry: &e, Time: time.Now().UTC()})
}
func (l *ClusterLog) AppendDecision(d ClusterDecision) error {
	return l.append(clusterLogRecord{Kind: "decision", Decision: &d, Time: time.Now().UTC()})
}
func (l *ClusterLog) Stats() ClusterLogStats { l.mu.Lock(); defer l.mu.Unlock(); return l.stats }
func (l *ClusterLog) Close() error           { return nil }

func (s *Store) ensureClusterLog() (*ClusterLog, error) {
	cfg := s.Config().Cluster
	s.clusterLogMu.Lock()
	defer s.clusterLogMu.Unlock()
	if s.clusterLog != nil {
		return s.clusterLog, nil
	}
	if !cfg.Enabled {
		return nil, errors.New("cluster is disabled")
	}
	l, err := openClusterLog(filepath.Join(s.clusterDir(), "log"), cfg.LogSegmentBytes)
	if err != nil {
		return nil, err
	}
	s.clusterLog = l
	return l, nil
}
func (s *Store) appendClusterLogEntry(e core.ClusterEntry) error {
	l, err := s.ensureClusterLog()
	if err != nil {
		return err
	}
	return l.AppendEntry(e)
}
func (s *Store) appendClusterLogDecision(d ClusterDecision) error {
	l, err := s.ensureClusterLog()
	if err != nil {
		return err
	}
	return l.AppendDecision(d)
}
func (s *Store) ClusterLogStats() ClusterLogStats {
	l, err := s.ensureClusterLog()
	if err != nil {
		return ClusterLogStats{}
	}
	return l.Stats()
}
