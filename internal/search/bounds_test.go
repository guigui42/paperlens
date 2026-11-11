package search

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/guigui42/paperlens/internal/paperless"
)

type pageAPI struct {
	paperless.API
	pages []paperless.Page
	calls int
}

func (a *pageAPI) Search(context.Context, string, paperless.Filters, int) (paperless.Page, error) {
	if a.calls >= len(a.pages) {
		return paperless.Page{}, errors.New("unexpected call")
	}
	p := a.pages[a.calls]
	a.calls++
	return p, nil
}
func TestRankMergeAndChangingExclusion(t *testing.T) {
	api := &pageAPI{pages: []paperless.Page{
		{Results: []paperless.Document{{ID: 1, Title: "A", Content: "eligible"}, {ID: 2, Title: "B"}}},
		{Results: []paperless.Document{{ID: 2, Title: "B"}, {ID: 3, Title: "C"}}},
		{Results: []paperless.Document{{ID: 1, Title: "A", Tags: []int{9}}}},
	}}
	j := &job{api: api, limits: Limits{Searches: 6, Candidates: 100, Excerpts: 12, Characters: 8000}, docs: map[int]paperless.Document{}, ranks: map[int]float64{}, evidence: map[int][]string{}, excluded: map[int]bool{9: true}}
	ctx := context.Background()
	if _, err := j.Search(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Excerpt(ctx, 1); err != nil {
		t.Fatal(err)
	}
	merged, err := j.Search(ctx, "second")
	if err != nil || len(merged) != 3 || merged[0].ID != 2 {
		t.Fatal("reciprocal rank merge failed", merged, err)
	}
	if _, err = j.Search(ctx, "changed"); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Excerpt(ctx, 1); err == nil {
		t.Fatal("cached excerpt bypassed updated exclusion")
	}
}
func TestLongOCRWindow(t *testing.T) {
	content := strings.Repeat("ordinary text ", 1000) + "Distinctive needle evidence." + strings.Repeat(" closing text", 1000)
	got := excerptWindow(content, "distinctive needle", 400)
	if len([]rune(got)) > 400 || !strings.Contains(got, "needle evidence") || !strings.Contains(content, got) {
		t.Fatal("window is not bounded contiguous matching evidence")
	}

	if got = excerptWindow(content, "notfound", 100); len([]rune(got)) != 100 {
		t.Fatal("fallback window is not bounded")
	}
}

func TestOCRWindowMapsExpandedAccentsToSource(t *testing.T) {
	content := strings.Repeat("cœur ", 200) + "distinctive needle evidence" + strings.Repeat(" closing text", 200)
	got := excerptWindow(content, "distinctive needle", 200)
	if !strings.Contains(got, "distinctive needle evidence") || !strings.Contains(content, got) {
		t.Fatal("expanded normalization hid the evidence")
	}
}

type reserveAPI struct {
	pageAPI
	limits []int
}

func (a *reserveAPI) Metadata(context.Context) (paperless.Vocabulary, error) {
	return paperless.Vocabulary{Tags: []paperless.Term{{ID: 9, Name: "Export financier"}}}, nil
}
func (a *reserveAPI) Search(ctx context.Context, q string, f paperless.Filters, limit int) (paperless.Page, error) {
	a.limits = append(a.limits, limit)
	p, err := a.pageAPI.Search(ctx, q, f, limit)
	p.Results = p.Results[:min(limit, len(p.Results))]
	return p, err
}

type agentFunc func(context.Context, string, Tools) (Ranking, error)

func (f agentFunc) Rank(c context.Context, q string, t Tools) (Ranking, error) { return f(c, q, t) }

func TestOriginalQueryLeavesRoomForExpansions(t *testing.T) {
	broad := paperless.Page{}
	for id := 1; id <= 40; id++ {
		broad.Results = append(broad.Results, paperless.Document{ID: id, Title: "broad"})
	}
	expansion := paperless.Page{Results: []paperless.Document{{ID: 99, Title: "answer"}}}
	api := &reserveAPI{pageAPI: pageAPI{pages: []paperless.Page{broad, expansion}}}
	reached := false
	agent := agentFunc(func(ctx context.Context, _ string, tools Tools) (Ranking, error) {
		cs, err := tools.Search(ctx, "expansion")
		for _, c := range cs {
			reached = reached || c.ID == 99
		}
		return Ranking{Results: []Pick{}}, err
	})
	e := &Engine{API: api, Agent: agent, Limits: Limits{Searches: 6, Candidates: 40, Excerpts: 6, Characters: 2500}}
	if _, err := e.Run(context.Background(), "broad", paperless.Filters{}, false, nil); err != nil || !reached {
		t.Fatal("expansion-only answer was crowded out", api.limits, err)
	}
	if api.limits[0] != 30 || api.limits[1] != 40 {
		t.Fatal("original query did not reserve expansion slots", api.limits)
	}

	api = &reserveAPI{pageAPI: pageAPI{pages: []paperless.Page{broad}}}
	out, err := (&Engine{API: api, Limits: e.Limits}).Run(context.Background(), "broad", paperless.Filters{}, true, nil)
	if err != nil || out.Candidates != 40 {
		t.Fatal("keyword search lost candidates to an unused reserve", out.Candidates, err)
	}
}

func TestSmallPoolsStillReserveExpansionSlots(t *testing.T) {
	for candidates, want := range map[int]int{1: 1, 2: 1, 3: 2, 4: 3, 40: 30} {
		broad := paperless.Page{Results: []paperless.Document{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}}
		api := &reserveAPI{pageAPI: pageAPI{pages: []paperless.Page{broad}}}
		agent := agentFunc(func(context.Context, string, Tools) (Ranking, error) { return Ranking{Results: []Pick{}}, nil })
		e := &Engine{API: api, Agent: agent, Limits: Limits{Searches: 6, Candidates: candidates, Excerpts: 6, Characters: 2500}}
		if _, err := e.Run(context.Background(), "broad", paperless.Filters{}, false, nil); err != nil || api.limits[0] != want {
			t.Fatal("original query page size", candidates, api.limits, err)
		}
	}
}

func TestExpansionRefreshesChargedExcerptWindow(t *testing.T) {
	long := strings.Repeat("filler ", 400) + "alpha evidence here." + strings.Repeat(" filler", 400) + " omega evidence here." + strings.Repeat(" filler", 400)
	doc := paperless.Document{ID: 1, Title: "Long", Content: long}
	api := &pageAPI{pages: []paperless.Page{{Results: []paperless.Document{doc}}, {Results: []paperless.Document{doc}}}}
	j := &job{api: api, limits: Limits{Searches: 6, Candidates: 10, Excerpts: 1, Characters: 300}, docs: map[int]paperless.Document{}, ranks: map[int]float64{}, evidence: map[int][]string{}}
	ctx := context.Background()
	if _, err := j.Search(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	first, err := j.Excerpt(ctx, 1)
	if err != nil || !strings.Contains(first.Text, "alpha evidence") {
		t.Fatal(first, err)
	}
	if _, err = j.Search(ctx, "omega"); err != nil {
		t.Fatal(err)
	}
	second, err := j.Excerpt(ctx, 1)
	if err != nil || !strings.Contains(second.Text, "omega evidence") {
		t.Fatal("expanded window unreadable at zero remaining budget", second, err)
	}
	out, err := j.validate(Ranking{Results: []Pick{{ID: 1, Quote: "alpha evidence here.", Strength: "strong"}}})
	if err != nil || len(out) != 1 {
		t.Fatal("earlier window no longer a valid quote source", err)
	}
	if _, err = j.validate(Ranking{Results: []Pick{{ID: 1, Quote: "omega evidence here.", Strength: "strong"}}}); err != nil {
		t.Fatal(err)
	}
}
