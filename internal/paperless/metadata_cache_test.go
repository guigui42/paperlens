package paperless

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMetadataWaiterCanCancelDuringRefresh(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"results":[],"next":null}`)
	})
	leader := make(chan error, 1)
	go func() { _, err := c.Metadata(context.Background()); leader <- err }()
	defer func() {
		close(release)
		select {
		case err := <-leader:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("metadata refresh did not finish after release")
		}
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	waiter := make(chan error, 1)
	go func() { _, err := c.Metadata(ctx); waiter <- err }()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter is blocked behind the network refresh")
	}
	select {
	case <-leader:
		t.Fatal("leader unexpectedly completed before release")
	default:
	}
}

func TestConcurrentMetadataRefreshIsCoalesced(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"results":[{"id":1,"name":"Synthetic"}],"next":null}`)
	})
	start := make(chan struct{})
	done := make(chan error, 16)
	for range 16 {
		go func() {
			<-start
			v, err := c.Metadata(context.Background())
			if err == nil && len(v.Tags) != 1 {
				err = errors.New("incomplete vocabulary")
			}
			done <- err
		}()
	}
	close(start)
	for range 16 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("concurrent metadata caused %d upstream calls", calls.Load())
	}
}

func TestFailedRefreshDoesNotReturnExpiredMetadata(t *testing.T) {
	var fail atomic.Bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"results":[{"id":1,"name":"Synthetic"}],"next":null}`)
	})
	if _, err := c.Metadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.until = time.Now().Add(-time.Second)
	c.mu.Unlock()
	fail.Store(true)
	if _, err := c.Metadata(context.Background()); err == nil {
		t.Fatal("expired privacy metadata silently reused")
	}
	fail.Store(false)
	if _, err := c.Metadata(context.Background()); err != nil {
		t.Fatal("failed refresh could not be retried", err)
	}
}
