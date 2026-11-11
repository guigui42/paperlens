// Package web serves server-rendered, browser-scoped Paperlens workflows.
package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/guigui42/paperlens/internal/config"
	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Server owns jobs, templates and restart-safe upload receipts.
type Server struct {
	config      config.Config
	api         paperless.API
	jobs        *jobs
	receipts    *receipts
	key         []byte
	templates   *template.Template
	uploadSlots chan struct{}
}
type page struct {
	Title, CSRF, Query, Error, Nonce string
	Demo, AI, UploadEnabled          bool
	// Keyword pre-checks "Keyword only"; Autorun asks the browser to submit
	// a search restored from the URL; Accepted marks a newly started job.
	Keyword, Autorun, Accepted bool
	Vocab                      paperless.Vocabulary
	Filters                    paperless.Filters
	Job                        job
	Running                    bool
	Results                    []search.Result
	Next, Previous             int
	HasPrevious, HasNext       bool
	Receipt                    receipt
	Receipts                   []receipt
	Document                   paperless.Document
	PDF                        bool
	PaperlessURL               string
}

// New validates persistent storage before accepting requests.
func New(c config.Config, api paperless.API, engine *search.Engine) (*Server, error) {
	key, err := keyFile(c.StateDir)
	if err != nil {
		return nil, err
	}
	receipts, err := openReceipts(c.StateDir)
	if err != nil {
		return nil, err
	}
	t, err := template.New("").Funcs(template.FuncMap{
		"filterable": paperless.Filterable,
		"date": func(s string) string {
			if len(s) > 10 {
				return s[:10]
			}
			return s
		},
		"term": func(id *int, ts []paperless.Term) string {
			if id == nil {
				return ""
			}
			for _, t := range ts {
				if t.ID == *id {
					return t.Name
				}
			}
			return ""
		},
		"docURL": api.DocumentURL,
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{config: c, api: api, key: key, receipts: receipts, templates: t, jobs: newJobs(engine, c.Concurrency, c.Queue, c.Deadline), uploadSlots: make(chan struct{}, 2)}, nil
}

// Close cancels all in-flight searches and waits for cleanup.
func (s *Server) Close() { s.jobs.close() }

// BeginShutdown rejects new work and cancels searches without blocking HTTP shutdown.
func (s *Server) BeginShutdown() { s.jobs.beginShutdown() }

// Handler applies security to every route, including static files.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("POST /search", s.startSearch)
	mux.HandleFunc("GET /jobs/{id}", s.poll)
	mux.HandleFunc("POST /jobs/{id}/cancel", s.cancelSearch)
	mux.HandleFunc("GET /documents/{id}", s.preview)
	mux.HandleFunc("GET /documents/{id}/thumbnail", s.thumbnail)
	mux.HandleFunc("GET /documents/{id}/preview", s.binary)
	mux.HandleFunc("GET /documents/{id}/download", s.binary)
	mux.HandleFunc("GET /upload", s.uploadPage)
	mux.HandleFunc("POST /upload", s.upload)
	mux.HandleFunc("GET /uploads/{id}", s.uploadStatus)
	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	return s.protect(mux)
}
func (s *Server) base(w http.ResponseWriter, r *http.Request) (page, string) {
	owner := s.browser(w, r)
	return page{Title: "Paperlens", CSRF: s.sign("csrf:" + owner), Demo: s.config.Demo, AI: s.config.AI, Keyword: !s.config.AI, UploadEnabled: len(s.config.UploadTypes) > 0}, owner
}
func (s *Server) render(w http.ResponseWriter, name string, p page) {
	var b bytes.Buffer
	if err := s.templates.ExecuteTemplate(&b, name, p); err != nil {
		slog.Error("template rendering failed", "template", name)
		http.Error(w, "Unable to render this page", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(b.Bytes()); err != nil {
		slog.Debug("response disconnected")
	}
}
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	p, _ := s.base(w, r)
	v, err := s.api.Metadata(r.Context())
	policyUnavailable := false
	if err != nil {
		p.Error = err.Error()
	} else {
		p.Vocab = v
		if p.AI && s.jobs.engine.ValidatePolicy(v) != nil {
			policyUnavailable = true
			p.AI = false
			p.Keyword = true
			p.Error = search.ErrExclusionPolicy.Error()
		}
	}
	// A search URL only restores inputs here; the browser reruns it through
	// the CSRF-protected POST so GET never starts billable work.
	if q := r.URL.Query(); q.Has("q") {
		p.Query = strings.TrimSpace(q.Get("q"))
		p.Keyword = parseMode(q.Get("mode"), p.Keyword) || policyUnavailable
		f, ferr := parseFilters(r)
		if ferr == nil && err == nil {
			ferr = f.Validate(v)
		}
		switch {
		case ferr != nil:
			p.Error = "This search link has invalid filters (" + ferr.Error() + "). Adjust them and search again."
		case len([]rune(p.Query)) < 1 || len([]rune(p.Query)) > 500:
			p.Error = "This search link needs a query of 1 to 500 characters."
			p.Filters = f
		default:
			p.Filters = f
			p.Autorun = err == nil && (!policyUnavailable || q.Get("mode") == "keyword")
		}
	}
	s.render(w, "home", p)
}

// parseMode keeps saved searches stable when the server default changes.
func parseMode(mode string, fallback bool) bool {
	switch mode {
	case "keyword":
		return true
	case "ai":
		return false
	}
	return fallback
}

// searchURL is the canonical reloadable form of a search. It never holds
// CSRF tokens, job IDs or results.
func searchURL(query string, f paperless.Filters, keyword bool) string {
	v := url.Values{}
	v.Set("q", query)
	add := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	id := func(n int) string {
		if n < 1 {
			return ""
		}
		return strconv.Itoa(n)
	}
	add("after", f.After)
	add("before", f.Before)
	add("correspondent", id(f.Correspondent))
	add("type", id(f.Type))
	add("tag", id(f.Tag))
	add("field", id(f.Field))
	add("value", f.Value)
	if keyword {
		v.Set("mode", "keyword")
	} else {
		v.Set("mode", "ai")
	}
	return "/?" + v.Encode()
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := s.api.Metadata(ctx); err != nil {
		http.Error(w, "Paperless is not ready", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ready\n"))
}
func parseFilters(r *http.Request) (paperless.Filters, error) {
	f := paperless.Filters{After: r.FormValue("after"), Before: r.FormValue("before"), Value: strings.TrimSpace(r.FormValue("value"))}
	for _, p := range []struct {
		name string
		id   *int
	}{{"correspondent", &f.Correspondent}, {"type", &f.Type}, {"tag", &f.Tag}, {"field", &f.Field}} {
		v := r.FormValue(p.name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return f, errors.New("invalid filter ID")
		}
		*p.id = n
	}
	return f, nil
}
func (s *Server) startSearch(w http.ResponseWriter, r *http.Request) {
	p, owner := s.base(w, r)
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Search request too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !s.csrf(r, owner) {
		http.Error(w, "Invalid CSRF token. Reload the page.", http.StatusForbidden)
		return
	}
	p.Query = strings.TrimSpace(r.FormValue("q"))
	p.Keyword = r.FormValue("mode") == "keyword"
	f, err := parseFilters(r)
	if err == nil {
		p.Vocab, err = s.api.Metadata(r.Context())
	}
	if err == nil {
		err = f.Validate(p.Vocab)
	}
	if err == nil && (len([]rune(p.Query)) < 1 || len([]rune(p.Query)) > 500) {
		err = errors.New("enter a query of 1 to 500 characters")
	}
	if err == nil {
		p.Job, err = s.jobs.start(owner, p.Query, f, p.Keyword, p.Vocab)
	}
	if err != nil {
		p.Error = err.Error()
	} else {
		p.Running = true
		p.Accepted = true
	}
	p.Filters = f
	s.renderSearch(w, r, p)
}
func (s *Server) renderSearch(w http.ResponseWriter, r *http.Request, p page) {
	if r.Header.Get("HX-Request") == "true" {
		s.render(w, "results", p)
	} else {
		s.render(w, "home", p)
	}
}
func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	p, owner := s.base(w, r)
	item, ok := s.jobs.get(r.PathValue("id"), owner)
	if !ok {
		p.Error = "Search expired, was evicted to make room, or belongs to another browser. Start a new search."
		s.renderSearch(w, r, p)
		return
	}
	p.Job = item
	p.Query = item.Query
	p.Filters = item.Filters
	p.Keyword = item.keyword
	p.Running = item.running()
	p.Vocab = item.Output.Vocabulary
	rawPage := r.URL.Query().Get("page")
	n, err := strconv.Atoi(rawPage)
	lastPage := max(0, (len(item.Output.Results)-1)/10)
	if err != nil || n < 0 || n > lastPage {
		if rawPage != "" {
			p.Error = "That results page is unavailable. Showing the first page."
		}
		n = 0
	}
	start := n * 10
	end := start + min(10, len(item.Output.Results)-start)
	p.Results = item.Output.Results[start:end]
	p.HasNext = end < len(item.Output.Results)
	p.HasPrevious = start > 0
	p.Next = n + 1
	p.Previous = n - 1
	s.renderSearch(w, r, p)
}
func (s *Server) cancelSearch(w http.ResponseWriter, r *http.Request) {
	_, owner := s.base(w, r)
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if !s.csrf(r, owner) {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	_, ok := s.jobs.stop(r.PathValue("id"), owner)
	if !ok {
		http.Error(w, "Search not found", 404)
		return
	}
	s.poll(w, r)
}
func documentID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		return 0, errors.New("invalid document ID")
	}
	return id, nil
}
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	p, _ := s.base(w, r)
	id, err := documentID(r)
	if err != nil {
		http.Error(w, "Invalid document", 400)
		return
	}
	p.Document, err = s.api.Document(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	p.PDF = p.Document.MIME == "application/pdf" || (p.Document.ArchivedFilename != nil && *p.Document.ArchivedFilename != "")
	p.PaperlessURL = s.api.DocumentURL(id)
	p.Title = p.Document.Title
	if r.Header.Get("X-Paperlens-Preview") == "inline" {
		s.render(w, "document-viewer", p)
		return
	}
	s.render(w, "preview", p)
}

func readDocumentBytes(body io.Reader, maxBinary int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(body, maxBinary+1))
	if err != nil || int64(len(b)) > maxBinary {
		return nil, fmt.Errorf("document exceeds the %d MiB proxy limit or could not be read; open it in Paperless", maxBinary>>20)
	}
	return b, nil
}

func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) {
	id, err := documentID(r)
	if err != nil {
		http.Error(w, "Invalid document", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.api.Thumbnail(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	b, err := readDocumentBytes(res.Body, 2<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	typ := http.DetectContentType(b)
	switch typ {
	case "image/webp", "image/png", "image/jpeg":
	default:
		http.Error(w, "Thumbnail is not a supported image. Open the document to preview it.", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}

func (s *Server) binary(w http.ResponseWriter, r *http.Request) {
	id, err := documentID(r)
	if err != nil {
		http.Error(w, "Invalid document", 400)
		return
	}
	original := strings.HasSuffix(r.URL.Path, "/download")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	d, err := s.api.Document(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	res, err := s.api.Binary(ctx, id, original)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	b, err := readDocumentBytes(res.Body, 32<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	typ := http.DetectContentType(b)
	if original {
		name := d.OriginalFilename
		if name == "" {
			name = d.Filename
		}
		name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
		if name == "." || name == "" {
			name = fmt.Sprintf("document-%d", id)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	} else {
		switch typ {
		case "application/pdf", "image/png", "image/jpeg":
		default:
			http.Error(w, "This format cannot be previewed safely. Download the original instead.", http.StatusUnsupportedMediaType)
			return
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Content-Disposition", "inline")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}
func (s *Server) uploadPage(w http.ResponseWriter, r *http.Request) {
	p, owner := s.base(w, r)
	s.prepareUploadPage(&p, owner)
	s.render(w, "upload", p)
}
func (s *Server) prepareUploadPage(p *page, owner string) {
	id, issued := randomID(), strconv.FormatInt(time.Now().Unix(), 10)
	p.Nonce = id + "." + issued + "." + s.sign("upload:"+owner+":"+id+":"+issued)
	p.Receipts = s.receipts.list(owner)
	sort.Slice(p.Receipts, func(i, j int) bool { return p.Receipts[i].Created.After(p.Receipts[j].Created) })
}
func (s *Server) renderUploadResult(w http.ResponseWriter, r *http.Request, p page) {
	if r.Header.Get("HX-Request") == "true" {
		s.render(w, "upload-result", p)
		return
	}
	if p.Receipt.ID != "" {
		http.Redirect(w, r, "/uploads/"+p.Receipt.ID, http.StatusSeeOther)
		return
	}
	s.render(w, "upload", p)
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	p, owner := s.base(w, r)
	if r.Header.Get("HX-Request") != "true" {
		s.prepareUploadPage(&p, owner)
	}
	fail := func(message string) { p.Error = message; s.renderUploadResult(w, r, p) }
	if len(s.config.UploadTypes) == 0 {
		fail("Uploads are disabled until the administrator confirms supported file formats.")
		return
	}
	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	default:
		fail("Uploads are busy. Try again later.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.config.MaxUpload+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
		fail("Upload is invalid or exceeds the file-size limit.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	if !s.csrf(r, owner) {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	nonce := strings.Split(r.FormValue("nonce"), ".")
	validNonce := len(nonce) == 3
	if validNonce {
		issued, err := strconv.ParseInt(nonce[1], 10, 64)
		age := time.Since(time.Unix(issued, 0))
		validNonce = err == nil && age >= 0 && age < 24*time.Hour && len(nonce[0]) == 26 && hmac.Equal([]byte(s.sign("upload:"+owner+":"+nonce[0]+":"+nonce[1])), []byte(nonce[2]))
	}
	if !validNonce {
		http.Error(w, "Invalid upload receipt; reload the page", 400)
		return
	}
	if old, ok := s.receipts.get(nonce[0], owner); ok {
		p.Receipt = old
		s.renderUploadResult(w, r, p)
		return
	}
	file, header, err := r.FormFile("document")
	if err != nil {
		fail("Choose one file.")
		return
	}
	defer file.Close()
	if len(r.MultipartForm.File["document"]) != 1 {
		fail("Upload one document at a time.")
		return
	}
	name := header.Filename
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if len(name) > 180 || name == "" || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		fail("Invalid filename.")
		return
	}
	allowed := false
	for _, typ := range s.config.UploadTypes {
		if ext == typ {
			allowed = true
		}
	}
	if !allowed {
		fail("File extension is not enabled for this Paperless instance.")
		return
	}
	b, err := io.ReadAll(io.LimitReader(file, s.config.MaxUpload+1))
	if err != nil || len(b) == 0 || int64(len(b)) > s.config.MaxUpload {
		fail("File is empty, unreadable, or too large.")
		return
	}
	typ := http.DetectContentType(b)
	if (ext == "tif" || ext == "tiff") && (bytes.HasPrefix(b, []byte{'I', 'I', 42, 0}) || bytes.HasPrefix(b, []byte{'M', 'M', 0, 42})) {
		typ = "image/tiff"
	}
	expected := map[string]string{"pdf": "application/pdf", "png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "tif": "image/tiff", "tiff": "image/tiff"}[ext]
	if typ != expected {
		fail("The file contents do not match its extension.")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if len([]rune(title)) > 200 {
		fail("Title must be at most 200 characters.")
		return
	}
	rec, fresh, err := s.receipts.reserve(nonce[0], owner)
	if err != nil {
		fail("Could not save an upload receipt. No file was sent.")
		return
	}
	if !fresh {
		p.Receipt = rec
		s.renderUploadResult(w, r, p)
		return
	}
	task, sendErr := s.api.Upload(r.Context(), name, title, bytes.NewReader(b))
	if sendErr != nil {
		rec.State = "unknown"
	} else {
		rec.State = "processing"
		rec.Task = task
	}
	if err = s.receipts.update(rec); err != nil {
		p.Receipt = rec
		fail("Upload may have been accepted, but its receipt could not be saved. Check Paperless before uploading again.")
		return
	}
	p.Receipt = rec
	s.renderUploadResult(w, r, p)
}
func (s *Server) uploadStatus(w http.ResponseWriter, r *http.Request) {
	p, owner := s.base(w, r)
	rec, ok := s.receipts.get(r.PathValue("id"), owner)
	if !ok {
		http.Error(w, "Receipt expired or belongs to another browser", 404)
		return
	}
	if rec.State == "processing" {
		task, err := s.api.Task(r.Context(), rec.Task)
		if err != nil {
			p.Error = "Cannot check ingestion now. The upload is not being retried."
		} else {
			switch task.Status {
			case "success":
				if task.Result.Duplicate > 0 {
					rec.State = "duplicate"
				} else if len(task.Documents) > 0 && task.Documents[0] > 0 {
					rec.State = "consumed"
					rec.Document = task.Documents[0]
				} else {
					rec.State = "unknown"
				}
			case "failure", "revoked":
				if task.Result.Duplicate > 0 {
					rec.State = "duplicate"
				} else {
					rec.State = "failed"
				}
			case "pending", "started":
			default:
				rec.State = "unknown"
			}
			if err = s.receipts.update(rec); err != nil {
				p.Error = "Could not persist ingestion status. Check Paperless before uploading again."
			}
		}
	}
	p.Receipt = rec
	if r.Header.Get("HX-Request") == "true" {
		s.render(w, "upload-result", p)
	} else {
		s.render(w, "receipt", p)
	}
}
