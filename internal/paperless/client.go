// Package paperless implements the bounded Paperless 3.3 and 3.4.2/API v10 contract.
package paperless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Document is canonical server metadata. Content is never logged.
type Document struct {
	ID               int          `json:"id"`
	Title            string       `json:"title"`
	Content          string       `json:"content"`
	Created          string       `json:"created"`
	OriginalFilename string       `json:"original_file_name"`
	ArchivedFilename *string      `json:"archived_file_name"`
	Filename         string       `json:"original_filename"`
	Correspondent    *int         `json:"correspondent"`
	DocumentType     *int         `json:"document_type"`
	Tags             []int        `json:"tags"`
	CustomFields     []FieldValue `json:"custom_fields"`
	MIME             string       `json:"mime_type"`
	Hit              *SearchHit   `json:"__search_hit__"`
}

// SearchHit is upstream relevance, retained without trusting its HTML.
type SearchHit struct {
	Score      float64 `json:"score"`
	Rank       int     `json:"rank"`
	Highlights string  `json:"highlights"`
}

// FieldValue holds a custom-field value.
type FieldValue struct {
	Field int `json:"field"`
	Value any `json:"value"`
}

// Term is a metadata vocabulary item.
type Term struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	DataType  string `json:"data_type,omitempty"`
	ExtraData struct {
		Options []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"select_options"`
	} `json:"extra_data,omitempty"`
}

// Vocabulary is a read-only metadata snapshot, never copied from documents.
// Callers must not mutate its slices or nested options.
type Vocabulary struct{ Tags, Correspondents, Types, Fields []Term }

// Filters are explicit immutable UI constraints.
type Filters struct {
	After, Before                   string
	Correspondent, Type, Tag, Field int
	Value                           string
	fieldType                       string
}

// Page contains a bounded search response.
type Page struct {
	Results []Document `json:"results"`
	Count   int        `json:"count"`
	Next    *string    `json:"next"`
}

// Task is the ingestion result, not the HTTP upload result.
type Task struct {
	ID        string `json:"task_id"`
	Status    string `json:"status"`
	Documents []int  `json:"related_document_ids"`
	Result    struct {
		Duplicate int `json:"duplicate_of"`
	} `json:"result_data"`
}

// API is the only archive boundary used by the app.
type API interface {
	Metadata(context.Context) (Vocabulary, error)
	Search(context.Context, string, Filters, int) (Page, error)
	Document(context.Context, int) (Document, error)
	Binary(context.Context, int, bool) (*http.Response, error)
	Upload(context.Context, string, string, io.Reader) (string, error)
	Task(context.Context, string) (Task, error)
	DocumentURL(int) string
}

// Client binds all requests to one configured origin and rejects redirects.
type Client struct {
	base    string
	token   string
	http    *http.Client
	mu      sync.Mutex
	vocab   Vocabulary
	until   time.Time
	refresh *metadataRefresh
}

type metadataRefresh struct {
	done  chan struct{}
	vocab Vocabulary
	err   error
}

// New creates a fixed-origin client.
func New(base, token string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid Paperless origin")
	}
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("paperless redirect rejected") }}}, nil
}

// Probe fails closed when the schema does not expose the expected filter contract.
func (c *Client) Probe(ctx context.Context) error {
	var schema struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]struct {
			Get struct {
				Parameters []struct {
					Name string `json:"name"`
				} `json:"parameters"`
			} `json:"get"`
		} `json:"paths"`
	}
	res, err := c.request(ctx, "GET", "/api/schema/?format=json", nil, "")
	if err != nil {
		return fmt.Errorf("paperless schema probe: %w", err)
	}
	defer res.Body.Close()
	version := res.Header.Get("X-Version")
	if (!strings.HasPrefix(version, "3.3.") && version != "3.4.2") || res.Header.Get("X-Api-Version") != "10" {
		return errors.New("paperless version is not supported (requires 3.3.x or 3.4.2 with API v10)")
	}
	if err := decodeResponse(res, &schema, 12<<20); err != nil {
		return err
	}
	if schema.Info.Version != "6.0.0" && schema.Info.Version != "6.0.0 (10)" {
		return errors.New("paperless OpenAPI schema revision is not supported (expected 6.0.0, optionally qualified with API 10)")
	}
	params := map[string]bool{}
	for _, p := range schema.Paths["/api/documents/"].Get.Parameters {
		params[p.Name] = true
	}
	for _, key := range []string{"query", "created__date__gte", "created__date__lte", "correspondent__id", "document_type__id", "tags__id__all", "custom_field_query"} {
		if !params[key] {
			return fmt.Errorf("paperless schema missing supported filter %s", key)
		}
	}
	_, err = c.Metadata(ctx)
	return err
}
func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	if !strings.HasPrefix(path, "/api/") || strings.Contains(path, "://") {
		return nil, errors.New("invalid API path")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, errors.New("cannot construct Paperless request")
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json; version=10")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("paperless unreachable or request cancelled")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		switch res.StatusCode {
		case 401, 403:
			return nil, errors.New("paperless authentication or document permission denied")
		case 429:
			return nil, errors.New("paperless rate limit reached; try later")
		case 404:
			return nil, errors.New("paperless document or endpoint not found")
		default:
			return nil, fmt.Errorf("paperless returned HTTP %d", res.StatusCode)
		}
	}
	return res, nil
}
func (c *Client) get(ctx context.Context, path string, out any, limit int64) error {
	res, err := c.request(ctx, "GET", path, nil, "")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return decodeResponse(res, out, limit)
}

func decodeResponse(res *http.Response, out any, limit int64) error {
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return errors.New("cannot read Paperless response")
	}
	if int64(len(b)) > limit {
		return errors.New("paperless response exceeds limit")
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errors.New("invalid Paperless JSON response")
	}
	return nil
}

// Metadata caches bounded vocabularies for five minutes.
func (c *Client) Metadata(ctx context.Context) (Vocabulary, error) {
	if err := ctx.Err(); err != nil {
		return Vocabulary{}, err
	}
	c.mu.Lock()
	if time.Now().Before(c.until) {
		v := c.vocab
		c.mu.Unlock()
		return v, nil
	}
	if pending := c.refresh; pending != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return Vocabulary{}, ctx.Err()
		case <-pending.done:
			return pending.vocab, pending.err
		}
	}
	pending := &metadataRefresh{done: make(chan struct{})}
	c.refresh = pending
	c.mu.Unlock()
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	v, err := c.fetchMetadata(refreshCtx)
	cancel()
	c.mu.Lock()
	if err == nil {
		c.vocab = v
		c.until = time.Now().Add(5 * time.Minute)
	}
	pending.vocab, pending.err = v, err
	c.refresh = nil
	close(pending.done)
	c.mu.Unlock()
	return v, err
}

func (c *Client) fetchMetadata(ctx context.Context) (Vocabulary, error) {
	var v Vocabulary
	for _, list := range []struct {
		path string
		dst  *[]Term
	}{{"tags", &v.Tags}, {"correspondents", &v.Correspondents}, {"document_types", &v.Types}, {"custom_fields", &v.Fields}} {
		path := "/api/" + list.path + "/?page_size=100"
		for page := 0; path != ""; page++ {
			if page >= 20 {
				return v, errors.New("paperless metadata exceeds 2000 items per category")
			}
			var data struct {
				Results []Term  `json:"results"`
				Next    *string `json:"next"`
			}
			if err := c.get(ctx, path, &data, 2<<20); err != nil {
				return v, err
			}
			*list.dst = append(*list.dst, data.Results...)
			path = ""
			if data.Next != nil {
				nextPath, err := c.metadataPage(*data.Next, list.path, page+2)
				if err != nil {
					return v, err
				}
				path = nextPath
			}
		}
	}
	return v, nil
}

// Validate checks filters before any upstream query.
func (f *Filters) Validate(v Vocabulary) error {
	for _, d := range []string{f.After, f.Before} {
		if d != "" {
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return errors.New("invalid issue date")
			}
		}
	}
	if f.After != "" && f.Before != "" && f.After > f.Before {
		return errors.New("issue-date range is reversed")
	}
	for _, x := range []struct {
		id    int
		terms []Term
	}{{f.Correspondent, v.Correspondents}, {f.Type, v.Types}, {f.Tag, v.Tags}, {f.Field, v.Fields}} {
		if x.id != 0 && !containsTerm(x.terms, x.id) {
			return errors.New("unknown metadata filter")
		}
	}
	if (f.Field == 0) != (f.Value == "") || len([]rune(f.Value)) > 200 {
		return errors.New("custom field and value must be supplied together (max 200 characters)")
	}
	if f.Field != 0 {
		for _, t := range v.Fields {
			if t.ID != f.Field {
				continue
			}
			if !Filterable(t.DataType) {
				return errors.New("this custom-field type is not supported for exact matching")
			}
			f.fieldType = t.DataType
			if t.DataType == "select" {
				valid := false
				for _, option := range t.ExtraData.Options {
					if option.ID == f.Value {
						valid = true
					}
				}
				if !valid {
					return errors.New("select an existing custom-field option ID")
				}
			}
		}
		if _, err := f.typedValue(); err != nil {
			return err
		}
	}
	return nil
}

// Filterable reports the scalar custom-field kinds supported by the pinned API.
func Filterable(kind string) bool {
	switch kind {
	case "string", "longtext", "url", "date", "boolean", "integer", "float", "select":
		return true
	default:
		return false
	}
}
func (f Filters) typedValue() (any, error) {
	switch f.fieldType {
	case "integer":
		n, err := strconv.ParseInt(f.Value, 10, 64)
		if err != nil {
			return nil, errors.New("custom value must be an integer")
		}
		return n, nil
	case "float":
		n, err := strconv.ParseFloat(f.Value, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, errors.New("custom value must be a finite number")
		}
		return n, nil
	case "boolean":
		if f.Value != "true" && f.Value != "false" {
			return nil, errors.New("custom boolean value must be true or false")
		}
		return f.Value == "true", nil
	case "date":
		if _, err := time.Parse("2006-01-02", f.Value); err != nil {
			return nil, errors.New("custom date must use YYYY-MM-DD")
		}
	}
	return f.Value, nil
}
func containsTerm(ts []Term, id int) bool {
	for _, t := range ts {
		if t.ID == id {
			return true
		}
	}
	return false
}

// Values serializes only approved filters, never model-provided query parameters.
func (f Filters) Values() url.Values {
	q := url.Values{}
	for _, p := range []struct{ k, v string }{{"created__date__gte", f.After}, {"created__date__lte", f.Before}} {
		if p.v != "" {
			q.Set(p.k, p.v)
		}
	}
	for _, p := range []struct {
		k string
		v int
	}{{"correspondent__id", f.Correspondent}, {"document_type__id", f.Type}, {"tags__id__all", f.Tag}} {
		if p.v != 0 {
			q.Set(p.k, strconv.Itoa(p.v))
		}
	}
	if f.Field != 0 {
		value, _ := f.typedValue()
		b, _ := json.Marshal([]any{f.Field, "exact", value})
		q.Set("custom_field_query", string(b))
	}
	return q
}

// Matches independently enforces hard constraints on returned documents.
func (f Filters) Matches(d Document) bool {
	date := d.Created
	if len(date) > 10 {
		date = date[:10]
	}
	if (f.After != "" && date < f.After) || (f.Before != "" && date > f.Before) {
		return false
	}
	if f.Correspondent != 0 && (d.Correspondent == nil || *d.Correspondent != f.Correspondent) {
		return false
	}
	if f.Type != 0 && (d.DocumentType == nil || *d.DocumentType != f.Type) {
		return false
	}
	if f.Tag != 0 {
		found := false
		for _, id := range d.Tags {
			if id == f.Tag {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if f.Field != 0 {
		found := false
		value, err := f.typedValue()
		if err != nil {
			return false
		}
		for _, x := range d.CustomFields {
			if x.Field == f.Field && fmt.Sprint(x.Value) == fmt.Sprint(value) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Search reads at most one bounded page; Next signals truncation rather than exhaustiveness.
func (c *Client) Search(ctx context.Context, query string, filters Filters, limit int) (Page, error) {
	if limit < 1 || limit > 100 || len([]rune(query)) > 500 || strings.TrimSpace(query) == "" {
		return Page{}, errors.New("invalid search bounds")
	}
	q := filters.Values()
	q.Set("query", query)
	q.Set("page_size", strconv.Itoa(limit))
	var p Page
	err := c.get(ctx, "/api/documents/?"+q.Encode(), &p, 16<<20)
	if len(p.Results) > limit {
		return Page{}, errors.New("paperless exceeded requested page limit")
	}
	return p, err
}

// Document returns canonical metadata.
func (c *Client) Document(ctx context.Context, id int) (Document, error) {
	var d Document
	if id < 1 {
		return d, errors.New("invalid document ID")
	}
	err := c.get(ctx, fmt.Sprintf("/api/documents/%d/", id), &d, 16<<20)
	return d, err
}

// DocumentURL is constructed exclusively from a server-side ID.
func (c *Client) DocumentURL(id int) string {
	return fmt.Sprintf("%s/documents/%d/details", c.base, id)
}

// Binary returns a response whose body the caller must close and bound.
func (c *Client) Binary(ctx context.Context, id int, original bool) (*http.Response, error) {
	if id < 1 {
		return nil, errors.New("invalid document ID")
	}
	path := fmt.Sprintf("/api/documents/%d/preview/", id)
	if original {
		path = fmt.Sprintf("/api/documents/%d/download/?original=true", id)
	}
	return c.request(ctx, "GET", path, nil, "")
}

// Upload sends exactly once. An error after sending is ambiguous and must not be retried.
func (c *Client) Upload(ctx context.Context, name, title string, file io.Reader) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("document", name)
	if err != nil {
		return "", err
	}
	if _, err = io.Copy(part, file); err != nil {
		return "", err
	}
	if title != "" {
		if err = w.WriteField("title", title); err != nil {
			return "", err
		}
	}
	if err = w.Close(); err != nil {
		return "", err
	}
	res, err := c.request(ctx, "POST", "/api/documents/post_document/", &body, w.FormDataContentType())
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var id string
	if err = json.NewDecoder(io.LimitReader(res.Body, 1024)).Decode(&id); err != nil || !ValidTaskID(id) {
		return "", errors.New("ambiguous upload response; inspect Paperless before uploading again")
	}
	return id, nil
}

// ValidTaskID validates the UUID shape without permitting path/query injection.
func ValidTaskID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// Task polls only the receipt's accepted UUID.
func (c *Client) Task(ctx context.Context, id string) (Task, error) {
	if !ValidTaskID(id) {
		return Task{}, errors.New("invalid ingestion task")
	}
	var page struct {
		Results []Task `json:"results"`
	}
	if err := c.get(ctx, "/api/tasks/?task_id="+id, &page, 1<<20); err != nil {
		return Task{}, err
	}
	for _, t := range page.Results {
		if t.ID == id {
			return t, nil
		}
	}
	return Task{ID: id, Status: "pending"}, nil
}
