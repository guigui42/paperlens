package copilot

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/guigui42/paperlens/internal/search"
)

func TestPinnedRuntimeCancellationCleanup(t *testing.T) {
	path := os.Getenv("PAPERLENS_TEST_CLI")
	if path == "" {
		t.Skip("set PAPERLENS_TEST_CLI for credential-free runtime cancellation checks")
	}
	a := &Agent{Path: path, Root: t.TempDir()}
	dir, err := os.MkdirTemp(a.Root, "search-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	client := a.client(dir)
	defer client.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	session, err := client.CreateSession(ctx, sessionConfig(ctx, dir, "gpt-5.4", emptyTools{}))
	if err != nil {
		t.Fatal(err)
	}
	err = a.cleanupRuntime(true, dir, session.SessionID, session, client, 30*time.Second)
	if err != nil && !errors.Is(err, search.ErrAITeardown) {
		t.Fatal(err)
	}
	if a.unhealthy.Load() {
		t.Fatal("normal session cancellation permanently disabled AI")
	}
	if _, err = os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled runtime state remains")
	}
}
