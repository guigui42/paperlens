package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSameOriginReferrerPolicyAndStrictMutationOrigins(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.Get(h.origin + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := res.Header.Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("same-origin fetches must retain a non-opaque Origin: policy=%q", got)
	}
	_, home := h.request(t, "GET", "/", "", "")
	csrf := hidden(t, home, "csrf")
	for _, path := range []string{"/search", "/jobs/unknown/cancel", "/upload"} {
		for _, origin := range []string{"", "null", "https://foreign.example", strings.Replace(h.origin, "127.0.0.1", "localhost", 1)} {
			t.Run(path+"/"+origin, func(t *testing.T) {
				req, _ := http.NewRequest("POST", h.origin+path, strings.NewReader(url.Values{"csrf": {csrf}, "q": {"synthetic"}}.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("Origin", origin)
				req.Header.Set("Referer", h.origin+"/")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				res, err := h.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				if res.StatusCode != http.StatusForbidden {
					t.Fatalf("invalid Origin accepted: %d", res.StatusCode)
				}
			})
		}
	}
}

func TestCanonicalOriginDoesNotReplaceCSRF(t *testing.T) {
	h := newHarness(t)
	h.request(t, "GET", "/", "", "")
	for _, path := range []string{"/search", "/jobs/unknown/cancel"} {
		status, _ := h.request(t, "POST", path, "q=synthetic", "application/x-www-form-urlencoded")
		if status != http.StatusForbidden {
			t.Fatalf("missing CSRF accepted on %s: %d", path, status)
		}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf", "wrong"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	status, _ := h.request(t, "POST", "/upload", body.String(), writer.FormDataContentType())
	if status != http.StatusForbidden {
		t.Fatalf("missing upload CSRF accepted: %d", status)
	}
}
