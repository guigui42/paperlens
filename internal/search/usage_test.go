package search_test

import (
	"context"
	"errors"
	"testing"

	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

func TestUsageStatusAndDeduplication(t *testing.T) {
	var m search.Meter
	if u := m.Snapshot(); u.Status() != search.CreditsNone || u.Label() != "0 AI credits (no AI used)" {
		t.Fatal(u.Status(), u.Label())
	}
	m.Inference()
	if u := m.Snapshot(); u.Status() != search.CreditsUnavailable {
		t.Fatal("an attempted run without billing must not claim zero:", u.Status())
	}
	m.Record("a", true, false, 2.5e9)
	m.Record("a", true, false, 2.5e9)
	m.Record("b", true, false, 0)
	if u := m.Snapshot(); u.Calls != 2 || u.Status() != search.CreditsComplete || u.Label() != "2.50 AI credits" {
		t.Fatal(u, u.Label())
	}
	m.Record("c", false, false, 0)
	if u := m.Snapshot(); u.Status() != search.CreditsPartial || u.Label() != "at least 2.50 AI credits (partial report)" {
		t.Fatal(u.Status(), u.Label())
	}
	var zero search.Meter
	zero.Record("", true, false, 0)
	if u := zero.Snapshot(); u.Label() != "0 AI credits" {
		t.Fatal("a reported zero is a real zero:", u.Label())
	}
	if got := search.FormatCredits(0.00042); got != "0.00042" {
		t.Fatal("small charges must stay visible:", got)
	}
}

func TestUsageSurvivesFallbackAndCancellation(t *testing.T) {
	fail := engine(agentFunc(func(ctx context.Context, _ string, _ search.Tools) (search.Ranking, error) {
		search.MeterFrom(ctx).Record("x", true, false, 1e9)
		return search.Ranking{Results: []search.Pick{{ID: 999, Quote: "invented", Strength: "strong"}}}, nil
	}))
	out, err := fail.Run(context.Background(), "home insurance", paperless.Filters{}, false, nil)
	if err != nil || out.Mode != "Keyword fallback" || out.Usage.Credits() != 1 {
		t.Fatal(out.Mode, out.Usage, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := engine(agentFunc(func(ctx context.Context, _ string, _ search.Tools) (search.Ranking, error) {
		search.MeterFrom(ctx).Record("y", true, false, 3e9)
		cancel()
		return search.Ranking{}, errors.New("interrupted")
	}))
	out, err = cancelled.Run(ctx, "home insurance", paperless.Filters{}, false, nil)
	if err == nil || out.Usage.Credits() != 3 {
		t.Fatal("cancelled run lost its charge:", out.Usage, err)
	}
	out, _ = cancelled.Run(context.Background(), "home insurance", paperless.Filters{}, true, nil)
	if out.Usage.Status() != search.CreditsNone {
		t.Fatal("keyword search reported AI usage")
	}
}
