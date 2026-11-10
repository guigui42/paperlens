package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

type agentFunc func(context.Context, string, search.Tools) (search.Ranking, error)

func (f agentFunc) Rank(c context.Context, q string, t search.Tools) (search.Ranking, error) {
	return f(c, q, t)
}
func engine(a search.Agent) *search.Engine {
	return &search.Engine{API: demo.New(), Agent: a, Limits: search.Limits{Searches: 6, Candidates: 100, Excerpts: 12, Characters: 8000}}
}
func TestExclusionAndEvidence(t *testing.T) {
	agent := agentFunc(func(ctx context.Context, q string, tools search.Tools) (search.Ranking, error) {
		cs, err := tools.Search(ctx, "synthetic financial export")
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(cs)
		if strings.Contains(string(payload), "financial") || strings.Contains(string(payload), "15") {
			t.Fatal("excluded metadata reached model")
		}
		if _, err = tools.Excerpt(ctx, 15); err == nil {
			t.Fatal("excluded OCR reachable")
		}
		if _, err = tools.Excerpt(ctx, 999999); err == nil {
			t.Fatal("unretrieved ID reachable")
		}
		v, _ := json.Marshal(tools.Lookup())
		if strings.Contains(string(v), "Export financier") {
			t.Fatal("excluded tag leaked into model vocabulary")
		}
		cs, err = tools.Search(ctx, "home insurance")
		if err != nil || len(cs) == 0 {
			t.Fatal(err)
		}
		ev, err := tools.Excerpt(ctx, 1)
		if err != nil || !strings.Contains(ev.Text, "certificate") {
			t.Fatal(ev, err)
		}
		return search.Ranking{Results: []search.Pick{{ID: 1, Quote: "Home insurance certificate 2025", Strength: "strong"}}}, nil
	})
	out, err := engine(agent).Run(context.Background(), "home insurance", paperless.Filters{}, false, nil)
	if err != nil || out.Mode != "Evidence-ranked search" || out.Results[0].Document.ID != 1 {
		t.Fatal(out, err)
	}
	found := false
	for _, r := range out.Results {
		if r.Document.ID == 15 {
			found = r.Excluded
		}
	}
	if !found {
		t.Fatal("excluded non-AI result absent")
	}
}
func TestInvalidEvidenceFallback(t *testing.T) {
	for _, pick := range []search.Pick{{ID: 999, Quote: "invented", Strength: "strong"}, {ID: 1, Quote: "invented", Strength: "strong"}, {ID: 1, Quote: "Home insurance certificate 2025", Strength: "absolute"}, {ID: 1, Quote: "", Strength: "strong"}} {
		t.Run(pick.Quote+pick.Strength, func(t *testing.T) {
			a := agentFunc(func(ctx context.Context, q string, tools search.Tools) (search.Ranking, error) {
				tools.Excerpt(ctx, 1)
				return search.Ranking{Results: []search.Pick{pick}}, nil
			})
			out, err := engine(a).Run(context.Background(), "home insurance", paperless.Filters{}, false, nil)
			if err != nil || out.Mode != "Keyword fallback" || len(out.Results) == 0 {
				t.Fatal(out, err)
			}
		})
	}
}
func TestHardFiltersAndNoRelaxation(t *testing.T) {
	f := paperless.Filters{After: "2025-01-01", Before: "2025-12-31", Tag: 1}
	a := agentFunc(func(ctx context.Context, q string, tools search.Tools) (search.Ranking, error) {
		cs, err := tools.Search(ctx, "insurance")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.ID == 2 {
				t.Fatal("date filter removed")
			}
		}
		if _, err = tools.Excerpt(ctx, 2); err == nil {
			t.Fatal("filter bypass through excerpt")
		}
		return search.Ranking{Results: []search.Pick{}}, nil
	})
	out, err := engine(a).Run(context.Background(), "insurance", f, false, nil)
	if err != nil || len(out.Results) != 0 {
		t.Fatal(out, err)
	}
	out, err = engine(nil).Run(context.Background(), "registration", paperless.Filters{Field: 1, Value: "Someone Else"}, true, nil)
	if err != nil || len(out.Results) != 0 {
		t.Fatal("custom filter relaxed", out, err)
	}
}
func TestBudgetsAndDeduplication(t *testing.T) {
	a := agentFunc(func(ctx context.Context, q string, tools search.Tools) (search.Ranking, error) {
		if _, err := tools.Search(ctx, "insurance"); err == nil {
			t.Fatal("search call budget bypassed")
		}
		if _, err := tools.Excerpt(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.Excerpt(ctx, 2); err == nil {
			t.Fatal("excerpt budget bypassed")
		}
		e, err := tools.Excerpt(ctx, 1)
		if err != nil || len([]rune(e.Text)) > 50 {
			t.Fatal(e, err)
		}
		return search.Ranking{}, errors.New("synthetic failure")
	})
	e := engine(a)
	e.Limits = search.Limits{Searches: 1, Candidates: 100, Excerpts: 1, Characters: 50}
	out, err := e.Run(context.Background(), "insurance", paperless.Filters{}, false, nil)
	if err != nil || !out.Limited || out.Calls != 1 {
		t.Fatal(out, err)
	}
	e = engine(agentFunc(func(ctx context.Context, q string, t search.Tools) (search.Ranking, error) {
		t.Search(ctx, q)
		t.Search(ctx, q)
		return search.Ranking{Results: []search.Pick{}}, nil
	}))
	out, err = e.Run(context.Background(), "home insurance", paperless.Filters{}, false, nil)
	if err != nil || out.Candidates != 4 || out.Calls != 3 {
		t.Fatal(out, err)
	}
}
func TestLatestAndMentionedYear(t *testing.T) {
	out, err := engine(nil).Run(context.Background(), "latest home insurance certificate", paperless.Filters{}, true, nil)
	if err != nil || out.Results[0].Document.ID != 1 {
		t.Fatal("incidental newest OCR outranked title", out, err)
	}
	out, err = engine(nil).Run(context.Background(), "tax income 2024", paperless.Filters{}, true, nil)
	if err != nil || out.Results[0].Document.ID != 13 {
		t.Fatal("mentioned year treated as issue date", out, err)
	}
}
func TestStrictOutput(t *testing.T) {
	for _, raw := range []string{`{"results":[{"id":1,"quote":"x","strength":"strong","url":"https://bad"}]}`, `{"results":[]} trailing`, `{}`, `{"results":null}`, "```json\n{}\n```", strings.Repeat("x", 32001)} {
		if _, err := search.Decode(raw); err == nil {
			t.Errorf("accepted %q", raw[:min(50, len(raw))])
		}
	}
	if _, err := search.Decode(`{"results":[]}`); err != nil {
		t.Fatal(err)
	}
}
func TestCancellationAndInvalidInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine(nil).Run(ctx, "test", paperless.Filters{}, true, nil); err == nil {
		t.Fatal("cancel ignored")
	}
	for _, q := range []string{"", strings.Repeat("a", 501)} {
		if _, err := engine(nil).Run(context.Background(), q, paperless.Filters{}, true, nil); err == nil {
			t.Fatal("bad query accepted")
		}
	}
	if search.Fold("Électricité à Noël") != "electricite a noel" {
		t.Fatal("accent folding failed")
	}
}
