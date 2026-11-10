// Package evaluation measures retrieval on labelled, entirely synthetic data.
package evaluation

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

//go:embed queries.json
var queries []byte

// Case is a human-labelled fixture; Expected=0 means no match.
type Case struct {
	Query    string `json:"query"`
	Expected int    `json:"expected"`
}

// Report distinguishes synthetic model stand-ins from actual inference.
type Report struct {
	Kind                                                                       string `json:"kind"`
	Answerable, NoMatch, BaselineHits, PipelineHits, CorrectNoMatch, Fallbacks int
	Limits                                                                     search.Limits
	ElapsedSeconds                                                             float64
}

// Cases returns the embedded evaluation labels.
func Cases() ([]Case, error) { var c []Case; err := json.Unmarshal(queries, &c); return c, err }

// FakeAgent is a deterministic contract test, not a model-quality evaluation.
type FakeAgent struct{}

// Rank exercises app tools and evidence without external inference.
func (FakeAgent) Rank(ctx context.Context, q string, t search.Tools) (search.Ranking, error) {
	candidates, err := t.Search(ctx, q)
	if err != nil {
		return search.Ranking{}, err
	}
	type evidenceCandidate struct {
		search.Candidate
		quote   string
		overlap int
	}
	var eligible []evidenceCandidate
	words := strings.FieldsFunc(search.Fold(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, c := range candidates[:min(12, len(candidates))] {
		ev, err := t.Excerpt(ctx, c.ID)
		if err != nil {
			return search.Ranking{}, err
		}
		matched, total := 0, 0
		for _, word := range words {
			if len(word) < 3 || strings.Contains(" latest newest dernier derniere for the ", " "+word+" ") {
				continue
			}
			total++
			if strings.Contains(search.Fold(ev.Text), word) {
				matched++
			}
		}
		if total == 0 || matched*100 < total*60 {
			continue
		}
		eligible = append(eligible, evidenceCandidate{c, strings.SplitN(ev.Text, "\n", 2)[0], matched})
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if a.overlap != b.overlap {
			return a.overlap > b.overlap
		}
		sa := search.TitleScore(q, paperless.Document{Title: a.Title})
		sb := search.TitleScore(q, paperless.Document{Title: b.Title})
		if sa != sb {
			return sa > sb
		}
		if search.Latest(q) {
			return a.Date > b.Date
		}
		return a.ID < b.ID
	})
	out := search.Ranking{Results: []search.Pick{}}
	for _, c := range eligible {
		out.Results = append(out.Results, search.Pick{ID: c.ID, Quote: c.quote, Strength: "strong"})
	}
	return out, nil
}

// Run compares the original lexical fixture rank with the full bounded pipeline.
func Run(ctx context.Context, agent search.Agent, kind string) (Report, error) {
	start := time.Now()
	api := demo.New()
	limits := search.Limits{Searches: 6, Candidates: 100, Excerpts: 12, Characters: 8000}
	e := search.Engine{API: api, Agent: agent, Limits: limits}
	report := Report{Kind: kind, Limits: limits}
	cases, err := Cases()
	if err != nil {
		return report, err
	}
	for _, c := range cases {
		base, err := api.Search(ctx, c.Query, paperless.Filters{}, 100)
		if err != nil {
			return report, err
		}
		jobCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		out, err := e.Run(jobCtx, c.Query, paperless.Filters{}, false, nil)
		cancel()
		if err != nil {
			return report, err
		}
		if out.Mode == "Keyword fallback" {
			report.Fallbacks++
		}
		if c.Expected == 0 {
			report.NoMatch++
			if len(out.Results) == 0 && out.Mode == "Evidence-ranked search" {
				report.CorrectNoMatch++
			}
			continue
		}
		report.Answerable++
		for _, d := range base.Results[:min(3, len(base.Results))] {
			if d.ID == c.Expected {
				report.BaselineHits++
				break
			}
		}
		for _, d := range out.Results[:min(3, len(out.Results))] {
			if d.Document.ID == c.Expected && out.Mode == "Evidence-ranked search" {
				report.PipelineHits++
				break
			}
		}
	}
	report.ElapsedSeconds = time.Since(start).Seconds()
	if report.Answerable < 24 || report.NoMatch < 6 || report.PipelineHits*100 < report.Answerable*90 || report.CorrectNoMatch != report.NoMatch || report.Fallbacks != 0 {
		return report, fmt.Errorf("evaluation did not meet Hit@3/no-match/zero-fallback thresholds")
	}
	return report, nil
}
