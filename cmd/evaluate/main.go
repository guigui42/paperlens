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
		a := &copilot.Agent{Path: c.CLIPath, Token: c.CopilotToken, Root: c.RuntimeDir, Model: c.Model}
		if err := a.Prepare(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer func() {
			if err := a.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "runtime ownership release failed")
			}
		}()
		agent = a
		kind = "live Copilot model: " + c.Model + "; synthetic archive, not actual Paperless index"
	}
	r, err := evaluation.Run(context.Background(), agent, kind)
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
