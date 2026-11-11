package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/guigui42/paperlens/internal/config"
	"github.com/guigui42/paperlens/internal/demo"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

type harness struct {
	app    *Server
	server *httptest.Server
	client *http.Client
	origin string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	origin := "http://" + srv.Listener.Addr().String()
	c := config.Config{Origin: origin, StateDir: t.TempDir(), Demo: true, Concurrency: 1, Queue: 1, Deadline: 2 * time.Second, MaxUpload: 1 << 20, UploadTypes: []string{"pdf", "png"}}
	api := demo.New()
	e := &search.Engine{API: api, Limits: search.Limits{Searches: 6, Candidates: 100, Excerpts: 12, Characters: 8000}}
	app, err := New(c, api, e)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = app.Handler()
	srv.Start()
	t.Cleanup(func() { app.Close(); srv.Close() })
	jar, _ := cookiejar.New(nil)
	return &harness{app, srv, &http.Client{Jar: jar, Timeout: 5 * time.Second}, origin}
}
func (h *harness) request(t *testing.T, method, path, body, content string) (int, string) {
	t.Helper()
	r, err := http.NewRequest(method, h.origin+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", h.origin)
	r.Header.Set("Content-Type", content)
	r.Header.Set("HX-Request", "true")
	res, err := h.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}
func hidden(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(body)
	if len(m) < 2 {
		t.Fatalf("missing %s in %s", name, body)
	}
	return m[1]
}
func TestSecurityAndSearchOwnership(t *testing.T) {
	h := newHarness(t)
	_, home := h.request(t, "GET", "/", "", "")
	csrf := hidden(t, home, "csrf")
	for _, bad := range []struct{ host, origin, csrf string }{{"evil.example", h.origin, csrf}, {"", "https://evil.example", csrf}, {"", h.origin, "bad"}} {
		req, _ := http.NewRequest("POST", h.origin+"/search", strings.NewReader(url.Values{"q": {"insurance"}, "csrf": {bad.csrf}}.Encode()))
		req.Header.Set("Origin", bad.origin)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if bad.host != "" {
			req.Host = bad.host
		}
		res, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("security check bypassed %d", res.StatusCode)
		}
	}
	code, body := h.request(t, "POST", "/search", url.Values{"q": {"latest home insurance"}, "csrf": {csrf}}.Encode(), "application/x-www-form-urlencoded")
	if code != 200 {
		t.Fatal(code, body)
	}
	id := regexp.MustCompile(`hx-get="/jobs/([^"]+)"`).FindStringSubmatch(body)[1]
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		_, body = h.request(t, "GET", "/jobs/"+id, "", "")
		if strings.Contains(body, "Documents for") {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("job never finished")
		case <-ticker.C:
		}
	}
	if !strings.Contains(body, "Home insurance certificate 2025") {
		t.Fatal(body)
	}
	req, _ := http.NewRequest("GET", h.origin+"/jobs/"+id, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(b), "Home insurance certificate") {
		t.Fatal("cross-browser job disclosed")
	}
	if _, body = h.request(t, "GET", "/jobs/"+id+"?page=-999", "", ""); !strings.Contains(body, "Home insurance") {
		t.Fatal("pagination broke")
	}
	code, body = h.request(t, "POST", "/search", url.Values{"q": {"x"}, "csrf": {csrf}, "tag": {"-9"}}.Encode(), "application/x-www-form-urlencoded")
	if code != 200 || !strings.Contains(body, "invalid filter") {
		t.Fatal(code, body)
	}
	res, err = h.client.Get(h.origin + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if strings.Contains(res.Header.Get("Content-Security-Policy"), "unsafe-") || !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("unsafe CSP")
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private response cached")
	}
}
func TestDocumentAndHealthRoutes(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		path   string
		status int
		want   string
	}{{"/healthz", 200, "ok"}, {"/readyz", 200, "ready"}, {"/documents/1", 200, "PDF preview"}, {"/documents/1/preview", 200, "%PDF"}, {"/documents/1/download", 200, "%PDF"}, {"/documents/999", 502, "not found"}, {"/documents/-1/preview", 400, "Invalid document"}, {"/static/app.js", 200, "alpine:init"}} {
		t.Run(tc.path, func(t *testing.T) {
			code, body := h.request(t, "GET", tc.path, "", "")
			if code != tc.status || !strings.Contains(body, tc.want) {
				t.Fatal(code, body)
			}
		})
	}
}

