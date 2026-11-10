// Package config validates Paperlens's server-only configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config contains validated configuration. Secrets must never be logged.
type Config struct {
	Listen, Origin, PaperlessURL, PaperlessToken, CopilotToken string
	Model, CLIPath, StateDir, RuntimeDir                       string
	Demo, AI, LANOnly                                          bool
	MaxUpload                                                  int64
	MaxSearches, MaxCandidates, MaxExcerpts, ExcerptChars      int
	Concurrency, Queue                                         int
	Deadline                                                   time.Duration
	UploadTypes                                                []string
}

// Load reads environment variables and optional secret files.
func Load() (Config, error) {
	c := Config{
		Listen:       env("PAPERLENS_LISTEN", "127.0.0.1:8080"),
		Origin:       env("PAPERLENS_ORIGIN", "http://localhost:8080"),
		PaperlessURL: strings.TrimRight(os.Getenv("PAPERLESS_URL"), "/"),
		Model:        env("COPILOT_MODEL", "gpt-5.4"), CLIPath: os.Getenv("COPILOT_CLI_PATH"),
		StateDir: env("PAPERLENS_STATE_DIR", ".state"), RuntimeDir: env("PAPERLENS_RUNTIME_DIR", ""),
		Demo: os.Getenv("PAPERLENS_DEMO") == "true", AI: os.Getenv("PAPERLENS_AI") != "false",
		LANOnly: os.Getenv("PAPERLENS_LAN_ONLY") == "true",
	}
	var err error
	if c.PaperlessToken, err = secret("PAPERLESS_TOKEN"); err != nil {
		return c, err
	}
	if c.CopilotToken, err = secret("COPILOT_GITHUB_TOKEN"); err != nil {
		return c, err
	}
	values := []struct {
		name          string
		dst           *int
		def, min, max int
	}{
		{"PAPERLENS_MAX_SEARCHES", &c.MaxSearches, 6, 1, 6},
		{"PAPERLENS_MAX_CANDIDATES", &c.MaxCandidates, 100, 1, 100},
		{"PAPERLENS_MAX_EXCERPTS", &c.MaxExcerpts, 12, 1, 12},
		{"PAPERLENS_EXCERPT_CHARS", &c.ExcerptChars, 8000, 100, 8000},
		{"PAPERLENS_CONCURRENCY", &c.Concurrency, 2, 1, 8},
		{"PAPERLENS_QUEUE", &c.Queue, 4, 0, 16},
	}
	for _, v := range values {
		n, e := strconv.Atoi(env(v.name, strconv.Itoa(v.def)))
		if e != nil || n < v.min || n > v.max {
			return c, fmt.Errorf("%s must be between %d and %d", v.name, v.min, v.max)
		}
		*v.dst = n
	}
	c.Deadline, err = time.ParseDuration(env("PAPERLENS_SEARCH_TIMEOUT", "90s"))
	if err != nil || c.Deadline < time.Second || c.Deadline > 3*time.Minute {
		return c, errors.New("PAPERLENS_SEARCH_TIMEOUT must be 1s to 3m")
	}
	c.MaxUpload, err = strconv.ParseInt(env("PAPERLENS_MAX_UPLOAD_MB", "20"), 10, 64)
	if err != nil || c.MaxUpload < 1 || c.MaxUpload > 100 {
		return c, errors.New("PAPERLENS_MAX_UPLOAD_MB must be 1 to 100")
	}
	c.MaxUpload *= 1024 * 1024
	if _, err = origin(c.Origin); err != nil {
		return c, fmt.Errorf("PAPERLENS_ORIGIN: %w", err)
	}
	c.StateDir, err = filepath.Abs(c.StateDir)
	if err != nil {
		return c, err
	}
	if c.RuntimeDir == "" {
		c.RuntimeDir = filepath.Join(c.StateDir, "runtime")
	}
	c.RuntimeDir, err = filepath.Abs(c.RuntimeDir)
	if err != nil {
		return c, err
	}
	if c.Demo {
		c.AI = false
		c.UploadTypes = []string{"pdf", "png", "jpg", "jpeg"}
		return c, nil
	}
	if !c.LANOnly {
		return c, errors.New("set PAPERLENS_LAN_ONLY=true only after restricting access to your LAN/VPN; there is no login")
	}
	if _, err = origin(c.PaperlessURL); err != nil {
		return c, fmt.Errorf("PAPERLESS_URL: %w", err)
	}
	if c.PaperlessToken == "" {
		return c, errors.New("PAPERLESS_TOKEN or PAPERLESS_TOKEN_FILE is required for a dedicated integration account")
	}
	if c.AI && (c.CopilotToken == "" || !filepath.IsAbs(c.CLIPath)) {
		return c, errors.New("AI requires COPILOT_GITHUB_TOKEN[_FILE] and an absolute COPILOT_CLI_PATH; use PAPERLENS_AI=false for keyword-only mode")
	}
	if raw := os.Getenv("PAPERLENS_UPLOAD_TYPES"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			switch t {
			case "pdf", "png", "jpg", "jpeg", "tif", "tiff":
			default:
				return c, errors.New("unsupported PAPERLENS_UPLOAD_TYPES entry")
			}
			c.UploadTypes = append(c.UploadTypes, t)
		}
	}
	return c, nil
}

func origin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return u, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func secret(key string) (string, error) {
	value, file := os.Getenv(key), os.Getenv(key+"_FILE")
	if value != "" && file != "" {
		return "", fmt.Errorf("set only %s or %s_FILE", key, key)
	}
	if file == "" {
		return value, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("cannot read %s_FILE", key)
	}
	if len(b) > 16384 {
		return "", fmt.Errorf("%s_FILE too large", key)
	}
	return strings.TrimSpace(string(b)), nil
}
