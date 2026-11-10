package copilot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeOwnershipProtectsActiveSessions(t *testing.T) {
	root := t.TempDir()
	first := &Agent{Root: root}
	if err := first.Prepare(); err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	dir := filepath.Join(root, "search-active")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(sentinel, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	second := &Agent{Root: root}
	if err := second.Prepare(); err == nil {
		second.Close()
		t.Fatal("second process acquired the runtime root")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("active runtime state was removed")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Prepare(); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("confirmed orphan was not removed")
	}
}

func TestKeywordOnlyRuntimeCleanupNeedsNoCLI(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "search-orphan")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &Agent{Root: root}
	if err := a.Prepare(); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("keyword-only startup left runtime data")
	}
}
