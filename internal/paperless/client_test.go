package paperless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token synthetic-token" {
			t.Error("missing server-only auth")
		}
		if r.Header.Get("Accept") != "application/json; version=10" {
			t.Error("wrong API version")
		}
		w.Header().Set("X-Version", "3.3.0")
		w.Header().Set("X-Api-Version", "10")
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "synthetic-token")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestSearchContract(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/documents/" {
			t.Fatal("unexpected request")
		}
		q := r.URL.Query()
		if q.Get("query") != "facture électricité" || q.Get("page_size") != "10" || q.Get("created__date__gte") != "2025-01-01" || q.Get("tags__id__all") != "7" || q.Get("custom_field_query") != `[8,"exact","Alex"]` {
			t.Errorf("wrong query: %v", q)
		}
		io.WriteString(w, `{"count":2,"next":"http://invalid.example/api/documents/?page=2","results":[{"id":1,"title":"Invoice","content":"<script>alert(1)</script>","created":"2025-01-02","tags":[7],"custom_fields":[{"field":8,"value":"Alex"}],"__search_hit__":{"score":23,"rank":0,"highlights":"<b>Invoice</b>"}}]}`)
	})
	p, err := c.Search(context.Background(), "facture électricité", Filters{After: "2025-01-01", Tag: 7, Field: 8, Value: "Alex"}, 10)
	if err != nil || len(p.Results) != 1 || p.Results[0].Hit.Score != 23 {
		t.Fatalf("%+v %v", p, err)
	}
	// Search pages are deliberately bounded; an untrusted Next is never followed.
	for _, query := range []string{"", strings.Repeat("x", 501)} {
		if _, err = c.Search(context.Background(), query, Filters{}, 10); err == nil {
			t.Error("accepted invalid query")
		}
	}
}
func TestErrorsAndRedirects(t *testing.T) {
	for _, code := range []int{401, 403, 404, 429, 500, 302} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://invalid.example")
				w.WriteHeader(code)
				io.WriteString(w, "PRIVATE upstream error")
			})
			_, err := c.Document(context.Background(), 1)
			if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("unsafe or missing error")
			}
		})
	}
	for _, body := range []string{"not json", strings.Repeat("x", 1025)} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			var out any
			if err := c.get(context.Background(), "/api/documents/", &out, 1024); err == nil {
				t.Error("accepted bad response")
			}
		})
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled request reached server") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Document(ctx, 1); err == nil {
		t.Error("cancel ignored")
	}
	if _, err := c.Document(context.Background(), 0); err == nil {
		t.Error("invalid ID accepted")
	}
	if _, err := c.request(context.Background(), "GET", "https://invalid.example", nil, ""); err == nil {
		t.Error("external URL accepted")
	}
}
func TestMetadataPaginationCacheAndOrigin(t *testing.T) {
	var count atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if strings.Contains(r.URL.Path, "tags") && r.URL.Query().Get("page") != "2" {
			io.WriteString(w, `{"results":[{"id":1,"name":"Home"}],"next":"/api/tags/?page=2"}`)
			return
		}
		io.WriteString(w, `{"results":[{"id":2,"name":"Second"}],"next":null}`)
	})
	for range 2 {
		v, err := c.Metadata(context.Background())
		if err != nil || len(v.Tags) != 2 {
			t.Fatalf("%+v %v", v, err)
		}
	}
	if count.Load() != 5 {
		t.Fatalf("metadata not cached: %d", count.Load())
	}
	for _, next := range []string{"https://invalid.example/api/tags/", "/api/documents/", "//invalid.example/api/tags/"} {
		t.Run(next, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"results": []any{}, "next": next})
			})
			if _, err := c.Metadata(context.Background()); err == nil {
				t.Fatal("unsafe pagination accepted")
			}
		})
	}
}
func TestFilters(t *testing.T) {
	v := Vocabulary{Tags: []Term{{ID: 7}}, Types: []Term{{ID: 2}}, Correspondents: []Term{{ID: 3}}, Fields: []Term{{ID: 8, DataType: "string"}}}
	typ, corr := 2, 3
	d := Document{ID: 1, Created: "2025-04-02", Tags: []int{7}, DocumentType: &typ, Correspondent: &corr, CustomFields: []FieldValue{{Field: 8, Value: "Alex"}}}
	f := Filters{After: "2025-01-01", Before: "2025-12-31", Tag: 7, Type: 2, Correspondent: 3, Field: 8, Value: "Alex"}
	if err := f.Validate(v); err != nil || !f.Matches(d) {
		t.Fatal(err)
	}
	for _, bad := range []Filters{{After: "bad"}, {After: "2026-01-01", Before: "2025-01-01"}, {Tag: 999}, {Field: 8}, {Value: "Alex"}, {Field: 8, Value: strings.Repeat("x", 201)}} {
		if bad.Validate(v) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	for _, bad := range []Filters{{After: "2026-01-01"}, {Before: "2020-01-01"}, {Tag: 99}, {Type: 99}, {Correspondent: 99}, {Field: 8, Value: "Other"}} {
		if bad.Matches(d) {
			t.Errorf("relaxed %+v", bad)
		}
	}
	if got := f.Values(); got.Get("correspondent__id") != "3" || got.Get("document_type__id") != "2" {
		t.Fatal(got)
	}
}
func TestUploadTasksAndBinary(t *testing.T) {
	const taskID = "01234567-0000-4000-8000-012345678901"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/post_document/":
			if r.Method != "POST" {
				t.Error("wrong write method")
			}
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			f, h, err := r.FormFile("document")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if string(b) != "synthetic" || h.Filename != "test.pdf" || r.FormValue("title") != "Test" {
				t.Error("wrong multipart")
			}
			fmt.Fprintf(w, "%q", taskID)
		case "/api/tasks/":
			if r.URL.Query().Get("task_id") != taskID {
				t.Error("wrong task")
			}
			fmt.Fprintf(w, `{"count":1,"results":[{"task_id":%q,"status":"success","result_data":{},"related_document_ids":[42]}]}`, taskID)
		case "/api/documents/42/download/":
			if r.URL.Query().Get("original") != "true" {
				t.Error("not original")
			}
			io.WriteString(w, "synthetic binary")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	id, err := c.Upload(context.Background(), "test.pdf", "Test", strings.NewReader("synthetic"))
	if err != nil || id != taskID {
		t.Fatal(id, err)
	}
	task, err := c.Task(context.Background(), id)
	if err != nil || task.Status != "success" || task.Documents[0] != 42 {
		t.Fatal(task, err)
	}
	res, err := c.Binary(context.Background(), 42, true)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if string(b) != "synthetic binary" {
		t.Fatal(string(b))
	}
	if _, err = c.Task(context.Background(), "../"); err == nil {
		t.Fatal("accepted invalid task")
	}
	if _, err = c.Binary(context.Background(), -1, false); err == nil {
		t.Fatal("accepted invalid ID")
	}
	if !strings.HasSuffix(c.DocumentURL(42), "/documents/42/details") {
		t.Fatal("invalid canonical link")
	}
}
func TestThumbnail(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/api/documents/42/thumb/" {
			t.Errorf("unexpected thumbnail request: %s %s", r.Method, r.URL.RequestURI())
		}
		w.Header().Set("Content-Type", "image/webp")
		io.WriteString(w, "synthetic thumbnail")
	})
	res, err := c.Thumbnail(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil || string(b) != "synthetic thumbnail" {
		t.Fatalf("thumbnail body: %q, error: %v", b, err)
	}
	for _, id := range []int{0, -1} {
		if _, err := c.Thumbnail(context.Background(), id); err == nil {
			t.Fatalf("accepted invalid document ID %d", id)
		}
	}
}

