package evaluation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
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
