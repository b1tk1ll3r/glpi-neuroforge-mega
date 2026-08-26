package httpapi

import (
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var requestDurationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

type httpMetricKey struct {
	Method string
	Route  string
	Code   int
}

type httpMetric struct {
	Count   uint64
	Bytes   uint64
	Sum     float64
	Buckets [len(requestDurationBuckets)]uint64
}

type metricsRegistry struct {
	mu      sync.RWMutex
	started time.Time
	http    map[httpMetricKey]*httpMetric
}

func newMetricsRegistry() *metricsRegistry {
	return &metricsRegistry{started: time.Now(), http: map[httpMetricKey]*httpMetric{}}
}

func normalizeMetricRoute(r *http.Request) string {
	route := strings.TrimSpace(r.Pattern)
	if route == "" {
		return "unmatched"
	}
	if strings.HasPrefix(route, r.Method+" ") {
		route = strings.TrimSpace(strings.TrimPrefix(route, r.Method+" "))
	}
	return route
}

func (m *metricsRegistry) observeHTTP(method, route string, code int, bytes int64, d time.Duration) {
	if code == 0 {
		code = http.StatusOK
	}
	key := httpMetricKey{Method: method, Route: route, Code: code}
	sec := d.Seconds()
	m.mu.Lock()
	x := m.http[key]
	if x == nil {
		x = &httpMetric{}
		m.http[key] = x
	}
	x.Count++
	if bytes > 0 {
		x.Bytes += uint64(bytes)
	}
	x.Sum += sec
	for i, upper := range requestDurationBuckets {
		if sec <= upper {
			x.Buckets[i]++
		}
	}
	m.mu.Unlock()
}

type httpDashboardRoute struct {
	Method        string  `json:"method"`
	Route         string  `json:"route"`
	Requests      uint64  `json:"requests"`
	Errors        uint64  `json:"errors"`
	AverageMS     float64 `json:"average_ms"`
	ApproxP95MS   float64 `json:"approx_p95_ms"`
	ResponseBytes uint64  `json:"response_bytes"`
}

type httpDashboardSnapshot struct {
	StartedAt     time.Time            `json:"started_at"`
	UptimeSeconds float64              `json:"uptime_seconds"`
	Requests      uint64               `json:"requests"`
	Errors4xx     uint64               `json:"errors_4xx"`
	Errors5xx     uint64               `json:"errors_5xx"`
	ResponseBytes uint64               `json:"response_bytes"`
	AverageMS     float64              `json:"average_ms"`
	ApproxP95MS   float64              `json:"approx_p95_ms"`
	Routes        []httpDashboardRoute `json:"routes"`
}

func approxP95(count uint64, buckets []uint64) float64 {
	if count == 0 {
		return 0
	}
	target := uint64(math.Ceil(float64(count) * 0.95))
	for i, n := range buckets {
		if n >= target {
			return requestDurationBuckets[i] * 1000
		}
	}
	return requestDurationBuckets[len(requestDurationBuckets)-1] * 1000
}

func (m *metricsRegistry) dashboardSnapshot() httpDashboardSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := httpDashboardSnapshot{StartedAt: m.started, UptimeSeconds: time.Since(m.started).Seconds()}
	type agg struct {
		count, errors, bytes uint64
		sum                  float64
		buckets              [len(requestDurationBuckets)]uint64
	}
	byRoute := map[[2]string]*agg{}
	var allBuckets [len(requestDurationBuckets)]uint64
	for key, x := range m.http {
		out.Requests += x.Count
		out.ResponseBytes += x.Bytes
		out.AverageMS += x.Sum * 1000
		if key.Code >= 400 && key.Code < 500 {
			out.Errors4xx += x.Count
		}
		if key.Code >= 500 {
			out.Errors5xx += x.Count
		}
		for i := range allBuckets {
			allBuckets[i] += x.Buckets[i]
		}
		k := [2]string{key.Method, key.Route}
		a := byRoute[k]
		if a == nil {
			a = &agg{}
			byRoute[k] = a
		}
		a.count += x.Count
		a.bytes += x.Bytes
		a.sum += x.Sum
		if key.Code >= 400 {
			a.errors += x.Count
		}
		for i := range a.buckets {
			a.buckets[i] += x.Buckets[i]
		}
	}
	if out.Requests > 0 {
		out.AverageMS /= float64(out.Requests)
	}
	out.ApproxP95MS = approxP95(out.Requests, allBuckets[:])
	for k, a := range byRoute {
		r := httpDashboardRoute{Method: k[0], Route: k[1], Requests: a.count, Errors: a.errors, ResponseBytes: a.bytes}
		if a.count > 0 {
			r.AverageMS = a.sum * 1000 / float64(a.count)
		}
		r.ApproxP95MS = approxP95(a.count, a.buckets[:])
		out.Routes = append(out.Routes, r)
	}
	sort.Slice(out.Routes, func(i, j int) bool {
		if out.Routes[i].Requests == out.Routes[j].Requests {
			return out.Routes[i].Route < out.Routes[j].Route
		}
		return out.Routes[i].Requests > out.Routes[j].Requests
	})
	if len(out.Routes) > 20 {
		out.Routes = out.Routes[:20]
	}
	return out
}

