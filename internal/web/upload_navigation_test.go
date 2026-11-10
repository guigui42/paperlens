package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/guigui42/paperlens/internal/demo"
)

func TestUploadWithoutHTMXKeepsFullNavigation(t *testing.T) {
	for _, valid := range []bool{false, true} {
		name := "invalid-file"
		if valid {
			name = "valid-file"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, home := h.request(t, "GET", "/upload", "", "")
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			if err := form.WriteField("csrf", hidden(t, home, "csrf")); err != nil {
				t.Fatal(err)
			}
			if err := form.WriteField("nonce", hidden(t, home, "nonce")); err != nil {
				t.Fatal(err)
			}
			file, err := form.CreateFormFile("document", "synthetic.pdf")
			if err != nil {
				t.Fatal(err)
			}
			data := []byte("not a PDF")
			if valid {
				data = demo.PDF()
			}
			if _, err = file.Write(data); err != nil {
				t.Fatal(err)
			}
			if err = form.Close(); err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest("POST", h.origin+"/upload", &body)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", h.origin)
			req.Header.Set("Content-Type", form.FormDataContentType())
			res, err := h.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			content, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			html := string(content)
			if !strings.Contains(html, "<!doctype html>") || !strings.Contains(html, "<header") {
				t.Fatal("non-HTMX upload returned a bare fragment")
			}
			if valid {
				if res.Request.Method != "GET" || !strings.HasPrefix(res.Request.URL.Path, "/uploads/") || !strings.Contains(html, "Document stored.") {
					t.Fatal("successful upload did not redirect to its receipt")
				}
			} else {
				if !strings.Contains(html, "contents do not match") || !strings.Contains(html, `name="nonce"`) {
					t.Fatal("invalid upload lost its error or retry form")
				}
			}
		})
	}
}
