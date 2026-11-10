package paperless

import (
	"errors"
	"net/url"
	"strconv"
)

func (c *Client) metadataPage(next, resource string, page int) (string, error) {
	path := "/api/" + resource + "/"
	current, _ := url.Parse(c.base + path)
	link, err := url.Parse(next)
	if err != nil {
		return "", errors.New("invalid metadata pagination")
	}
	link = current.ResolveReference(link)
	// A reverse proxy can advertise HTTP while the configured origin is HTTPS.
	// Treat this only as a page-number hint; never use its scheme for a request.
	schemeOK := link.Scheme == current.Scheme || (current.Scheme == "https" && link.Scheme == "http")
	if !schemeOK || link.Host != current.Host || link.User != nil || link.Path != path || link.Fragment != "" {
		return "", errors.New("unsafe metadata pagination")
	}
	q, err := url.ParseQuery(link.RawQuery)
	if err != nil || len(q["page"]) != 1 || q.Get("page") != strconv.Itoa(page) {
		return "", errors.New("invalid metadata page progression")
	}
	for key, values := range q {
		if key != "page" && key != "page_size" {
			return "", errors.New("unexpected metadata pagination parameter")
		}
		if len(values) != 1 {
			return "", errors.New("duplicate metadata pagination parameter")
		}
	}
	if size := q.Get("page_size"); size != "" && size != "100" {
		return "", errors.New("metadata pagination changed page size")
	}
	return path + "?" + url.Values{"page": {strconv.Itoa(page)}, "page_size": {"100"}}.Encode(), nil
}
