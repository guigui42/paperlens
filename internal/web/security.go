package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func randomID() string { return rand.Text() }
func keyFile(dir string) ([]byte, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "browser.key")
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("invalid browser signing key")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}
func (s *Server) sign(value string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (s *Server) browser(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie("paperlens_browser"); err == nil {
		parts := strings.Split(cookie.Value, ".")
		if len(parts) == 2 && len(parts[0]) == 26 && hmac.Equal([]byte(parts[1]), []byte(s.sign("browser:"+parts[0]))) {
			return parts[0]
		}
	}
	id := randomID()
	http.SetCookie(w, &http.Cookie{Name: "paperlens_browser", Value: id + "." + s.sign("browser:"+id), Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.config.Origin, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 60 * 60})
	return id
}
func (s *Server) protect(next http.Handler) http.Handler {
	origin, _ := url.Parse(s.config.Origin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; frame-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'; object-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.Host != origin.Host {
			http.Error(w, "Host not allowed. Use the canonical address configured in PAPERLENS_ORIGIN.", http.StatusForbidden)
			return
		}
		if !s.jobs.accepting() {
			http.Error(w, "Server is shutting down; no new work is accepted.", http.StatusServiceUnavailable)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("Origin") != s.config.Origin {
				site := r.Header.Get("Sec-Fetch-Site")
				switch site {
				case "", "same-origin", "same-site", "cross-site", "none":
				default:
					site = "other"
				}
				referrerMatches := false
				if ref, err := url.Parse(r.Referer()); err == nil {
					referrerMatches = ref.Scheme == origin.Scheme && ref.Host == origin.Host && ref.User == nil
				}
				slog.Warn("request origin rejected",
					"origin_missing", r.Header.Get("Origin") == "",
					"origin_opaque", r.Header.Get("Origin") == "null",
					"referrer_missing", r.Referer() == "",
					"referrer_matches", referrerMatches,
					"host_matches", r.Host == origin.Host,
					"fetch_site", site)
				http.Error(w, "Same-origin request required", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) csrf(r *http.Request, owner string) bool {
	return hmac.Equal([]byte(r.FormValue("csrf")), []byte(s.sign("csrf:"+owner)))
}
