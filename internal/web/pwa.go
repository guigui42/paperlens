package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

// serviceWorker renders pwa/sw.js with a cache version derived from every
// embedded asset, so each release replaces the previous shell cache.
func serviceWorker(demo bool) ([]byte, error) {
	h := sha256.New()
	var shell []string
	err := fs.WalkDir(assets, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			return err
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
		if strings.HasPrefix(name, "static/") {
			switch path.Ext(name) {
			case ".css", ".js", ".svg", ".png":
				shell = append(shell, "/"+name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if demo {
		h.Write([]byte("demo"))
	}
	list, err := json.Marshal(shell)
	if err != nil {
		return nil, err
	}
	src, err := fs.ReadFile(assets, "pwa/sw.js")
	if err != nil {
		return nil, err
	}
	version := hex.EncodeToString(h.Sum(nil))[:16]
	return []byte(strings.NewReplacer(
		`"__PAPERLENS_VERSION__"`, `"`+version+`"`,
		`["__PAPERLENS_SHELL__"]`, string(list),
	).Replace(string(src))), nil
}

func (s *Server) manifest(w http.ResponseWriter, _ *http.Request) {
	b, err := fs.ReadFile(assets, "pwa/manifest.webmanifest")
	if err != nil {
		http.Error(w, "Manifest unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	if _, err := w.Write(b); err != nil {
		slog.Debug("response disconnected")
	}
}

func (s *Server) serviceWorker(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	if _, err := w.Write(s.sw); err != nil {
		slog.Debug("response disconnected")
	}
}

// offline is precached by the service worker, so it must not issue the
// browser cookie or carry a CSRF token.
func (s *Server) offline(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "offline", page{Title: "Offline", Demo: s.config.Demo})
}