type thumbnailAPI struct {
	paperless.API
	data []byte
	err  error
}

func (a thumbnailAPI) Thumbnail(context.Context, int) (*http.Response, error) {
	if a.err != nil {
		return nil, a.err
	}
	return &http.Response{Body: io.NopCloser(bytes.NewReader(a.data))}, nil
}

type boundedBinaryAPI struct {
	paperless.API
	body *bytes.Reader
}

func (a boundedBinaryAPI) Thumbnail(context.Context, int) (*http.Response, error) {
	return &http.Response{Body: io.NopCloser(a.body)}, nil
}

func (a boundedBinaryAPI) Binary(ctx context.Context, id int, _ bool) (*http.Response, error) {
	return a.Thumbnail(ctx, id)
}

func TestDocumentProxySizeLimits(t *testing.T) {
	h := newHarness(t)
	api := h.app.api
	for _, tc := range []struct {
		name, path  string
		size, limit int
		status      int
	}{
		{"thumbnail at limit", "/thumbnail", 2 << 20, 2 << 20, 200},
		{"thumbnail over limit", "/thumbnail", (2 << 20) + 4096, 2 << 20, 502},
		{"preview above thumbnail limit", "/preview", (2 << 20) + 1, 32 << 20, 200},
		{"preview at limit", "/preview", 32 << 20, 32 << 20, 200},
		{"preview over limit", "/preview", (32 << 20) + 4096, 32 << 20, 502},
		{"download at limit", "/download", 32 << 20, 32 << 20, 200},
		{"download over limit", "/download", (32 << 20) + 4096, 32 << 20, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			copy(data, "\x89PNG\r\n\x1a\n")
			body := bytes.NewReader(data)
			h.app.api = boundedBinaryAPI{API: api, body: body}
			req := httptest.NewRequest(http.MethodGet, h.origin+"/documents/1"+tc.path, nil)
			w := httptest.NewRecorder()
			h.app.Handler().ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if tc.status == 200 {
				if w.Body.Len() != tc.size {
					t.Fatal("proxy truncated an allowed response")
				}
			} else if read := tc.size - body.Len(); read != tc.limit+1 {
				t.Fatalf("proxy read %d bytes; should stop at %d", read, tc.limit+1)
			}
		})
	}
}

func TestThumbnailProxy(t *testing.T) {
	h := newHarness(t)
	api := h.app.api
	for _, tc := range []struct {
		name, path, mime string
		data             []byte
		err              error
		status           int
	}{
		{name: "webp", path: "/documents/1/thumbnail", data: []byte("RIFF\x10\x00\x00\x00WEBPVP8 \x04\x00\x00\x00test"), mime: "image/webp", status: 200},
		{name: "png", path: "/documents/1/thumbnail", data: []byte("\x89PNG\r\n\x1a\nsynthetic"), mime: "image/png", status: 200},
		{name: "jpeg", path: "/documents/1/thumbnail", data: []byte("\xff\xd8\xffsynthetic"), mime: "image/jpeg", status: 200},
		{name: "html", path: "/documents/1/thumbnail", data: []byte("<html>not an image</html>"), status: 415},
		{name: "svg", path: "/documents/1/thumbnail", data: []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"), status: 415},
		{name: "pdf", path: "/documents/1/thumbnail", data: demo.PDF(), status: 415},
		{name: "oversized", path: "/documents/1/thumbnail", data: bytes.Repeat([]byte("x"), (2<<20)+1), status: 502},
		{name: "upstream failure", path: "/documents/1/thumbnail", err: errors.New("thumbnail unavailable"), status: 502},
		{name: "invalid ID", path: "/documents/0/thumbnail", status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.app.api = thumbnailAPI{API: api, data: tc.data, err: tc.err}
			req := httptest.NewRequest(http.MethodGet, h.origin+tc.path, nil)
			w := httptest.NewRecorder()
			h.app.Handler().ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.status == 200 && (w.Header().Get("Content-Type") != tc.mime || !bytes.Equal(w.Body.Bytes(), tc.data)) {
				t.Fatal("thumbnail bytes or detected content type changed")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("thumbnail response lost privacy headers")
			}
		})
	}
}

type documentPreviewAPI struct {
	paperless.API
	document paperless.Document
	err      error
}

func (a documentPreviewAPI) Document(context.Context, int) (paperless.Document, error) {
	return a.document, a.err
}

