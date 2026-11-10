package search_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

type hookAPI struct {
	*demo.Archive
	search func(context.Context) error
	empty  bool
}

func (a *hookAPI) Search(ctx context.Context, q string, f paperless.Filters, limit int) (paperless.Page, error) {
	if a.search != nil {
		if err := a.search(ctx); err != nil {
			return paperless.Page{}, err
		}
	}
	if a.empty {
		return paperless.Page{}, nil
	}
	return a.Archive.Search(ctx, q, f, limit)
}

// starter records the runtime lifecycle the engine drives.
type starter struct {
	agentFunc
	mu                            sync.Mutex
	begins, ranks, closes, active int
	inflight                      int
	beginning                     chan struct{}
	begin                         func(context.Context) error
	closeErr                      error
}

func (s *starter) Begin(ctx context.Context) (search.Run, error) {
	s.mu.Lock()
	s.begins++
	s.inflight++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inflight--
		s.mu.Unlock()
	}()
	close(s.beginning)
	if s.begin != nil {
		if err := s.begin(ctx); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.active++
	s.mu.Unlock()
	return &fakeRun{s: s}, nil
}

type fakeRun struct {
	s    *starter
	once sync.Once
}

func (r *fakeRun) Rank(ctx context.Context, q string, t search.Tools) (search.Ranking, error) {
	r.s.mu.Lock()
	r.s.ranks++
	r.s.mu.Unlock()
	return r.s.agentFunc(ctx, q, t)
}

func (r *fakeRun) Close() error {
	r.once.Do(func() {
		r.s.mu.Lock()
		r.s.closes++
		r.s.active--
		r.s.mu.Unlock()
	})
	return r.s.closeErr
}

func newStarter(rank agentFunc) *starter {
	return &starter{agentFunc: rank, beginning: make(chan struct{})}
}

func grounded(ctx context.Context, _ string, tools search.Tools) (search.Ranking, error) {
	if _, err := tools.Excerpt(ctx, 1); err != nil {
		return search.Ranking{}, err
	}
	return search.Ranking{Results: []search.Pick{{ID: 1, Quote: "Home insurance certificate 2025", Strength: "strong"}}}, nil
}

// waitBegin makes the original Paperless query wait for the runtime start,
// which only completes if start-up genuinely overlaps it.
func waitBegin(s *starter) func(context.Context) error {
	return func(context.Context) error {
		select {
		case <-s.beginning:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("runtime start did not overlap the Paperless query")
		}
	}
}

func TestOverlapStartsRuntimeOnlyForAI(t *testing.T) {
	s := newStarter(grounded)
	e := engine(s)
	e.API = &hookAPI{Archive: demo.New(), search: waitBegin(s)}
	out, err := e.Run(context.Background(), "home insurance", paperless.Filters{}, false, nil)
	if err != nil || out.Mode != "Evidence-ranked search" || out.Results[0].Document.ID != 1 {
		t.Fatal(out, err)
	}
	if s.begins != 1 || s.ranks != 1 || s.closes != 1 || s.active != 0 {
		t.Fatal("runtime not started once, ranked and released", s.begins, s.ranks, s.closes, s.active)
	}

	for name, run := range map[string]func(*starter) (search.Output, error){
		"keyword": func(s *starter) (search.Output, error) {
			return engine(s).Run(context.Background(), "home insurance", paperless.Filters{}, true, nil)
		},
		"policy": func(s *starter) (search.Output, error) {
			e := engine(s)
			e.API = &policyAPI{Archive: demo.New(), tags: []paperless.Term{{ID: 9, Name: "unrelated"}}}
			return e.Run(context.Background(), "home insurance", paperless.Filters{}, false, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStarter(grounded)
			if _, err := run(s); err != nil {
				t.Fatal(err)
			}
			if s.begins != 0 || s.ranks != 0 {
				t.Fatal("runtime started without an AI search", s.begins, s.ranks)
			}
		})
	}
}

