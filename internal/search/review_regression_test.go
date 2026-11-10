package search_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

func TestOneSearchBudgetKeepsOriginalCandidates(t *testing.T) {
	e := engine(agentFunc(func(ctx context.Context, q string, tools search.Tools) (search.Ranking, error) {
		candidates, err := tools.Search(ctx, q)
		if err != nil || len(candidates) == 0 {
			t.Fatal("original candidates unavailable", err)
		}
		ev, err := tools.Excerpt(ctx, candidates[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		return search.Ranking{Results: []search.Pick{{ID: ev.ID, Quote: strings.SplitN(ev.Text, "\n", 2)[0], Strength: "strong"}}}, nil
	}))
	e.Limits.Searches = 1
	out, err := e.Run(context.Background(), "home insurance", paperless.Filters{}, false, nil)
	if err != nil || out.Mode != "Evidence-ranked search" || out.Calls != 1 || len(out.Results) == 0 {
		t.Fatal(out, err)
	}
}
func TestOperationalFailuresAreSanitizedAndActionable(t *testing.T) {
	for _, safe := range []error{search.ErrAIRuntime, search.ErrAIAuthentication, search.ErrAIModel, search.ErrAISession, search.ErrAIInference} {
		e := engine(agentFunc(func(context.Context, string, search.Tools) (search.Ranking, error) { return search.Ranking{}, safe }))
		out, err := e.Run(context.Background(), "insurance", paperless.Filters{}, false, nil)
		if err != nil || !strings.Contains(out.Notice, safe.Error()) {
			t.Fatal(out, err)
		}
	}
	e := engine(agentFunc(func(context.Context, string, search.Tools) (search.Ranking, error) {
		return search.Ranking{}, errors.New("PRIVATE_DIAGNOSTIC")
	}))
	out, err := e.Run(context.Background(), "insurance", paperless.Filters{}, false, nil)
	if err != nil || strings.Contains(out.Notice, "PRIVATE_DIAGNOSTIC") {
		t.Fatal("raw model diagnostic exposed")
	}
}
