package paperless

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMetadataProxyLinksKeepConfiguredHTTPS(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.TLS == nil {
			t.Error("metadata request downgraded from HTTPS")
		}
		if r.URL.Path == "/api/tags/" && r.URL.Query().Get("page") != "2" {
			json.NewEncoder(w).Encode(map[string]any{
				"results": []Term{{ID: 1, Name: "First"}},
				"next":    "http://" + r.Host + "/api/tags/?page=2&page_size=100",
			})
			return
		}
		if r.URL.Query().Get("page") == "2" && r.URL.Query().Get("page_size") != "100" {
			t.Error("pagination changed the fixed page size")
		}
		io.WriteString(w, `{"results":[{"id":2,"name":"Second"}],"next":null}`)
	}))
	defer server.Close()
	c, err := New(server.URL, "synthetic-token")
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = server.Client().Transport
	v, err := c.Metadata(context.Background())
	if err != nil || len(v.Tags) != 2 || calls.Load() != 5 {
		t.Fatalf("HTTPS pagination failed: tag_count=%d calls=%d err=%v", len(v.Tags), calls.Load(), err)
	}
}

func TestMetadataPageHintValidation(t *testing.T) {
	c, err := New("https://archive.example", "synthetic-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []string{"?page=2", "/api/tags/?page=2", "https://archive.example/api/tags/?page=2&page_size=100", "http://archive.example/api/tags/?page=2"} {
		t.Run(next, func(t *testing.T) {
			path, err := c.metadataPage(next, "tags", 2)
			if err != nil || path != "/api/tags/?page=2&page_size=100" {
				t.Fatal(path, err)
			}
		})
	}
	for _, next := range []string{
		"https://other.example/api/tags/?page=2",
		"https://user@archive.example/api/tags/?page=2",
		"https://archive.example/api/documents/?page=2",
		"ftp://archive.example/api/tags/?page=2",
		"/api/tags/?page=1", "/api/tags/?page=3",
		"/api/tags/?page=2&page=2", "/api/tags/?page=2&page_size=999",
		"/api/tags/?page=2&name=hidden", "/api/tags/?page=2#fragment",
	} {
		t.Run(next, func(t *testing.T) {
			if _, err := c.metadataPage(next, "tags", 2); err == nil {
				t.Fatal("unsafe page hint accepted")
			}
		})
	}
}
