package web

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserKeyConcurrentAtomicCreation(t *testing.T) {
	dir := t.TempDir()
	keys := make(chan []byte, 12)
	errs := make(chan error, 12)
	for range 12 {
		go func() { key, err := keyFile(dir); keys <- key; errs <- err }()
	}
	var first []byte
	for range 12 {
		key := <-keys
		err := <-errs
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = key
		}
		if len(key) != 32 || !bytes.Equal(first, key) {
			t.Fatal("concurrent startup published different keys")
		}
	}
	stored, err := os.ReadFile(filepath.Join(dir, "browser.key"))
	if err != nil || !bytes.Equal(stored, first) {
		t.Fatal("incomplete key published", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary key files remain", err)
	}
	info, err := os.Stat(filepath.Join(dir, "browser.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe key mode")
	}
}