func TestProbeSchema(t *testing.T) {
	for _, tc := range []struct {
		version, schema, api     string
		missingFilter, supported bool
	}{
		{"3.3.0", "6.0.0", "10", false, true},
		{"3.3.0", "6.0.0 (10)", "10", false, true},
		{"3.4.2", "6.0.0 (10)", "10", false, true},
		{"3.4.2", "6.0.0", "10", false, true},
		{"3.4.2", "6.0.0 (9)", "10", false, false},
		{"3.4.2", "6.0.0 (10)", "9", false, false},
		{"3.4.2", "6.0.0 (10)", "10", true, false},
		{"3.4.1", "6.0.0 (10)", "10", false, false},
		{"3.5.0", "6.0.0 (10)", "10", false, false},
		{"3.2.0", "6.0.0", "10", false, false},
	} {
		t.Run(fmt.Sprintf("%s/%s/%s/missing=%t", tc.version, tc.schema, tc.api, tc.missingFilter), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/schema/" {
					io.WriteString(w, `{"results":[],"next":null}`)
					return
				}
				params := []map[string]string{}
				for _, p := range []string{"query", "created__date__gte", "created__date__lte", "correspondent__id", "document_type__id", "tags__id__all", "custom_field_query"} {
					if tc.missingFilter && p == "custom_field_query" {
						continue
					}
					params = append(params, map[string]string{"name": p})
				}
				w.Header().Set("X-Version", tc.version)
				w.Header().Set("X-Api-Version", tc.api)
				json.NewEncoder(w).Encode(map[string]any{"info": map[string]string{"version": tc.schema}, "paths": map[string]any{"/api/documents/": map[string]any{"get": map[string]any{"parameters": params}}}})
			})
			if err := c.Probe(context.Background()); (err == nil) != tc.supported {
				t.Fatal(err)
			}
		})
	}
}
func TestOriginValidation(t *testing.T) {
	for _, raw := range []string{"file:///tmp/x", "http://u:p@localhost", "http://localhost/path", "http://localhost?x=1", "http://localhost#x"} {
		if _, err := New(raw, ""); err == nil {
			t.Fatal(raw)
		}

	}
	f := Filters{Field: 4, Value: `x"],["OR",["any","exists",true]]`}
	q := f.Values()
	u, err := url.ParseQuery(q.Encode())
	if err != nil || u.Get("custom_field_query") != q.Get("custom_field_query") {
		t.Fatal("filter serialization failed")
	}
}

