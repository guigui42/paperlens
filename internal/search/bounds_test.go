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
	j := &job{api: api, limits: Limits{Searches: 6, Candidates: 100, Excerpts: 12, Characters: 8000}, docs: map[int]paperless.Document{}, ranks: map[int]float64{}, evidence: map[int]string{}, excluded: map[int]bool{9: true}}
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
