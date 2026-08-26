package queue

import (
	"context"
	"testing"
	"time"
)

func TestPriorityOrderAndFIFO(t *testing.T) {
	q := New(10)
	q.EnqueueWork(WorkItem{TicketID: 1, Trigger: "poll", Priority: 10})
	q.EnqueueWork(WorkItem{TicketID: 2, Trigger: "webhook", Priority: 80})
	q.EnqueueWork(WorkItem{TicketID: 3, Trigger: "manual", Priority: 80})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i, want := range []int64{2, 3, 1} {
		got, ok := q.NextWork(ctx)
		if !ok || got.TicketID != want {
			t.Fatalf("item %d = %#v, want ticket %d", i, got, want)
		}
		q.DoneWork(got)
	}
}

func TestSameTicketDifferentTriggers(t *testing.T) {
	q := New(3)
	if !q.EnqueueWork(WorkItem{TicketID: 7, Trigger: "poll"}) {
		t.Fatal("poll enqueue failed")
	}
	if !q.EnqueueWork(WorkItem{TicketID: 7, Trigger: "scheduled_escalation"}) {
		t.Fatal("separate trigger should be accepted")
	}
	if q.EnqueueWork(WorkItem{TicketID: 7, Trigger: "poll"}) {
		t.Fatal("duplicate trigger should be rejected")
	}
}
