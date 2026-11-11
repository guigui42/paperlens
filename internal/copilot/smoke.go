package copilot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

type smokeTools struct{}

func (smokeTools) Seed() search.Seed { return search.Seed{} }
func (smokeTools) Limits() search.Limits {
	return search.Limits{Searches: 6, Candidates: 40, Excerpts: 6, Characters: 2500}
}
func (smokeTools) Lookup() paperless.Vocabulary { return paperless.Vocabulary{} }
func (smokeTools) Search(context.Context, string) ([]search.Candidate, error) {
	return []search.Candidate{}, nil
}
func (smokeTools) Excerpt(context.Context, int) (search.Evidence, error) {
	return search.Evidence{}, errors.New("no smoke-test documents")
}

// Smoke checks the pinned runtime without credentials or a model request.
func Smoke(ctx context.Context, path string) (err error) {
	if !filepath.IsAbs(path) {
		return errors.New("absolute COPILOT_CLI_PATH required")
	}
	parent := os.Getenv("PAPERLENS_RUNTIME_DIR")
	if parent != "" {
		if !filepath.IsAbs(parent) {
			return errors.New("absolute PAPERLENS_RUNTIME_DIR required")
		}
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
	}
	root, err := os.MkdirTemp(parent, "search-check-")
	if err != nil {
		return err
	}
	a := Agent{Path: path, Root: root}
	client := a.client(root)
	var session *sdk.Session
	defer func() {
		var target cleanupSession
		var id string
		if session != nil {
			target, id = session, session.SessionID
		}
		err = errors.Join(err, a.cleanupRuntime(ctx.Err() != nil, root, id, target, client, 30*time.Second))
	}()
	if err = client.Start(ctx); err != nil {
		return fmt.Errorf("cannot start credential-free CLI smoke runtime: %w", err)
	}
	if _, err = client.Ping(ctx, "synthetic runtime check"); err != nil {
		return errors.New("runtime protocol handshake failed")
	}
	session, err = client.CreateSession(ctx, sessionConfig(ctx, root, "gpt-5.4", smokeTools{}))
	if err != nil {
		return errors.New("restricted session creation failed")
	}
	if err = verifyToolSurface(ctx, session); err != nil {
		return err
	}
	result, err := session.RPC.Tools.Execute(ctx, &rpc.ToolsExecuteRequest{Name: "lookup_metadata", Arguments: map[string]any{}})
	if err != nil {
		return fmt.Errorf("application tool invocation failed: %w", err)
	}
	expanded, ok := result.(*rpc.ToolResultExpanded)
	if !ok || expanded.ResultType != "success" {
		return errors.New("application tool invocation did not succeed")
	}
	result, err = session.RPC.Tools.Execute(ctx, &rpc.ToolsExecuteRequest{Name: "bash", Arguments: map[string]any{"command": "printf forbidden-tool"}})
	if err == nil {
		if expanded, ok = result.(*rpc.ToolResultExpanded); !ok || expanded.ResultType == "success" {
			return errors.New("forbidden tool was not rejected")
		}
	}
	return nil
}