func TestTypedCustomFilters(t *testing.T) {
	for _, tc := range []struct {
		kind, value, want string
		actual            any
	}{
		{"integer", "0042", `[1,"exact",42]`, float64(42)},
		{"float", "4.25", `[1,"exact",4.25]`, 4.25},
		{"boolean", "true", `[1,"exact",true]`, true},
		{"date", "2025-01-02", `[1,"exact","2025-01-02"]`, "2025-01-02"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			f := Filters{Field: 1, Value: tc.value}
			if err := f.Validate(Vocabulary{Fields: []Term{{ID: 1, DataType: tc.kind}}}); err != nil {
				t.Fatal(err)
			}
			if f.Values().Get("custom_field_query") != tc.want || !f.Matches(Document{CustomFields: []FieldValue{{Field: 1, Value: tc.actual}}}) {
				t.Fatal("typed filter mismatch")
			}
		})
	}
	for _, tc := range []struct{ kind, value string }{{"integer", "x"}, {"float", "NaN"}, {"boolean", "yes"}, {"date", "bad"}, {"select", "missing"}, {"documentlink", "1"}} {
		f := Filters{Field: 1, Value: tc.value}
		if err := f.Validate(Vocabulary{Fields: []Term{{ID: 1, DataType: tc.kind}}}); err == nil {
			t.Fatal("invalid custom field accepted", tc)
		}
	}
}