type runtimeSnapshot struct {
	Goroutines   int    `json:"goroutines"`
	HeapAlloc    uint64 `json:"heap_alloc_bytes"`
	HeapInuse    uint64 `json:"heap_inuse_bytes"`
	HeapObjects  uint64 `json:"heap_objects"`
	SysBytes     uint64 `json:"sys_bytes"`
	NumGC        uint32 `json:"gc_cycles_total"`
	PauseTotalNS uint64 `json:"gc_pause_total_ns"`
}

func currentRuntimeSnapshot() runtimeSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return runtimeSnapshot{
		Goroutines: runtime.NumGoroutine(), HeapAlloc: ms.HeapAlloc, HeapInuse: ms.HeapInuse,
		HeapObjects: ms.HeapObjects, SysBytes: ms.Sys, NumGC: ms.NumGC, PauseTotalNS: ms.PauseTotalNs,
	}
}

func metricEscape(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
}

func metricLabels(labels ...string) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(labels); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(labels[i])
		b.WriteString(`="`)
		b.WriteString(metricEscape(labels[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func promHeader(b *strings.Builder, name, help, typ string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func promSample(b *strings.Builder, name string, value any, labels ...string) {
	fmt.Fprintf(b, "%s%s %v\n", name, metricLabels(labels...), value)
}

func boolFloat(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Server) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	sec := s.store.Secrets()
	token := bearer(r)
	if sec.MetricsToken == "" || (!secureEqual(token, sec.MetricsToken) && !secureEqual(token, sec.AdminToken)) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="neuroforge-metrics"`)
		s.err(w, http.StatusUnauthorized, fmt.Errorf("invalid metrics token"))
		return
	}

	st := s.store.ObservabilitySnapshot()
	costs := s.cost.Totals()
	cfg := s.store.Config()
	rt := currentRuntimeSnapshot()
	httpSnap := s.metrics.dashboardSnapshot()
	var b strings.Builder
	b.Grow(24 << 10)

	promHeader(&b, "neuroforge_up", "Whether the NeuroForge process is serving metrics.", "gauge")
	promSample(&b, "neuroforge_up", 1)
	promHeader(&b, "neuroforge_uptime_seconds", "Process uptime in seconds.", "gauge")
	promSample(&b, "neuroforge_uptime_seconds", strconv.FormatFloat(httpSnap.UptimeSeconds, 'f', 3, 64))
	promHeader(&b, "neuroforge_revision", "Current persisted NeuroForge state revision.", "gauge")
	promSample(&b, "neuroforge_revision", st.Revision)

	promHeader(&b, "neuroforge_memories", "Current number of memory records.", "gauge")
	promSample(&b, "neuroforge_memories", st.Memories)
	promHeader(&b, "neuroforge_sources", "Current number of registered knowledge sources.", "gauge")
	promSample(&b, "neuroforge_sources", st.Sources)
	promHeader(&b, "neuroforge_synapses", "Current number of synapse edges.", "gauge")
	promSample(&b, "neuroforge_synapses", st.Synapses)
	promHeader(&b, "neuroforge_goals", "Current number of goal records.", "gauge")
	promSample(&b, "neuroforge_goals", st.Goals)
	promHeader(&b, "neuroforge_learning_cycles", "Current number of retained learning-cycle records.", "gauge")
	promSample(&b, "neuroforge_learning_cycles", st.LearningCycles)
	promHeader(&b, "neuroforge_knowledge_events", "Current number of retained explainability/knowledge events.", "gauge")
	promSample(&b, "neuroforge_knowledge_events", st.KnowledgeEvents)

	promHeader(&b, "neuroforge_jobs", "Current worker jobs by bounded status class.", "gauge")
	promSample(&b, "neuroforge_jobs", st.JobsQueued, "status", "queued")
	promSample(&b, "neuroforge_jobs", st.JobsClaimed, "status", "claimed")
	promSample(&b, "neuroforge_jobs", st.JobsDone, "status", "done")
	promSample(&b, "neuroforge_jobs", st.JobsFailed, "status", "failed")

	promHeader(&b, "neuroforge_hnsw_nodes", "Current number of vectors in hot HNSW indexes.", "gauge")
	promSample(&b, "neuroforge_hnsw_nodes", st.HNSWNodes)
	promHeader(&b, "neuroforge_hnsw_dimensions", "Number of active HNSW dimensionality indexes.", "gauge")
	promSample(&b, "neuroforge_hnsw_dimensions", st.HNSWDimensions)
	promHeader(&b, "neuroforge_disk_pq_items", "Current number of items indexed in disk PQ.", "gauge")
	promSample(&b, "neuroforge_disk_pq_items", st.DiskPQItems)
	promHeader(&b, "neuroforge_disk_pq_bytes", "Disk bytes used by disk PQ indexes.", "gauge")
	promSample(&b, "neuroforge_disk_pq_bytes", st.DiskPQBytes)
	promHeader(&b, "neuroforge_index_mode", "Active vector index mode as a one-hot info gauge.", "gauge")
	promSample(&b, "neuroforge_index_mode", 1, "mode", st.IndexMode)
	promHeader(&b, "neuroforge_index_delta_segments", "Current HNSW delta segment count.", "gauge")
	promSample(&b, "neuroforge_index_delta_segments", st.IndexDeltaCount)
	promHeader(&b, "neuroforge_disk_pq_building", "Whether a disk PQ rebuild is currently running.", "gauge")
	promSample(&b, "neuroforge_disk_pq_building", boolFloat(st.DiskANNBuilding))
	vj := s.store.VectorJournalStats()
	promHeader(&b, "neuroforge_vector_journal_raw_bytes", "Raw vector bytes represented by the rebuildable vector journal.", "gauge")
	promSample(&b, "neuroforge_vector_journal_raw_bytes", vj.VectorRawBytes)
	promHeader(&b, "neuroforge_vector_journal_stored_bytes", "Stored vector payload bytes after raw/DEFLATE/SQAR selection.", "gauge")
	promSample(&b, "neuroforge_vector_journal_stored_bytes", vj.VectorStoredBytes)
	promHeader(&b, "neuroforge_vector_journal_compression_savings_percent", "Vector journal payload savings percent.", "gauge")
	promSample(&b, "neuroforge_vector_journal_compression_savings_percent", strconv.FormatFloat(vj.CompressionSavingsPct, 'f', 3, 64))
	promHeader(&b, "neuroforge_vector_journal_sqar_blocks", "Number of vector journal blocks encoded with SQAR.", "gauge")
	promSample(&b, "neuroforge_vector_journal_sqar_blocks", vj.SQARBlocks)
	promHeader(&b, "neuroforge_vector_journal_compressed_blocks", "Number of compressed vector journal blocks.", "gauge")
	promSample(&b, "neuroforge_vector_journal_compressed_blocks", vj.CompressedBlocks)

	promHeader(&b, "neuroforge_memory_segment_bytes", "Bytes used by authoritative memory segments.", "gauge")
	promSample(&b, "neuroforge_memory_segment_bytes", st.Segments.Bytes)
	promHeader(&b, "neuroforge_memory_segments", "Number of authoritative memory segment files.", "gauge")
	promSample(&b, "neuroforge_memory_segments", st.Segments.Segments)
	promHeader(&b, "neuroforge_memory_segment_records", "Number of records in memory segments.", "gauge")
	promSample(&b, "neuroforge_memory_segment_records", st.Segments.Records)
	promHeader(&b, "neuroforge_memory_segment_tombstones", "Current tombstone count in memory segments.", "gauge")
	promSample(&b, "neuroforge_memory_segment_tombstones", st.Segments.Tombstones)
	promHeader(&b, "neuroforge_memory_mmap_segments", "Current number of mmap-backed sealed segments.", "gauge")
	promSample(&b, "neuroforge_memory_mmap_segments", st.Segments.MmapSegments)

	promHeader(&b, "neuroforge_memory_tier_memories", "Memory bodies by hot/cold tier.", "gauge")
	promSample(&b, "neuroforge_memory_tier_memories", st.HotMemories, "tier", "hot")
	promSample(&b, "neuroforge_memory_tier_memories", st.ColdMemories, "tier", "cold")
	promHeader(&b, "neuroforge_memory_hot_bytes", "Approximate bytes held by hot memory bodies.", "gauge")
	promSample(&b, "neuroforge_memory_hot_bytes", st.HotBytes)
	promHeader(&b, "neuroforge_memory_tier_evictions_total", "Total memory-body evictions from the hot tier.", "counter")
	promSample(&b, "neuroforge_memory_tier_evictions_total", st.TierEvictions)

	promHeader(&b, "neuroforge_page_cache_bytes", "Current page-cache bytes.", "gauge")
	promSample(&b, "neuroforge_page_cache_bytes", st.PageCacheBytes)
	promHeader(&b, "neuroforge_page_cache_max_bytes", "Configured page-cache byte limit.", "gauge")
	promSample(&b, "neuroforge_page_cache_max_bytes", st.PageCacheMaxBytes)
	promHeader(&b, "neuroforge_page_cache_entries", "Current page-cache entries.", "gauge")
	promSample(&b, "neuroforge_page_cache_entries", st.PageCacheEntries)
	promHeader(&b, "neuroforge_page_cache_hits_total", "Total page-cache hits.", "counter")
	promSample(&b, "neuroforge_page_cache_hits_total", st.PageCacheHits)
	promHeader(&b, "neuroforge_page_cache_misses_total", "Total page-cache misses.", "counter")
	promSample(&b, "neuroforge_page_cache_misses_total", st.PageCacheMisses)
	promHeader(&b, "neuroforge_page_cache_evictions_total", "Total page-cache evictions.", "counter")
	promSample(&b, "neuroforge_page_cache_evictions_total", st.PageCacheEvicts)

	promHeader(&b, "neuroforge_wal_events_since_checkpoint", "WAL events written since the last checkpoint.", "gauge")
	promSample(&b, "neuroforge_wal_events_since_checkpoint", st.WALEventsSinceCheckpoint)

	promHeader(&b, "neuroforge_cluster_enabled", "Whether cluster mode is enabled.", "gauge")
	promSample(&b, "neuroforge_cluster_enabled", boolFloat(st.ClusterEnabled))
	promHeader(&b, "neuroforge_cluster_term", "Current cluster election term.", "gauge")
	promSample(&b, "neuroforge_cluster_term", st.ClusterTerm)
	promHeader(&b, "neuroforge_cluster_log_index", "Current cluster last log index.", "gauge")
	promSample(&b, "neuroforge_cluster_log_index", st.ClusterLastIndex)
	promHeader(&b, "neuroforge_cluster_commit_index", "Current committed cluster log index.", "gauge")
	promSample(&b, "neuroforge_cluster_commit_index", st.ClusterCommitIndex)
	promHeader(&b, "neuroforge_cluster_quorum", "Configured or calculated voting quorum.", "gauge")
	promSample(&b, "neuroforge_cluster_quorum", st.ClusterQuorum)
	promHeader(&b, "neuroforge_cluster_voters", "Current configured voting nodes including local node.", "gauge")
	promSample(&b, "neuroforge_cluster_voters", st.ClusterVoters)
	promHeader(&b, "neuroforge_cluster_info", "Static cluster identity information for this target.", "gauge")
	promSample(&b, "neuroforge_cluster_info", 1, "node_id", st.ClusterNodeID, "leader_id", st.ClusterLeaderID, "role", st.ClusterRole)
	promHeader(&b, "neuroforge_cluster_replicated_log_bytes", "Bytes used by the replicated cluster log.", "gauge")
	promSample(&b, "neuroforge_cluster_replicated_log_bytes", st.ClusterLog.Bytes)

	promHeader(&b, "neuroforge_openai_cost_usd", "Recorded OpenAI cost in USD for the current day or month.", "gauge")
	promSample(&b, "neuroforge_openai_cost_usd", strconv.FormatFloat(costs["daily_usd"], 'f', 9, 64), "period", "day")
	promSample(&b, "neuroforge_openai_cost_usd", strconv.FormatFloat(costs["monthly_usd"], 'f', 9, 64), "period", "month")
	promHeader(&b, "neuroforge_openai_budget_usd", "Configured OpenAI budget in USD.", "gauge")
	promSample(&b, "neuroforge_openai_budget_usd", strconv.FormatFloat(cfg.OpenAI.DailyBudgetUSD, 'f', 9, 64), "period", "day")
	promSample(&b, "neuroforge_openai_budget_usd", strconv.FormatFloat(cfg.OpenAI.MonthlyBudgetUSD, 'f', 9, 64), "period", "month")

	promHeader(&b, "neuroforge_runtime_goroutines", "Current Go goroutine count.", "gauge")
	promSample(&b, "neuroforge_runtime_goroutines", rt.Goroutines)
	promHeader(&b, "neuroforge_runtime_heap_alloc_bytes", "Current Go heap allocation bytes.", "gauge")
	promSample(&b, "neuroforge_runtime_heap_alloc_bytes", rt.HeapAlloc)
	promHeader(&b, "neuroforge_runtime_heap_inuse_bytes", "Current Go heap in-use bytes.", "gauge")
	promSample(&b, "neuroforge_runtime_heap_inuse_bytes", rt.HeapInuse)
	promHeader(&b, "neuroforge_runtime_heap_objects", "Current number of allocated heap objects.", "gauge")
	promSample(&b, "neuroforge_runtime_heap_objects", rt.HeapObjects)
	promHeader(&b, "neuroforge_runtime_sys_bytes", "Total bytes obtained from the OS by the Go runtime.", "gauge")
	promSample(&b, "neuroforge_runtime_sys_bytes", rt.SysBytes)
	promHeader(&b, "neuroforge_runtime_gc_cycles_total", "Completed Go garbage-collection cycles.", "counter")
	promSample(&b, "neuroforge_runtime_gc_cycles_total", rt.NumGC)
	promHeader(&b, "neuroforge_runtime_gc_pause_seconds_total", "Cumulative Go stop-the-world GC pause time in seconds.", "counter")
	promSample(&b, "neuroforge_runtime_gc_pause_seconds_total", strconv.FormatFloat(float64(rt.PauseTotalNS)/1e9, 'f', 9, 64))

	m := s.metrics
	m.mu.RLock()
	keys := make([]httpMetricKey, 0, len(m.http))
	for key := range m.http {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Route != keys[j].Route {
			return keys[i].Route < keys[j].Route
		}
		if keys[i].Method != keys[j].Method {
			return keys[i].Method < keys[j].Method
		}
		return keys[i].Code < keys[j].Code
	})
	promHeader(&b, "neuroforge_http_requests_total", "Total HTTP requests by method, normalized route and response code.", "counter")
	for _, key := range keys {
		x := m.http[key]
		promSample(&b, "neuroforge_http_requests_total", x.Count, "method", key.Method, "route", key.Route, "code", strconv.Itoa(key.Code))
	}
	promHeader(&b, "neuroforge_http_response_bytes_total", "Total HTTP response bytes by method, normalized route and response code.", "counter")
	for _, key := range keys {
		x := m.http[key]
		promSample(&b, "neuroforge_http_response_bytes_total", x.Bytes, "method", key.Method, "route", key.Route, "code", strconv.Itoa(key.Code))
	}
	promHeader(&b, "neuroforge_http_request_duration_seconds", "HTTP request duration histogram by method and normalized route.", "histogram")
	type routeKey struct{ method, route string }
	type routeAgg struct {
		count   uint64
		sum     float64
		buckets [len(requestDurationBuckets)]uint64
	}
	aggs := map[routeKey]*routeAgg{}
	for _, key := range keys {
		x := m.http[key]
		rk := routeKey{key.Method, key.Route}
		a := aggs[rk]
		if a == nil {
			a = &routeAgg{}
			aggs[rk] = a
		}
		a.count += x.Count
		a.sum += x.Sum
		for i := range a.buckets {
			a.buckets[i] += x.Buckets[i]
		}
	}
	routes := make([]routeKey, 0, len(aggs))
	for k := range aggs {
		routes = append(routes, k)
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].route == routes[j].route {
			return routes[i].method < routes[j].method
		}
		return routes[i].route < routes[j].route
	})
	for _, rk := range routes {
		a := aggs[rk]
		for i, upper := range requestDurationBuckets {
			promSample(&b, "neuroforge_http_request_duration_seconds_bucket", a.buckets[i], "method", rk.method, "route", rk.route, "le", strconv.FormatFloat(upper, 'f', -1, 64))
		}
		promSample(&b, "neuroforge_http_request_duration_seconds_bucket", a.count, "method", rk.method, "route", rk.route, "le", "+Inf")
		promSample(&b, "neuroforge_http_request_duration_seconds_sum", strconv.FormatFloat(a.sum, 'f', 9, 64), "method", rk.method, "route", rk.route)
		promSample(&b, "neuroforge_http_request_duration_seconds_count", a.count, "method", rk.method, "route", rk.route)
	}
	m.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}
