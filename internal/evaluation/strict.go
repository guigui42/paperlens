package evaluation

import (
	"context"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

func words(s string) []string {
	return strings.FieldsFunc(search.Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// Strict approximates Paperless full-text search, which requires every
// accent-folded query word to appear. It is a simplified proxy, not Whoosh:
// a query word matches any document word it is a prefix of (a stand-in for
// stemming), there are no stopwords or field queries, and result order comes
// from the demo archive's scorer. The demo archive itself scores any matching
// word, which hides recall lost to filler words ("latest") or words the
// document never uses.
type Strict struct{ paperless.API }

// Search keeps only demo results where every query word starts a document word.
func (s Strict) Search(ctx context.Context, q string, f paperless.Filters, limit int) (paperless.Page, error) {
	p, err := s.API.Search(ctx, q, f, 100)
	if err != nil {
		return p, err
	}
	terms := words(q)
	out := paperless.Page{Results: []paperless.Document{}}
	for _, d := range p.Results {
		tokens := words(d.Title + " " + d.Content)
		all := true
		for _, t := range terms {
			if !slices.ContainsFunc(tokens, func(w string) bool { return strings.HasPrefix(w, t) }) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		out.Count++
		if len(out.Results) < limit {
			out.Results = append(out.Results, d)
		}
	}
	if out.Count > len(out.Results) {
		next := "?page=2"
		out.Next = &next
	}
	return out, nil
}

// recorder notes every document ID Paperless returned during one search job,
// including ones the engine later drops (candidate cap, filters, exclusions).
type recorder struct {
	paperless.API
	mu   sync.Mutex
	seen map[int]bool
}

func (r *recorder) Search(ctx context.Context, q string, f paperless.Filters, limit int) (paperless.Page, error) {
	p, err := r.API.Search(ctx, q, f, limit)
	r.mu.Lock()
	for _, d := range p.Results {
		r.seen[d.ID] = true
	}
	r.mu.Unlock()
	return p, err
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.seen = map[int]bool{}
	r.mu.Unlock()
}

func (r *recorder) retrieved(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[id]
}
