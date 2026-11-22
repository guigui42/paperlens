package evaluation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

func TestSyntheticRelevance(t *testing.T) {
	r, err := Run(context.Background(), FakeAgent{}, "fake-agent contract evaluation, not AI quality", DefaultLimits)
	b, _ := json.MarshalIndent(r, "", "  ")
	t.Log(string(b))
	if err != nil {
		t.Fatal(err)
	}
}

// The hard set only measures context-aware retrieval while strict keyword
// search, which mimics Paperless, misses every answerable case.
func TestHardCasesDefeatStrictKeywordSearch(t *testing.T) {
	cases, err := HardCases()
	if err != nil {
		t.Fatal(err)
	}
	api := Strict{API: demo.New()}
	holdout := 0
	for _, c := range cases {
		if c.Holdout {
			holdout++
		}
		if c.Expected == 0 {
			continue
		}
		if _, err := api.Document(context.Background(), c.Expected); err != nil {
			t.Fatalf("%q expects unknown document %d", c.Query, c.Expected)
		}
		p, err := api.Search(context.Background(), c.Query, paperless.Filters{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range p.Results {
			if d.ID == c.Expected {
				t.Errorf("%q is not hard: strict keyword search already finds %d", c.Query, c.Expected)
			}
		}
	}
	if holdout == 0 || holdout == len(cases) {
		t.Fatalf("hard set needs both dev and holdout cases, got %d/%d holdout", holdout, len(cases))
	}
}

func TestStrictRequiresEveryWord(t *testing.T) {
	api := Strict{API: demo.New()}
	for q, want := range map[string]int{"garantie lave-linge": 1, "latest garantie lave-linge": 0} {
		p, err := api.Search(context.Background(), q, paperless.Filters{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Results) != want {
			t.Errorf("%q: got %d results, want %d", q, len(p.Results), want)
		}
	}
}

type rankFunc func(context.Context, string, search.Tools) (search.Ranking, error)

func (f rankFunc) Rank(c context.Context, q string, t search.Tools) (search.Ranking, error) {
	return f(c, q, t)
}

func TestEvaluateExplainsRejectedRankings(t *testing.T) {
	for want, pick := range map[string]search.Pick{
		"unread_id":            {ID: 999, Quote: "Washing machine warranty", Strength: "strong"},
		"quote_not_in_excerpt": {ID: 1, Quote: "invented text", Strength: "strong"},
	} {
		agent := rankFunc(func(ctx context.Context, q string, tools search.Tools) (search.Ranking, error) {
			if _, err := tools.Excerpt(ctx, 1); err != nil {
				return search.Ranking{}, err
			}
			return search.Ranking{Results: []search.Pick{pick}}, nil
		})
		cases := []Case{{Query: "insurance warranty", Expected: 1}}
		r, err := Evaluate(context.Background(), demo.New(), agent, nil, DefaultLimits, cases, "test")
		if err != nil || r.Cases[0].Mode != "Keyword fallback" || r.Cases[0].Reject != want {
			t.Fatalf("want %s, got %+v %v", want, r.Cases, err)
		}
	}
}
