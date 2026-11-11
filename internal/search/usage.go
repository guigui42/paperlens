package search

import (
	"context"
	"fmt"
	"strconv"
	"sync"
)

// Usage is the AI-credit charge reported for one search. It is
// application-owned metadata, never part of the model ranking contract.
type Usage struct {
	// Inference is true once a model request was sent. Without it no AI
	// credits were consumed.
	Inference bool
	// Calls counts distinct model calls; Reported counts calls that carried
	// a billing amount. Missing billing is unknown, not zero.
	Calls, Reported int
	// Partial is set when the runtime itself flagged a call as partial.
	Partial bool
	NanoAIU float64
}

// Credit availability states.
const (
	CreditsNone        = "none"
	CreditsComplete    = "complete"
	CreditsPartial     = "partial"
	CreditsUnavailable = "unavailable"
)

// Status reports whether Credits is a full, partial, or missing amount.
func (u Usage) Status() string {
	switch {
	case !u.Inference:
		return CreditsNone
	case u.Reported == 0:
		return CreditsUnavailable
	case u.Partial || u.Reported < u.Calls:
		return CreditsPartial
	default:
		return CreditsComplete
	}
}

// Credits converts nano AI units to AI credits.
func (u Usage) Credits() float64 { return u.NanoAIU / 1e9 }

// FormatCredits keeps small non-zero charges visible.
func FormatCredits(c float64) string {
	switch {
	case c == 0:
		return "0"
	case c >= 0.01:
		return strconv.FormatFloat(c, 'f', 2, 64)
	case c < 0.000001:
		return "< 0.000001"
	default:
		return strconv.FormatFloat(c, 'g', 2, 64)
	}
}

// Label is the human-readable charge for this search.
func (u Usage) Label() string {
	amount := FormatCredits(u.Credits())
	switch u.Status() {
	case CreditsNone:
		return "0 AI credits (no AI used)"
	case CreditsUnavailable:
		return "AI credits unavailable"
	case CreditsPartial:
		return fmt.Sprintf("at least %s AI credits (partial report)", amount)
	default:
		return amount + " AI credits"
	}
}

// Meter accumulates per-call usage reported while a search runs.
type Meter struct {
	mu    sync.Mutex
	usage Usage
	seen  map[string]bool
}

type meterKey struct{}

// WithMeter attaches m so agents can report usage, including on failure paths.
func WithMeter(ctx context.Context, m *Meter) context.Context {
	return context.WithValue(ctx, meterKey{}, m)
}

// MeterFrom returns the search's meter, or nil when none is attached.
func MeterFrom(ctx context.Context) *Meter {
	m, _ := ctx.Value(meterKey{}).(*Meter)
	return m
}

// Inference marks that a model request was sent.
func (m *Meter) Inference() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage.Inference = true
}

// Record adds one model call. id deduplicates repeated reports of the same
// call; an empty id is always counted. reported is false when the runtime
// omitted billing.
func (m *Meter) Record(id string, reported, partial bool, nanoAIU float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != "" {
		if m.seen == nil {
			m.seen = map[string]bool{}
		}
		if m.seen[id] {
			return
		}
		m.seen[id] = true
	}
	m.usage.Inference = true
	m.usage.Calls++
	if partial {
		m.usage.Partial = true
	}
	if reported {
		m.usage.Reported++
		m.usage.NanoAIU += nanoAIU
	}
}

// Snapshot returns the usage observed so far.
func (m *Meter) Snapshot() Usage {
	if m == nil {
		return Usage{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usage
}
