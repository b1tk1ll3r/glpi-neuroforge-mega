package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

type relinkPayload struct {
	TargetID   string    `json:"target_id"`
	Target     []float32 `json:"target"`
	Candidates []struct {
		ID     string    `json:"id"`
		Vector []float32 `json:"vector"`
	} `json:"candidates"`
	K             int     `json:"k"`
	MinSimilarity float64 `json:"min_similarity"`
}
type neighbor struct {
	ID         string  `json:"id"`
	Similarity float64 `json:"similarity"`
}
type relinkResult struct {
	TargetID  string     `json:"target_id"`
	Neighbors []neighbor `json:"neighbors"`
}

func main() {
	server := flag.String("server", "http://localhost:8080", "NeuroForge server")
	token := flag.String("token", os.Getenv("NEUROFORGE_WORKER_TOKEN"), "worker token")
	id := flag.String("id", hostname(), "worker id")
	interval := flag.Duration("interval", 2*time.Second, "poll interval")
	flag.Parse()
	if *token == "" {
		log.Fatal("worker token required (-token or NEUROFORGE_WORKER_TOKEN)")
	}
	client := &http.Client{Timeout: 180 * time.Second}
	log.Printf("worker %s polling %s", *id, *server)
	for {
		job, err := claim(client, *server, *token, *id)
		if err != nil {
			log.Printf("claim: %v", err)
			time.Sleep(*interval)
			continue
		}
		if job == nil {
			time.Sleep(*interval)
			continue
		}
		res, jobErr := run(job)
		if err := complete(client, *server, *token, *id, job.ID, res, jobErr); err != nil {
			log.Printf("complete %s: %v", job.ID, err)
		} else {
			log.Printf("job %s %s done", job.ID, job.Type)
		}
	}
}
func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "worker"
	}
	return h
}
func claim(c *http.Client, server, token, id string) (*core.Job, error) {
	body, _ := json.Marshal(map[string]string{"worker_id": id})
	req, _ := http.NewRequest("POST", strings.TrimRight(server, "/")+"/api/v1/worker/claim", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return nil, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	var j core.Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, err
	}
	return &j, nil
}
func run(j *core.Job) (json.RawMessage, string) {
	switch j.Type {
	case "vector.relink":
		var p relinkPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, err.Error()
		}
		out := relinkResult{TargetID: p.TargetID}
		for _, c := range p.Candidates {
			sim := vector.Cosine(p.Target, c.Vector)
			if sim >= p.MinSimilarity {
				out.Neighbors = append(out.Neighbors, neighbor{ID: c.ID, Similarity: sim})
			}
		}
		sort.Slice(out.Neighbors, func(i, k int) bool { return out.Neighbors[i].Similarity > out.Neighbors[k].Similarity })
		if p.K > 0 && len(out.Neighbors) > p.K {
			out.Neighbors = out.Neighbors[:p.K]
		}
		b, _ := json.Marshal(out)
		return b, ""
	default:
		return nil, "unsupported job type: " + j.Type
	}
}
func complete(c *http.Client, server, token, id, jobID string, result json.RawMessage, jobErr string) error {
	body, _ := json.Marshal(map[string]any{"worker_id": id, "job_id": jobID, "result": result, "error": jobErr})
	req, _ := http.NewRequest("POST", strings.TrimRight(server, "/")+"/api/v1/worker/complete", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	return nil
}
