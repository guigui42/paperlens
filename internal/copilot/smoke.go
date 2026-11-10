package copilot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

type smokeTools struct{}

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
	defer func() { err = errors.Join(err, os.RemoveAll(root)) }()
	a := Agent{Path: path, Root: root}
	client := a.client(root)
	defer func() { err = errors.Join(err, client.Stop()) }()
	if err = client.Start(ctx); err != nil {
		return fmt.Errorf("cannot start credential-free CLI smoke runtime: %w", err)
	}
	if _, err = client.Ping(ctx, "synthetic runtime check"); err != nil {
		return errors.New("runtime protocol handshake failed")
	}
	session, err := client.CreateSession(ctx, sessionConfig(ctx, root, "gpt-5.4", smokeTools{}))
	if err != nil {
		return errors.New("restricted session creation failed")
	}
	defer func() {
		err = errors.Join(err, session.Disconnect())
		err = errors.Join(err, client.DeleteSession(ctx, session.SessionID))
	}()
	return verifyToolSurface(ctx, session)
}