func TestInlineDocumentViewer(t *testing.T) {
	h := newHarness(t)
	api := h.app.api
	archive := "archived.pdf"
	for _, tc := range []struct {
		name, mime, want string
		archive          *string
		err              error
		status           int
	}{
		{name: "pdf", mime: "application/pdf", want: `title="PDF preview"`, status: 200},
		{name: "archived PDF", mime: "image/tiff", archive: &archive, want: `title="PDF preview"`, status: 200},
		{name: "png", mime: "image/png", want: `data-document-image`, status: 200},
		{name: "jpeg", mime: "image/jpeg", want: `data-document-image`, status: 200},
		{name: "unsupported", mime: "text/html", want: "does not have a safe inline preview", status: 200},
		{name: "metadata failure", err: errors.New("document permission denied"), want: "document permission denied", status: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.app.api = documentPreviewAPI{API: api, document: paperless.Document{
				ID: 1, Title: "Test <document>", MIME: tc.mime, ArchivedFilename: tc.archive,
			}, err: tc.err}
			req := httptest.NewRequest(http.MethodGet, h.origin+"/documents/1", nil)
			req.Header.Set("X-Paperlens-Preview", "inline")
			w := httptest.NewRecorder()
			h.app.Handler().ServeHTTP(w, req)
			body := w.Body.String()
			if w.Code != tc.status || !strings.Contains(body, tc.want) {
				t.Fatalf("status %d: %s", w.Code, body)
			}
			if strings.Contains(body, "<html") || strings.Contains(body, "topbar") || strings.Contains(body, "Test <document>") {
				t.Fatal("inline viewer contains page chrome or unescaped metadata")
			}
			if tc.status == 200 {
				full := httptest.NewRecorder()
				req.Header.Del("X-Paperlens-Preview")
				h.app.Handler().ServeHTTP(full, req)
				if !strings.Contains(full.Body.String(), "<html") || !strings.Contains(full.Body.String(), tc.want) {
					t.Fatal("standalone viewer no longer includes the shared media")
				}
			}
		})
	}
}

func TestResultsKeepEvidenceWithoutEagerPreviewLoads(t *testing.T) {
	h := newHarness(t)
	quote := "First line\n\n\n\n<script>untrusted text</script>\n\nFinal line"
	w := httptest.NewRecorder()
	h.app.render(w, "results", page{
		Job: job{State: "done"},
		Results: []search.Result{{
			Document: paperless.Document{ID: 1, Title: "Example", MIME: "application/pdf"},
			Quote:    quote,
		}},
	})
	body := w.Body.String()
	if !strings.Contains(body, "First line\n\n\n\n&lt;script&gt;untrusted text&lt;/script&gt;\n\nFinal line") {
		t.Fatal("source evidence was changed or not escaped")
	}
	if !strings.Contains(body, `src="/documents/1/thumbnail"`) || !strings.Contains(body, `loading="lazy"`) {
		t.Fatal("result thumbnail missing")
	}
	if strings.Contains(body, `src="/documents/1/preview"`) || strings.Contains(body, "<iframe") {
		t.Fatal("results eagerly embed full documents")
	}
}

