package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/guigui42/paperlens/internal/config"
	"github.com/guigui42/paperlens/internal/copilot"
	"github.com/guigui42/paperlens/internal/evaluation"
	"github.com/guigui42/paperlens/internal/search"
)

func main() {
	live := flag.Bool("live", false, "use explicitly configured Copilot identity; sends synthetic data only and incurs model usage")
	flag.Parse()
	var agent search.Agent = evaluation.FakeAgent{}
	kind := "fake-agent contract evaluation; not model quality"
	limits := evaluation.DefaultLimits
	if *live {
		c, err := config.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !c.Demo || c.CopilotToken == "" || !filepath.IsAbs(c.CLIPath) {
			fmt.Fprintln(os.Stderr, "live evaluation requires PAPERLENS_DEMO=true, COPILOT_GITHUB_TOKEN[_FILE], and absolute COPILOT_CLI_PATH")
			os.Exit(1)
		}
		a := &copilot.Agent{Path: c.CLIPath, Token: c.CopilotToken, Root: c.RuntimeDir, Model: c.Model, Effort: c.ReasoningEffort}
		if err := a.Prepare(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer func() {
			if err := a.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "runtime ownership release failed")
			}
		}()
		if err := a.Warm(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "copilot package cache warm-up failed")
		}
		agent = a
		limits = search.Limits{Searches: c.MaxSearches, Candidates: c.MaxCandidates, Excerpts: c.MaxExcerpts, Characters: c.ExcerptChars}
		effort := c.ReasoningEffort
		if effort == "" {
			effort = "default"
		}
		kind = "live Copilot model: " + c.Model + " (reasoning " + effort + "); synthetic archive, not actual Paperless index"
	}
	r, err := evaluation.Run(context.Background(), agent, kind, limits)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if e := enc.Encode(r); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
