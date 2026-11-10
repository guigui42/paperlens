package config

import (
	"os"
	"path/filepath"
	"testing"
)

func defaults(t *testing.T) {
	t.Helper()
	for _, key := range []string{"PAPERLESS_URL", "PAPERLESS_TOKEN", "PAPERLESS_TOKEN_FILE", "COPILOT_GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN_FILE", "COPILOT_CLI_PATH", "PAPERLENS_DEMO", "PAPERLENS_AI", "PAPERLENS_LAN_ONLY", "PAPERLENS_ORIGIN", "PAPERLENS_UPLOAD_TYPES", "PAPERLENS_SEARCH_TIMEOUT", "PAPERLENS_MAX_UPLOAD_MB", "PAPERLENS_MAX_SEARCHES", "PAPERLENS_MAX_CANDIDATES", "PAPERLENS_MAX_EXCERPTS", "PAPERLENS_EXCERPT_CHARS", "PAPERLENS_CONCURRENCY", "PAPERLENS_QUEUE"} {
		t.Setenv(key, "")
	}
	t.Setenv("PAPERLENS_STATE_DIR", t.TempDir())
}
func TestConfigGuards(t *testing.T) {
	defaults(t)
	if _, err := Load(); err == nil {
		t.Fatal("LAN boundary not acknowledged")
	}
	t.Setenv("PAPERLENS_LAN_ONLY", "true")
	t.Setenv("PAPERLESS_URL", "http://paperless.example")
	if _, err := Load(); err == nil {
		t.Fatal("missing Paperless identity accepted")
	}
	t.Setenv("PAPERLESS_TOKEN", "synthetic")
	if _, err := Load(); err == nil {
		t.Fatal("missing Copilot identity accepted")
	}
	t.Setenv("PAPERLENS_AI", "false")
	c, err := Load()
	if err != nil || c.AI || len(c.UploadTypes) != 0 {
		t.Fatal(c, err)
	}
	t.Setenv("PAPERLENS_UPLOAD_TYPES", "pdf,png")
	c, err = Load()
	if err != nil || len(c.UploadTypes) != 2 {
		t.Fatal(err)
	}
	t.Setenv("PAPERLENS_UPLOAD_TYPES", "html")
	if _, err = Load(); err == nil {
		t.Fatal("unsafe upload type accepted")
	}
}
func TestDemoAndValidation(t *testing.T) {
	for _, tc := range []struct{ key, val string }{{"PAPERLENS_ORIGIN", "http://user:pass@example.com"}, {"PAPERLENS_MAX_SEARCHES", "99"}, {"PAPERLENS_MAX_UPLOAD_MB", "0"}, {"PAPERLENS_SEARCH_TIMEOUT", "1h"}} {
		t.Run(tc.key, func(t *testing.T) {
			defaults(t)
			t.Setenv("PAPERLENS_DEMO", "true")
			t.Setenv(tc.key, tc.val)
			if _, err := Load(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	t.Run("valid demo", func(t *testing.T) {
		defaults(t)
		t.Setenv("PAPERLENS_DEMO", "true")
		c, err := Load()
		if err != nil || c.AI || !filepath.IsAbs(c.StateDir) {
			t.Fatal(c, err)
		}
	})
}
func TestSecretFiles(t *testing.T) {
	defaults(t)
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("synthetic-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAPERLESS_TOKEN_FILE", file)
	v, err := secret("PAPERLESS_TOKEN")
	if err != nil || v != "synthetic-token" {
		t.Fatal("secret file not read")
	}
	t.Setenv("PAPERLESS_TOKEN", "ambiguous")
	if _, err = secret("PAPERLESS_TOKEN"); err == nil {
		t.Fatal("ambiguous token accepted")
	}
	t.Setenv("PAPERLESS_TOKEN", "")
	t.Setenv("PAPERLESS_TOKEN_FILE", file+"-missing")
	if _, err = secret("PAPERLESS_TOKEN"); err == nil {
		t.Fatal("missing token accepted")
	}
}
