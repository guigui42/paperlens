package copilot

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guigui42/paperlens/internal/search"
)

type teardownSession struct {
	abortErr, detachErr error
	blocked             <-chan struct{}
}

func (s teardownSession) Abort(context.Context) error { return s.abortErr }
func (s teardownSession) Disconnect() error {
	if s.blocked != nil {
		<-s.blocked
	}
	return s.detachErr
}

type teardownClient struct {
	deleteErr, stopErr error
	stopped            bool
	force              func()
	forced             atomic.Bool
	blockedStop        <-chan struct{}
}

func (c *teardownClient) DeleteSession(context.Context, string) error { return c.deleteErr }
func (c *teardownClient) Stop() error {
	if c.blockedStop != nil {
		<-c.blockedStop
	}
	c.stopped = true
	return c.stopErr
}
func (c *teardownClient) ForceStop() {
	c.forced.Store(true)
	if c.force != nil {
		c.force()
	}
}

func TestRecoverableSessionErrorsDoNotDisableAI(t *testing.T) {
	sensitive := errors.New("PRIVATE_SYNTHETIC_DIAGNOSTIC")
	for _, stage := range []string{"abort", "disconnect", "delete"} {
		t.Run(stage, func(t *testing.T) {
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(old)
			session := teardownSession{}
			client := &teardownClient{}
			switch stage {
			case "abort":
				session.abortErr = sensitive
			case "disconnect":
				session.detachErr = sensitive
			case "delete":
				client.deleteErr = sensitive
			}
			dir := filepath.Join(t.TempDir(), "search-test")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("synthetic"), 0600); err != nil {
				t.Fatal(err)
			}
			agent := &Agent{}
			err := agent.cleanupRuntime(true, dir, "synthetic", session, client, time.Second)
			if !errors.Is(err, search.ErrAITeardown) || agent.unhealthy.Load() || !client.stopped {
				t.Fatal(err)
			}
			if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("state survived cleanup")
			}
			if strings.Contains(logs.String(), sensitive.Error()) || !strings.Contains(logs.String(), stage) {
				t.Fatal("unsafe or missing teardown diagnostics")
			}
			if err = agent.cleanupRuntime(false, dir, "", nil, &teardownClient{}, time.Second); err != nil || agent.unhealthy.Load() {
				t.Fatal("subsequent cleanup did not recover", err)
			}
		})
	}
}

func TestUnconfirmedStopAndStateRemovalDisableAI(t *testing.T) {
	t.Run("process", func(t *testing.T) {
		a := &Agent{}
		err := a.cleanupRuntime(false, filepath.Join(t.TempDir(), "search-test"), "", nil, &teardownClient{stopErr: errors.New("unconfirmed stop")}, time.Second)
		if !errors.Is(err, search.ErrAIDisabled) || !a.unhealthy.Load() {
			t.Fatal(err)
		}
		_, err = a.Rank(context.Background(), "synthetic", emptyTools{})
		if !errors.Is(err, search.ErrAIDisabled) {
			t.Fatal("latched state not surfaced", err)
		}
	})
	t.Run("filesystem", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "not-directory")
		if err := os.WriteFile(file, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
		a := &Agent{}
		err := a.cleanupRuntime(false, filepath.Join(file, "search-test"), "", nil, &teardownClient{}, time.Second)
		if !errors.Is(err, search.ErrAIDisabled) || !a.unhealthy.Load() {
			t.Fatal(err)
		}
	})
}

func TestUnresponsiveDisconnectIsBoundedAndFailsClosed(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	client := &teardownClient{force: unblock}
	a := &Agent{}
	done := make(chan error, 1)
	go func() {
		done <- a.cleanupRuntime(true, filepath.Join(t.TempDir(), "search-test"), "synthetic", teardownSession{blocked: release}, client, 10*time.Millisecond)
	}()
	select {
	case err := <-done:
		if !errors.Is(err, search.ErrAIDisabled) || !a.unhealthy.Load() || !client.forced.Load() {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unresponsive detach blocked indefinitely")
	}
}

func TestUnresponsiveStopIsBoundedAndFailsClosed(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	client := &teardownClient{force: unblock, blockedStop: release}
	a := &Agent{}
	dir := filepath.Join(t.TempDir(), "search-test")
	done := make(chan error, 1)
	go func() { done <- a.cleanupRuntime(false, dir, "", nil, client, 10*time.Millisecond) }()
	select {
	case err := <-done:
		if !errors.Is(err, search.ErrAIDisabled) || !client.forced.Load() {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unresponsive stop blocked indefinitely")
	}
}
