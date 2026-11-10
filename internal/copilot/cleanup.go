package copilot

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guigui42/paperlens/internal/search"
)

type cleanupSession interface {
	Abort(context.Context) error
	Disconnect() error
}
type cleanupClient interface {
	DeleteSession(context.Context, string) error
	Stop() error
	ForceStop()
}

func (a *Agent) cleanupRuntime(cancelled bool, dir, id string, session cleanupSession, client cleanupClient, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var forced atomic.Bool
	var forceOnce sync.Once
	forceDone := make(chan struct{})
	force := func() {
		forceOnce.Do(func() {
			forced.Store(true)
			client.ForceStop()
			close(forceDone)
		})
	}
	// Disconnect has no context in the pinned SDK. ForceStop unblocks its RPC
	// and Stop's I/O; forced or unconfirmed termination remains fail-closed.
	timer := time.AfterFunc(timeout, force)
	rpcFailed := false
	warn := func(stage string, err error) {
		if err != nil {
			rpcFailed = true
			slog.Warn("copilot session teardown failed", "stage", stage)
		}
	}
	if session != nil {
		if cancelled {
			warn("abort", session.Abort(ctx))
		}
		warn("disconnect", session.Disconnect())
		if !forced.Load() {
			warn("delete", client.DeleteSession(ctx, id))
		}
	}
	stopErr := client.Stop()
	if stopErr != nil {
		force()
	}
	if !timer.Stop() {
		<-forceDone
	}
	removeErr := os.RemoveAll(dir)
	_, statErr := os.Lstat(dir)
	unsafe := forced.Load() || stopErr != nil || removeErr != nil || !errors.Is(statErr, os.ErrNotExist)
	if unsafe {
		a.unhealthy.Store(true)
		slog.Error("copilot AI disabled until restart",
			"forced_shutdown", forced.Load(), "shutdown_unconfirmed", stopErr != nil,
			"state_removal_failed", removeErr != nil, "state_absence_unconfirmed", !errors.Is(statErr, os.ErrNotExist))
		return search.ErrAIDisabled
	}
	if rpcFailed {
		return search.ErrAITeardown
	}
	return nil
}
