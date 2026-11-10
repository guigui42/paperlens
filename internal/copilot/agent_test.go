package copilot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

type emptyTools struct{}

func (emptyTools) Lookup() paperless.Vocabulary                               { return paperless.Vocabulary{} }
func (emptyTools) Search(context.Context, string) ([]search.Candidate, error) { return nil, nil }
func (emptyTools) Excerpt(context.Context, int) (search.Evidence, error) {
	return search.Evidence{}, nil
}

func TestPolicyDefaults(t *testing.T) {
	c := sessionConfig(context.Background(), t.TempDir(), "gpt-5.4", emptyTools{})
	if len(c.AvailableTools) != 3 || *c.EnableConfigDiscovery || *c.EnableSkills || *c.EnableFileHooks || *c.EnableSessionStore || *c.InfiniteSessions.Enabled || c.OnUserInputRequest != nil {
		t.Fatal("unsafe session options")
	}
	for _, name := range []string{"shell", "view", "web_fetch", "task", "mcp:github", "memory", "ask_user"} {
		out, err := c.Hooks.OnPreToolUse(sdk.PreToolUseHookInput{ToolName: name}, sdk.HookInvocation{})
		if err != nil || out.PermissionDecision != "deny" {
			t.Fatal("tool escaped allowlist", name)
		}
	}
	for _, name := range []string{"lookup_metadata", "search_documents", "read_document_excerpt"} {
		out, err := c.Hooks.OnPreToolUse(sdk.PreToolUseHookInput{ToolName: name}, sdk.HookInvocation{})
		if err != nil || out.PermissionDecision != "allow" {
			t.Fatal(name)
		}
	}
	decision, err := c.OnPermissionRequest(nil, sdk.PermissionInvocation{})
	if err != nil || string(decision.Kind()) != "denied-by-rules" {
		t.Fatal(decision, err)
	}
}
func TestPrepareOnlyDedicatedStorage(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "search-orphan"), 0700)
	os.WriteFile(filepath.Join(root, "search-orphan", "events.jsonl"), []byte("synthetic"), 0600)
	a := Agent{Path: "/example/copilot", Root: root}
	if err := a.Prepare(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("orphan transcript remains")
	}
	os.WriteFile(filepath.Join(root, "not-ours"), []byte("safe"), 0600)
	if err := a.Prepare(); err == nil {
		t.Fatal("non-dedicated storage accepted")
	}
}
func TestPinnedRuntimeLifecycle(t *testing.T) {
	path := os.Getenv("PAPERLENS_TEST_CLI")
	if path == "" {
		t.Skip("set PAPERLENS_TEST_CLI to the checksum-verified v1.0.105 release executable")
	}
	root := t.TempDir()
	a := Agent{Path: path, Root: root}
	if err := a.Prepare(); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "search-")
	if err != nil {
		t.Fatal(err)
	}
	client := a.client(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	if _, err = client.Ping(ctx, "synthetic lifecycle check"); err != nil {
		t.Fatal(err)
	}
	session, err := client.CreateSession(ctx, sessionConfig(ctx, dir, "gpt-5.4", emptyTools{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.RPC.Tools.InitializeAndValidate(ctx); err != nil {
		t.Fatal(err)
	}
	metadata, err := session.RPC.Tools.GetCurrentMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Tools) != 3 {
		t.Fatalf("unexpected runtime tools: %+v", metadata.Tools)
	}
	for _, tool := range metadata.Tools {
		switch tool.Name {
		case "lookup_metadata", "search_documents", "read_document_excerpt":
		default:
			t.Fatalf("unexpected tool %s", tool.Name)
		}
	}
	if err = session.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if err = client.DeleteSession(ctx, session.SessionID); err != nil {
		t.Fatal(err)
	}
	if err = client.Stop(); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("runtime data survived cleanup")
	}
}
