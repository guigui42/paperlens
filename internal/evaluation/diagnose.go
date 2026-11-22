package evaluation

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/guigui42/paperlens/internal/search"
)

// diagTools records the evidence an agent was shown, without extra reads, so
// a rejected ranking can be explained without perturbing the job.
type diagTools struct {
	search.Tools
	mu    sync.Mutex
	texts map[int][]string
}

// newDiagTools counts prefetched excerpts as read, as the engine does.
func newDiagTools(t search.Tools) *diagTools {
	d := &diagTools{Tools: t, texts: map[int][]string{}}
	for _, ev := range t.Seed().Excerpts {
		d.texts[ev.ID] = append(d.texts[ev.ID], ev.Text)
	}
	return d
}

func (t *diagTools) Excerpt(ctx context.Context, id int) (search.Evidence, error) {
	ev, err := t.Tools.Excerpt(ctx, id)
	if err == nil {
		t.mu.Lock()
		t.texts[id] = append(t.texts[id], ev.Text)
		t.mu.Unlock()
	}
	return ev, err
}

// reason classifies why the engine would reject a ranking. Categories only:
// no query, title or OCR text.
func (t *diagTools) reason(r search.Ranking, err error) string {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "agent_timeout"
		}
		if strings.Contains(err.Error(), "invalid ranking") || strings.Contains(err.Error(), "results array") || strings.Contains(err.Error(), "trailing output") {
			return "invalid_json"
		}
		return "agent_error"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(r.Results) > t.Limits().Excerpts {
		return "too_many_results"
	}
	var seen []int
	for _, p := range r.Results {
		switch {
		case slices.Contains(seen, p.ID):
			return "duplicate_id"
		case p.Strength != "strong" && p.Strength != "weak":
			return "bad_strength"
		case strings.TrimSpace(p.Quote) == "" || len([]rune(p.Quote)) > 500:
			return "quote_length"
		case len(t.texts[p.ID]) == 0:
			return "unread_id"
		case !slices.ContainsFunc(t.texts[p.ID], func(s string) bool { return strings.Contains(s, p.Quote) }):
			return "quote_not_in_excerpt"
		}
		seen = append(seen, p.ID)
	}
	return ""
}

// diagAgent wraps an agent and keeps the last rejection reason.
type diagAgent struct {
	inner search.Agent
	mu    sync.Mutex
	last  string
}

func (a *diagAgent) record(t *diagTools, r search.Ranking, err error) {
	a.mu.Lock()
	a.last = t.reason(r, err)
	a.mu.Unlock()
}

func (a *diagAgent) take() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.last
	a.last = ""
	return s
}

func (a *diagAgent) Rank(ctx context.Context, q string, t search.Tools) (search.Ranking, error) {
	dt := newDiagTools(t)
	r, err := a.inner.Rank(ctx, q, dt)
	a.record(dt, r, err)
	return r, err
}

// diagStarter preserves the inner agent's overlapped start-up.
type diagStarter struct{ *diagAgent }

func (a diagStarter) Begin(ctx context.Context) (search.Run, error) {
	run, err := a.inner.(search.Starter).Begin(ctx)
	if err != nil {
		return nil, err
	}
	return diagRun{run, a.diagAgent}, nil
}

type diagRun struct {
	search.Run
	a *diagAgent
}

func (r diagRun) Rank(ctx context.Context, q string, t search.Tools) (search.Ranking, error) {
	dt := newDiagTools(t)
	out, err := r.Run.Rank(ctx, q, dt)
	r.a.record(dt, out, err)
	return out, err
}

func diagnose(a search.Agent) (search.Agent, *diagAgent) {
	d := &diagAgent{inner: a}
	if _, ok := a.(search.Starter); ok {
		return diagStarter{d}, d
	}
	return d, d
}