func TestOverlapReleasesRuntimeOnEveryExit(t *testing.T) {
	paperlessDown := errors.New("synthetic paperless failure")
	cases := []struct {
		name  string
		setup func(*starter, *hookAPI, context.CancelFunc)
		check func(*testing.T, *starter, search.Output, error)
	}{
		{"paperless error after start", func(s *starter, api *hookAPI, _ context.CancelFunc) {
			api.search = func(ctx context.Context) error {
				if err := waitBegin(s)(ctx); err != nil {
					return err
				}
				return paperlessDown
			}
		}, func(t *testing.T, s *starter, _ search.Output, err error) {
			if !errors.Is(err, paperlessDown) || s.ranks != 0 {
				t.Fatal(err, s.ranks)
			}
		}},
		{"cancel after start", func(s *starter, api *hookAPI, cancel context.CancelFunc) {
			api.search = func(ctx context.Context) error {
				if err := waitBegin(s)(ctx); err != nil {
					return err
				}
				cancel()
				return ctx.Err()
			}
		}, func(t *testing.T, s *starter, _ search.Output, err error) {
			if !errors.Is(err, context.Canceled) || s.ranks != 0 {
				t.Fatal(err, s.ranks)
			}
		}},
		{"cancel during start", func(s *starter, api *hookAPI, cancel context.CancelFunc) {
			s.begin = func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}
			api.search = func(ctx context.Context) error {
				err := waitBegin(s)(ctx)
				cancel()
				return errors.Join(err, ctx.Err())
			}
		}, func(t *testing.T, s *starter, _ search.Output, err error) {
			if !errors.Is(err, context.Canceled) || s.ranks != 0 {
				t.Fatal(err, s.ranks)
			}
		}},
		{"zero candidates", func(_ *starter, api *hookAPI, _ context.CancelFunc) {
			api.empty = true
		}, func(t *testing.T, s *starter, out search.Output, err error) {
			if err != nil || out.Candidates != 0 || s.ranks != 1 {
				t.Fatal(out, err, s.ranks)
			}
		}},
		{"rank error", func(s *starter, _ *hookAPI, _ context.CancelFunc) {
			s.agentFunc = func(context.Context, string, search.Tools) (search.Ranking, error) {
				return search.Ranking{}, search.ErrAIInference
			}
		}, func(t *testing.T, _ *starter, out search.Output, err error) {
			if err != nil || out.Mode != "Keyword fallback" || !strings.Contains(out.Notice, search.ErrAIInference.Error()) {
				t.Fatal(out, err)
			}
		}},
		{"start error", func(s *starter, _ *hookAPI, _ context.CancelFunc) {
			s.begin = func(context.Context) error { return search.ErrAIRuntime }
		}, func(t *testing.T, s *starter, out search.Output, err error) {
			if err != nil || out.Mode != "Keyword fallback" || !strings.Contains(out.Notice, search.ErrAIRuntime.Error()) || s.ranks != 0 {
				t.Fatal(out, err, s.ranks)
			}
		}},
		{"teardown error", func(s *starter, _ *hookAPI, _ context.CancelFunc) {
			s.closeErr = search.ErrAITeardown
		}, func(t *testing.T, _ *starter, out search.Output, err error) {
			if err != nil || out.Mode != "Keyword fallback" || !strings.Contains(out.Notice, search.ErrAITeardown.Error()) {
				t.Fatal(out, err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStarter(grounded)
			api := &hookAPI{Archive: demo.New()}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.setup(s, api, cancel)
			e := engine(s)
			e.API = api
			out, err := e.Run(ctx, "home insurance", paperless.Filters{}, false, nil)
			c.check(t, s, out, err)
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.begins != 1 || s.inflight != 0 || s.active != 0 {
				t.Fatal("runtime outlived the search", s.begins, s.inflight, s.active)
			}
		})
	}
}
