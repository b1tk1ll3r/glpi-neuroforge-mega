package queue

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	PriorityScheduled = 20
	PriorityPoll      = 50
	PriorityWebhook   = 80
	PriorityManual    = 100
)

type WorkItem struct {
	TicketID int64     `json:"ticket_id"`
	Trigger  string    `json:"trigger"`
	Priority int       `json:"priority"`
	Enqueued time.Time `json:"enqueued_at"`
	Attempt  int       `json:"attempt,omitempty"`
	Force    bool      `json:"force,omitempty"`
	sequence uint64
}

func (w WorkItem) Key() string {
	trigger := w.Trigger
	if trigger == "" {
		trigger = "poll"
	}
	return fmt.Sprintf("%d:%s", w.TicketID, trigger)
}

type itemHeap []WorkItem

func (h itemHeap) Len() int { return len(h) }
func (h itemHeap) Less(i, j int) bool {
	if h[i].Priority != h[j].Priority {
		return h[i].Priority > h[j].Priority
	}
	return h[i].sequence < h[j].sequence
}
func (h itemHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *itemHeap) Push(x any)   { *h = append(*h, x.(WorkItem)) }
func (h *itemHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

type Queue struct {
	mu      sync.Mutex
	items   itemHeap
	pending map[string]struct{}
	notify  chan struct{}
	limit   int
	seq     uint64
}

func New(size int) *Queue {
	if size < 1 {
		size = 1
	}
	q := &Queue{pending: make(map[string]struct{}), notify: make(chan struct{}, 1), limit: size}
	heap.Init(&q.items)
	return q
}

func (q *Queue) Enqueue(id int64) bool {
	return q.EnqueueWork(WorkItem{TicketID: id, Trigger: "poll", Priority: PriorityPoll})
}

func (q *Queue) EnqueueWork(item WorkItem) bool {
	if item.TicketID <= 0 {
		return false
	}
	if item.Trigger == "" {
		item.Trigger = "poll"
	}
	if item.Priority == 0 {
		item.Priority = PriorityPoll
	}
	if item.Enqueued.IsZero() {
		item.Enqueued = time.Now()
	}
	key := item.Key()
	q.mu.Lock()
	if _, ok := q.pending[key]; ok || len(q.items) >= q.limit {
		q.mu.Unlock()
		return false
	}
	q.seq++
	item.sequence = q.seq
	q.pending[key] = struct{}{}
	heap.Push(&q.items, item)
	q.mu.Unlock()
	q.signal()
	return true
}

func (q *Queue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *Queue) Next(ctx context.Context) (int64, bool) {
	item, ok := q.NextWork(ctx)
	return item.TicketID, ok
}

func (q *Queue) NextWork(ctx context.Context) (WorkItem, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			item := heap.Pop(&q.items).(WorkItem)
			more := len(q.items) > 0
			q.mu.Unlock()
			if more {
				q.signal()
			}
			return item, true
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return WorkItem{}, false
		case <-q.notify:
		}
	}
}

func (q *Queue) Done(id int64) {
	q.mu.Lock()
	for key := range q.pending {
		prefix := fmt.Sprintf("%d:", id)
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(q.pending, key)
		}
	}
	q.mu.Unlock()
}

func (q *Queue) DoneWork(item WorkItem) {
	q.mu.Lock()
	delete(q.pending, item.Key())
	q.mu.Unlock()
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
