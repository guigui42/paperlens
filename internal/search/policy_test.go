package search_test

import (
	"context"
	"strings"
	"testing"

	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

type policyAPI struct {
	*demo.Archive
	tags []paperless.Term
}

func (a *policyAPI) Metadata(ctx context.Context) (paperless.Vocabulary, error) {
	v, err := a.Archive.Metadata(ctx)
	v.Tags = a.tags
	return v, err
}

func TestMissingExclusionsNeverReachAgent(t *testing.T) {
	for _, name := range []string{"", "Exports financiers", "Export-financier"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			e := engine(agentFunc(func(context.Context, string, search.Tools) (search.Ranking, error) {
				calls++
				return search.Ranking{}, nil
			}))
			e.API = &policyAPI{Archive: demo.New(), tags: []paperless.Term{{ID: 9, Name: name}}}
			out, err := e.Run(context.Background(), "financial export", paperless.Filters{}, false, nil)
			if err != nil || calls != 0 || out.Mode != "Keyword fallback" || !strings.Contains(out.Notice, "required exclusion tag") {
				t.Fatal(out, err, calls)
			}
		})
	}
}

func TestPolicyNormalizationAndConfiguredNames(t *testing.T) {
	e := engine(nil)
	for _, name := range []string{" Export financier ", "EXPORT   FINANCIER", "Éxport\tfinancier"} {
		if err := e.ValidatePolicy(paperless.Vocabulary{Tags: []paperless.Term{{ID: 9, Name: name}}}); err != nil {
			t.Fatal(name, err)
		}
	}
	e.ExcludedTags = []string{"Private", "Exports financiers"}
	v := paperless.Vocabulary{Tags: []paperless.Term{{ID: 9, Name: "Private"}}}
	if err := e.ValidatePolicy(v); err == nil {
		t.Fatal("partially resolved policy accepted")
	}
	v.Tags = append(v.Tags, paperless.Term{ID: 10, Name: "Exports financiers"})
	if err := e.ValidatePolicy(v); err != nil {
		t.Fatal(err)
	}
	e.ExcludedTags = []string{}
	if err := e.ValidatePolicy(v); err == nil {
		t.Fatal("empty explicit policy accepted")
	}
}

func TestPolicyIsCheckedAgainAfterMetadataChanges(t *testing.T) {
	api := &policyAPI{Archive: demo.New(), tags: []paperless.Term{{ID: 9, Name: "Export financier"}}}
	calls := 0
	e := engine(agentFunc(func(context.Context, string, search.Tools) (search.Ranking, error) {
		calls++
		return search.Ranking{Results: []search.Pick{}}, nil
	}))
	e.API = api
	if _, err := e.Run(context.Background(), "financial", paperless.Filters{}, false, nil); err != nil {
		t.Fatal(err)
	}
	api.tags = nil
	out, err := e.Run(context.Background(), "financial", paperless.Filters{}, false, nil)
	if err != nil || calls != 1 || out.Mode != "Keyword fallback" {
		t.Fatal(out, err, calls)
	}
}

func TestUnknownTagMetadataNeverReachesAgent(t *testing.T) {
	e := engine(agentFunc(func(ctx context.Context, q string, tools search.Tools) (search.Ranking, error) {
		cs, err := tools.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.ID == 1 || c.ID == 2 || c.ID == 5 {
				t.Fatal("candidate with unknown tag metadata reached AI")
			}
		}
		if _, err := tools.Excerpt(ctx, 1); err == nil {
			t.Fatal("unknown-tag excerpt reached AI")
		}
		return search.Ranking{Results: []search.Pick{}}, nil
	}))
	e.API = &policyAPI{Archive: demo.New(), tags: []paperless.Term{{ID: 9, Name: "Export financier"}}}
	out, err := e.Run(context.Background(), "home insurance", paperless.Filters{}, false, nil)
	if err != nil || out.Mode != "Evidence-ranked search" {
		t.Fatal(out, err)
	}
}

func TestCleanupFailuresHaveDistinctUserNotices(t *testing.T) {
	for _, tc := range []struct {
		err    error
		notice string
	}{
		{search.ErrAIDisabled, "disabled until restart"},
		{search.ErrAITeardown, "A new search can use AI"},
	} {
		e := engine(agentFunc(func(context.Context, string, search.Tools) (search.Ranking, error) { return search.Ranking{}, tc.err }))
		out, err := e.Run(context.Background(), "insurance", paperless.Filters{}, false, nil)
		if err != nil || out.Mode != "Keyword fallback" || !strings.Contains(out.Notice, tc.notice) {
			t.Fatal(out, err)
		}
	}
}
