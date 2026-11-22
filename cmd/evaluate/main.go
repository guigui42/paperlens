package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guigui42/paperlens/internal/config"
	"github.com/guigui42/paperlens/internal/copilot"
	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/evaluation"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

func fail(v ...any) {
	fmt.Fprintln(os.Stderr, v...)
	os.Exit(1)
}

// parseLabels requires an explicit nonnegative expected ID per case so a
// missing, misspelled or null field is not scored as a no-match label.
func parseLabels(raw []byte) ([]evaluation.Case, error) {
	var labels []struct {
		Query    string `json:"query"`
		Expected *int   `json:"expected"`
		Holdout  bool   `json:"holdout"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&labels); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the label array")
	}
	if len(labels) == 0 {
		return nil, errors.New("no labelled cases")
	}
	cases := make([]evaluation.Case, len(labels))
	for i, l := range labels {
		if strings.TrimSpace(l.Query) == "" {
			return nil, fmt.Errorf("case %d: query is required", i)
		}
		if l.Expected == nil {
			return nil, fmt.Errorf("case %d: expected is required (document ID, or 0 for no match)", i)
		}
		if *l.Expected < 0 {
			return nil, fmt.Errorf("case %d: expected must be a document ID or 0 for no match", i)
		}
		cases[i] = evaluation.Case{Query: l.Query, Expected: *l.Expected, Holdout: l.Holdout}
	}
	return cases, nil
}

func main() {
	live := flag.Bool("live", false, "use explicitly configured Copilot identity; incurs model usage")
	set := flag.String("set", "synthetic", "synthetic (gated contract set) or hard (strict-keyword synthetic set, ungated)")
	private := flag.String("private", "", "path to private labels (JSON [{query, expected, holdout}]); runs against the configured real Paperless archive, ungated. Keep the file out of git (for example under .state/)")
	flag.Parse()
	if *set != "synthetic" && *set != "hard" {
		fail("-set must be synthetic or hard")
	}
	var agent search.Agent = evaluation.FakeAgent{}
	kind := "fake-agent contract evaluation; not model quality"
	limits := evaluation.DefaultLimits
	var c config.Config
	if *live || *private != "" {
		if !*live {
			// The fake agent needs no Copilot identity; skip AI configuration checks.
			if err := os.Setenv("PAPERLENS_AI", "false"); err != nil {
				fail(err)
			}
		}
		var err error
		if c, err = config.Load(); err != nil {
			fail(err)
		}
		limits = search.Limits{Searches: c.MaxSearches, Candidates: c.MaxCandidates, Excerpts: c.MaxExcerpts, Characters: c.ExcerptChars}
	}
	if *private != "" && c.Demo {
		fail("private evaluation requires the real Paperless configuration, not PAPERLENS_DEMO=true")
	}
	if *live {
		if *private == "" && !c.Demo {
			fail("live synthetic evaluation requires PAPERLENS_DEMO=true; use -private for the real archive")
		}
		if c.CopilotToken == "" || !filepath.IsAbs(c.CLIPath) {
			fail("live evaluation requires COPILOT_GITHUB_TOKEN[_FILE] and absolute COPILOT_CLI_PATH")
		}
		a := &copilot.Agent{Path: c.CLIPath, Token: c.CopilotToken, Root: c.RuntimeDir, Model: c.Model, Effort: c.ReasoningEffort}
		if err := a.Prepare(); err != nil {
			fail(err)
		}
		defer func() {
			if err := a.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "runtime ownership release failed")
			}
		}()
		warm, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		if err := a.Warm(warm); err != nil {
			fmt.Fprintln(os.Stderr, "copilot package cache warm-up failed")
		}
		cancel()
		agent = a
		effort := c.ReasoningEffort
		if effort == "" {
			effort = "default"
		}
		kind = "live Copilot model: " + c.Model + " (reasoning " + effort + ")"
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	ctx := context.Background()
	if *private != "" {
		raw, err := os.ReadFile(*private)
		if err != nil {
			fail(err)
		}
		cases, err := parseLabels(raw)
		if err != nil {
			fail("private labels:", err)
		}
		client, err := paperless.New(c.PaperlessURL, c.PaperlessToken)
		if err != nil {
			fail(err)
		}
		r, err := evaluation.Evaluate(ctx, client, agent, c.ExcludedTags, limits, cases, kind+"; private archive")
		if e := enc.Encode(r); e != nil {
			fail(e)
		}
		if err != nil {
			fail(err)
		}
		return
	}
	if *set == "hard" {
		cases, err := evaluation.HardCases()
		if err != nil {
			fail(err)
		}
		r, err := evaluation.Evaluate(ctx, evaluation.Strict{API: demo.New()}, agent, nil, limits, cases, kind+"; hard synthetic set, strict keyword semantics")
		if e := enc.Encode(r); e != nil {
			fail(e)
		}
		if err != nil {
			fail(err)
		}
		return
	}
	if *live {
		kind += "; synthetic archive, not actual Paperless index"
	}
	r, err := evaluation.Run(ctx, agent, kind, limits)
	if e := enc.Encode(r); e != nil {
		fail(e)
	}
	if err != nil {
		fail(err)
	}
}