func (h *harness) upload(t *testing.T, csrf, nonce, title string, data []byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("csrf", csrf)
	w.WriteField("nonce", nonce)
	w.WriteField("title", title)
	part, err := w.CreateFormFile("document", "sample.pdf")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	w.Close()
	return h.request(t, "POST", "/upload", buf.String(), w.FormDataContentType())
}
func TestUploadsReceiptsAndNoBlindRetry(t *testing.T) {
	h := newHarness(t)
	_, body := h.request(t, "GET", "/upload", "", "")
	csrf, nonce := hidden(t, body, "csrf"), hidden(t, body, "nonce")
	code, body := h.upload(t, csrf, nonce, "Example", demo.PDF())
	if code != 200 || !strings.Contains(body, "Accepted") {
		t.Fatal(code, body)
	}
	id := strings.Split(nonce, ".")[0]
	_, body = h.request(t, "GET", "/uploads/"+id, "", "")
	if !strings.Contains(body, "Document stored") {
		t.Fatal(body)
	}
	_, body = h.upload(t, csrf, nonce, "Second", demo.PDF())
	if !strings.Contains(body, "Document stored") {
		t.Fatal("submission retried", body)
	}
	if len(h.app.receipts.items) != 1 {
		t.Fatal("duplicate receipt")
	}
	store, err := openReceipts(h.app.config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 1 {
		t.Fatal("receipt lost after restart")
	}
	_, body = h.request(t, "GET", "/upload", "", "")
	nonce = hidden(t, body, "nonce")
	_, body = h.upload(t, csrf, nonce, "", demo.PDF())
	if !strings.Contains(body, "Accepted") {
		t.Fatal(body)
	}
	_, body = h.request(t, "GET", "/uploads/"+strings.Split(nonce, ".")[0], "", "")
	if !strings.Contains(body, "Duplicate detected") {
		t.Fatal(body)
	}
	_, body = h.request(t, "GET", "/upload", "", "")
	nonce = hidden(t, body, "nonce")
	h.upload(t, csrf, nonce, "simulate failure", demo.PDF())
	_, body = h.request(t, "GET", "/uploads/"+strings.Split(nonce, ".")[0], "", "")
	if !strings.Contains(body, "Ingestion failed") {
		t.Fatal(body)
	}
	_, body = h.upload(t, csrf, nonce, "", []byte("<script>bad</script>"))
	if !strings.Contains(body, "Ingestion failed") {
		t.Fatal("receipt retry was not idempotent", body)
	}
	_, body = h.request(t, "GET", "/upload", "", "")
	nonce = hidden(t, body, "nonce")
	_, body = h.upload(t, csrf, nonce, "", []byte("<script>bad</script>"))
	if !strings.Contains(body, "contents do not match") {
		t.Fatal(body)
	}
	_, body = h.upload(t, csrf, nonce, "", bytes.Repeat([]byte("x"), (1<<20)+1))
	if !strings.Contains(body, "too large") && !strings.Contains(body, "limit") {
		t.Fatal(body)
	}
	if code, _ = h.request(t, "GET", "/uploads/missing", "", ""); code != 404 {
		t.Fatal("missing receipt accepted")
	}
}
func TestReceiptCrashAndStorageBounds(t *testing.T) {
	dir := t.TempDir()
	s, err := openReceipts(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, ok, err := s.reserve("id", "owner")
	if err != nil || !ok {
		t.Fatal(err)
	}
	s, err = openReceipts(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, ok = s.get(r.ID, "owner")
	if !ok || r.State != "unknown" {
		t.Fatal(r)
	}
	if _, ok = s.get(r.ID, "someone"); ok {
		t.Fatal("receipt ownership bypass")
	}
	r.Created = time.Now().Add(-25 * time.Hour)
	s.update(r)
	if _, ok = s.get(r.ID, "owner"); ok {
		t.Fatal("expired receipt visible")
	}
	s, err = openReceipts(dir)
	if err != nil || len(s.items) != 0 {
		t.Fatal("expired receipt not pruned")
	}
	if err = os.WriteFile(filepath.Join(dir, "receipts.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = openReceipts(dir); err == nil {
		t.Fatal("corrupt receipts accepted")
	}
}

type waitingAgent struct{ entered chan struct{} }

func (a waitingAgent) Rank(ctx context.Context, _ string, _ search.Tools) (search.Ranking, error) {
	select {
	case a.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return search.Ranking{}, ctx.Err()
}
func TestJobQueueCancelExpiryAndCleanup(t *testing.T) {
	api := demo.New()
	a := waitingAgent{entered: make(chan struct{}, 2)}
	e := &search.Engine{API: api, Agent: a, Limits: search.Limits{Searches: 6, Candidates: 100, Excerpts: 12, Characters: 8000}}
	j := newJobs(e, 1, 1, time.Second)
	defer j.close()
	first, err := j.start("a", "insurance", paperless.Filters{}, false, paperless.Vocabulary{})
	if err != nil {
		t.Fatal(err)
	}
	<-a.entered
	if _, err = j.start("b", "insurance", paperless.Filters{}, false, paperless.Vocabulary{}); err != nil {
		t.Fatal(err)
	}
	if _, err = j.start("c", "insurance", paperless.Filters{}, false, paperless.Vocabulary{}); err == nil {
		t.Fatal("unbounded queue")
	}
	if _, ok := j.stop(first.ID, "someone"); ok {
		t.Fatal("cross-browser cancellation")
	}
	if _, ok := j.stop(first.ID, "a"); !ok {
		t.Fatal("cancel failed")
	}
	j.mu.Lock()
	j.items[first.ID].Created = time.Now().Add(-11 * time.Minute)
	j.mu.Unlock()
	if _, ok := j.get(first.ID, "a"); ok {
		t.Fatal("expired snapshot retained")
	}
}
