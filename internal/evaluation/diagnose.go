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
	shown map[int]bool
}

// newDiagTools counts prefetched candidates as shown and prefetched excerpts
// as read, as the engine supplies both with the first model turn.
func newDiagTools(t search.Tools) *diagTools {
	d := &diagTools{Tools: t, texts: map[int][]string{}, shown: map[int]bool{}}
	seed := t.Seed()
	for _, c := range seed.Candidates {
		d.shown[c.ID] = true
	}
	for _, ev := range seed.Excerpts {
		d.texts[ev.ID] = append(d.texts[ev.ID], ev.Text)
	}
	return d
}

func (t *diagTools) Search(ctx context.Context, q string) ([]search.Candidate, error) {
	cs, err := t.Tools.Search(ctx, q)
	t.mu.Lock()
	for _, c := range cs {
		t.shown[c.ID] = true
	}
	t.mu.Unlock()
	return cs, err
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
		return errReason(err)
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

func errReason(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "agent_timeout"
	}
	if strings.Contains(err.Error(), "invalid ranking") || strings.Contains(err.Error(), "results array") || strings.Contains(err.Error(), "trailing output") {
		return "invalid_json"
	}
	return "agent_error"
}

// diagnosis is what one ranking job showed the agent, and why the engine
// would reject its answer.
type diagnosis struct {
	reason      string
	shown, read map[int]bool
}

// diagAgent wraps an agent and keeps the last job's diagnosis.
type diagAgent struct {
	inner search.Agent
	mu    sync.Mutex
	last  diagnosis
}

func (a *diagAgent) record(t *diagTools, r search.Ranking, err error) {
	reason := t.reason(r, err)
	t.mu.Lock()
	d := diagnosis{reason: reason, shown: t.shown, read: map[int]bool{}}
	for id := range t.texts {
		d.read[id] = true
	}
	t.mu.Unlock()
	a.set(d)
}

func (a *diagAgent) set(d diagnosis) {
	a.mu.Lock()
	a.last = d
	a.mu.Unlock()
}

func (a *diagAgent) take() diagnosis {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.last
	a.last = diagnosis{}
	return d
}

func (a *diagAgent) Rank(ctx context.Context, q string, t search.Tools) (search.Ranking, error) {
	dt := newDiagTools(t)
	r, err := a.inner.Rank(ctx, q, dt)
	a.record(dt, r, err)
	return r, err
}

// diagStarter preserves the inner agent's overlapped start-up. A failed
// start-up never reaches Rank, so it is recorded here. The engine awaits Begin
// before Run returns, so Evaluate's take sees it.
type diagStarter struct{ *diagAgent }

func (a diagStarter) Begin(ctx context.Context) (search.Run, error) {
	run, err := a.inner.(search.Starter).Begin(ctx)
	if err != nil || run == nil {
		if err == nil {
			err = search.ErrAIRuntime
		}
		a.set(diagnosis{reason: errReason(err)})
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
