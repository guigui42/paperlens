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

//go:embed hard.json
var hard []byte

// Case is a human-labelled fixture; Expected=0 means no match. Holdout cases
// are scored separately and must not guide changes.
type Case struct {
	Query    string `json:"query"`
	Expected int    `json:"expected"`
	Holdout  bool   `json:"holdout,omitempty"`
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

// HardCases returns synthetic labels that Paperless-style strict keyword
// search misses, so context-aware retrieval has room to show a difference.
func HardCases() ([]Case, error) { var c []Case; err := json.Unmarshal(hard, &c); return c, err }

// CaseResult explains one case without document titles or text.
type CaseResult struct {
	Query     string  `json:"query"`
	Expected  int     `json:"expected"`
	Holdout   bool    `json:"holdout,omitempty"`
	Retrieved bool    `json:"retrieved"`
	Rank      int     `json:"rank"`
	Top       []int   `json:"top"`
	Mode      string  `json:"mode"`
	Calls     int     `json:"calls"`
	NanoAIU   float64 `json:"nano_aiu"`
	Seconds   float64 `json:"seconds"`
}

// Score aggregates one split. Retrieved counts answerable cases whose expected
// document came back from any Paperless search in the job.
type Score struct {
	Answerable, Retrieved, Hit3, NoMatch, CorrectNoMatch, Fallbacks int
}

// Detailed is an ungated per-case report for iterating on search quality.
type Detailed struct {
	Kind           string `json:"kind"`
	Limits         search.Limits
	Dev, Holdout   Score
	NanoAIU        float64      `json:"nano_aiu"`
	ElapsedSeconds float64      `json:"elapsed_seconds"`
	Cases          []CaseResult `json:"cases"`
}

// Evaluate runs each case through the production engine. Fallbacks count as
// misses, as in Run.
func Evaluate(ctx context.Context, api paperless.API, agent search.Agent, excluded []string, limits search.Limits, cases []Case, kind string) (Detailed, error) {
	start := time.Now()
	rec := &recorder{API: api}
	e := search.Engine{API: rec, Agent: agent, Limits: limits, ExcludedTags: excluded}
	report := Detailed{Kind: kind, Limits: limits, Cases: []CaseResult{}}
	for _, c := range cases {
		rec.reset()
		caseStart := time.Now()
		jobCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		out, err := e.Run(jobCtx, c.Query, paperless.Filters{}, false, nil)
		cancel()
		if err != nil {
			return report, fmt.Errorf("case %q: %w", c.Query, err)
		}
		r := CaseResult{Query: c.Query, Expected: c.Expected, Holdout: c.Holdout, Mode: out.Mode, Calls: out.Calls,
			NanoAIU: out.Usage.NanoAIU, Seconds: time.Since(caseStart).Seconds(), Top: []int{}}
		var kept []int
		for _, res := range out.Results {
			if !res.Excluded {
				kept = append(kept, res.Document.ID)
			}
		}
		r.Top = append(r.Top, kept[:min(3, len(kept))]...)
		for i, id := range kept {
			if id == c.Expected {
				r.Rank = i + 1
				break
			}
		}
		s := &report.Dev
		if c.Holdout {
			s = &report.Holdout
		}
		ranked := out.Mode == "Evidence-ranked search"
		if !ranked {
			s.Fallbacks++
		}
		if c.Expected == 0 {
			s.NoMatch++
			if len(kept) == 0 && ranked {
				s.CorrectNoMatch++
			}
		} else {
			s.Answerable++
			r.Retrieved = rec.retrieved(c.Expected)
			if r.Retrieved {
				s.Retrieved++
			}
			if ranked && r.Rank >= 1 && r.Rank <= 3 {
				s.Hit3++
			}
		}
		report.NanoAIU += r.NanoAIU
		report.Cases = append(report.Cases, r)
	}
	report.ElapsedSeconds = time.Since(start).Seconds()
	return report, nil
}

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
	// Prefetched excerpts are free to reread, so evaluate them first and then
	// spend the remaining budget on other candidates, like a model would.
	seeded := map[int]bool{}
	for _, ev := range t.Seed().Excerpts {
		seeded[ev.ID] = true
	}
	sort.SliceStable(candidates, func(i, j int) bool { return seeded[candidates[i].ID] && !seeded[candidates[j].ID] })
	for _, c := range candidates[:min(t.Limits().Excerpts, len(candidates))] {
		ev, err := t.Excerpt(ctx, c.ID)
		if err != nil {
			break
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

// DefaultLimits mirrors the built-in configuration defaults.
var DefaultLimits = search.Limits{Searches: 6, Candidates: 40, Excerpts: 6, Characters: 2500}

// Run compares the original lexical fixture rank with the full bounded pipeline.
func Run(ctx context.Context, agent search.Agent, kind string, limits search.Limits) (Report, error) {
	start := time.Now()
	api := demo.New()
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
