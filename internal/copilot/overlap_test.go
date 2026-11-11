package copilot

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

// signalStarter reports when the real runtime has finished starting.
type signalStarter struct {
	*Agent
	started chan *run
	last    **run
}

func (s signalStarter) Begin(ctx context.Context) (search.Run, error) {
	r, err := s.Agent.Begin(ctx)
	if err == nil {
		*s.last = r.(*run)
		s.started <- *s.last
	}
	return r, err
}

type blockingAPI struct {
	*demo.Archive
	search func(context.Context) error
}

func (a blockingAPI) Search(ctx context.Context, q string, f paperless.Filters, limit int) (paperless.Page, error) {
	if err := a.search(ctx); err != nil {
		return paperless.Page{}, err
	}
	return a.Archive.Search(ctx, q, f, limit)
}

func runtimeDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "search-") {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}

// TestPinnedRuntimeOverlapCleanup drives the real CLI through Engine.Run and
// proves an overlapped runtime is released when the search never reaches Rank.
// The model cache is pre-seeded so no credential or network call is needed.
func TestPinnedRuntimeOverlapCleanup(t *testing.T) {
	path := os.Getenv("PAPERLENS_TEST_CLI")
	if path == "" {
		t.Skip("set PAPERLENS_TEST_CLI for credential-free overlapped runtime checks")
	}
	paperlessDown := errors.New("synthetic paperless failure")
	cases := []struct {
		name   string
		search func(t *testing.T, root string, started chan *run, cancel context.CancelFunc) func(context.Context) error
		want   error
	}{
		{"paperless error after start", func(t *testing.T, _ string, started chan *run, _ context.CancelFunc) func(context.Context) error {
			return func(context.Context) error {
				select {
				case r := <-started:
					if _, err := r.client.Ping(context.Background(), "synthetic overlap check"); err != nil {
						t.Error("runtime was not started during the Paperless query", err)
					}
				case <-time.After(30 * time.Second):
					t.Error("runtime did not start during the Paperless query")
				}
				return paperlessDown
			}
		}, paperlessDown},
		{"cancel during start", func(t *testing.T, root string, _ chan *run, cancel context.CancelFunc) func(context.Context) error {
			return func(ctx context.Context) error {
				deadline := time.Now().Add(10 * time.Second)
				for len(runtimeDirs(t, root)) == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if len(runtimeDirs(t, root)) == 0 {
					t.Error("runtime did not begin during the Paperless query")
				}
				// Let the CLI process launch so cancellation interrupts Start.
				time.Sleep(300 * time.Millisecond)
				cancel()
				return ctx.Err()
			}
		}, context.Canceled},
		{"cancel after start", func(t *testing.T, _ string, started chan *run, cancel context.CancelFunc) func(context.Context) error {
			return func(ctx context.Context) error {
				select {
				case <-started:
				case <-time.After(30 * time.Second):
					t.Error("runtime did not start during the Paperless query")
				}
				cancel()
				return ctx.Err()
			}
		}, context.Canceled},
	}
	// One root keeps the extracted CLI package warm, as in production.
	root := t.TempDir()
	a := &Agent{Path: path, Root: root, Model: "gpt-5.4"}
	if err := a.Prepare(); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a.storeModel(modelEntry{}, time.Now())
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			started := make(chan *run, 1)
			var last *run
			e := &search.Engine{
				API:    blockingAPI{Archive: demo.New(), search: c.search(t, root, started, cancel)},
				Agent:  signalStarter{Agent: a, started: started, last: &last},
				Limits: search.Limits{Searches: 6, Candidates: 40, Excerpts: 6, Characters: 2500},
			}
			if _, err := e.Run(ctx, "synthetic", paperless.Filters{}, false, nil); !errors.Is(err, c.want) {
				t.Fatal(err)
			}
			if dirs := runtimeDirs(t, root); len(dirs) != 0 {
				t.Fatal("overlapped runtime state remains", dirs)
			}
			if a.unhealthy.Load() {
				t.Fatal("abandoned runtime cleanup disabled AI")
			}
			if last != nil {
				if _, err := last.client.Ping(context.Background(), "synthetic overlap check"); err == nil {
					t.Fatal("abandoned runtime still answers")
				}
			}
		})
	}
}
