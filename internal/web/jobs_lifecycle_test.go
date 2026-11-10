package web

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/guigui42/paperlens/internal/paperless"
)

func TestCompletedSnapshotsDoNotLockOutSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := &jobs{items: map[string]*job{}, queue: make(chan *job, 2), ctx: ctx, cancel: cancel, capacity: 2, timeout: time.Second}
	for i := range 64 {
		id := fmt.Sprintf("%02d", i)
		j.items[id] = &job{ID: id, Owner: "owner", State: "done", finished: true, Created: time.Now().Add(time.Duration(i-64) * time.Second), ctx: ctx, cancel: func() {}}
	}
	oldest := j.items["00"]
	item, err := j.start("new-owner", "synthetic", paperless.Filters{}, true, paperless.Vocabulary{})
	if err != nil || len(j.items) != 64 || j.inFlight != 1 {
		t.Fatal("terminal history blocked admission", err)
	}
	if _, ok := j.items[oldest.ID]; ok {
		t.Fatal("oldest terminal snapshot not evicted")
	}
	if _, ok := j.items[item.ID]; !ok {
		t.Fatal("new job missing")
	}
	if _, ok := j.items["01"]; !ok {
		t.Fatal("newer snapshot unnecessarily evicted")
	}
}

func TestCancelledCleanupStillCountsTowardCapacity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := &jobs{items: map[string]*job{}, queue: make(chan *job, 1), ctx: ctx, cancel: cancel, capacity: 1, inFlight: 1, timeout: time.Second}
	cancelled, cancelJob := context.WithCancel(ctx)
	cancelJob()
	j.items["pending-cleanup"] = &job{ID: "pending-cleanup", Owner: "owner", State: "cancelled", Created: time.Now(), ctx: cancelled, cancel: cancelJob}
	if _, err := j.start("other", "synthetic", paperless.Filters{}, true, paperless.Vocabulary{}); err == nil {
		t.Fatal("unfinished cleanup released its capacity")
	}
}

func TestShutdownRejectsAdmissionBeforeWorkerJoin(t *testing.T) {
	h := newHarness(t)
	h.app.BeginShutdown()
	if _, err := h.app.jobs.start("owner", "synthetic", paperless.Filters{}, true, paperless.Vocabulary{}); err == nil {
		t.Fatal("job admitted after shutdown began")
	}
	for _, path := range []string{"/", "/search", "/upload"} {
		method := "POST"
		if path == "/" {
			method = "GET"
		}
		status, _ := h.request(t, method, path, "", "")
		if status != 503 {
			t.Fatalf("request admitted during shutdown: %s %d", path, status)
		}
	}
	h.app.Close()
	h.app.Close()
	if h.app.jobs.inFlight != 0 {
		t.Fatal("closed jobs still counted")
	}
}
