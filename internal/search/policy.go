package search

import (
	"errors"
	"strings"

	"github.com/guigui42/paperlens/internal/paperless"
)

// ErrExclusionPolicy prevents AI when required privacy tags cannot be resolved.
var ErrExclusionPolicy = errors.New("AI is unavailable because a required exclusion tag could not be resolved; check PAPERLENS_EXCLUDED_TAGS and Paperless tag access")

// ErrAIDisabled indicates that runtime termination or local cleanup is unverified.
var ErrAIDisabled = errors.New("AI is disabled until restart because runtime shutdown or local cleanup could not be verified")

// ErrAITeardown marks a single failed search after otherwise verified cleanup.
var ErrAITeardown = errors.New("AI session teardown reported an error; this search uses keyword results")

// Sanitized operational failures are safe to show without logging SDK payloads.
var (
	ErrAIRuntime        = errors.New("copilot runtime unavailable; check the pinned CLI installation and writable runtime directory")
	ErrAIAuthentication = errors.New("copilot authentication or model listing failed; check the server credential and entitlement")
	ErrAIModel          = errors.New("the configured Copilot model is unavailable for this identity; check COPILOT_MODEL and model access")
	ErrAISession        = errors.New("copilot could not establish the restricted session policy; check the pinned SDK and CLI pair")
	ErrAIInference      = errors.New("copilot inference failed or timed out; check provider availability and the search deadline")
)

func agentFailureNotice(err error) string {
	for _, safe := range []error{ErrAIDisabled, ErrAITeardown, ErrAIRuntime, ErrAIAuthentication, ErrAIModel, ErrAISession, ErrAIInference} {
		if errors.Is(err, safe) {
			return safe.Error()
		}
	}
	return ""
}

func tagName(name string) string { return strings.Join(strings.Fields(Fold(name)), " ") }

// ValidatePolicy checks every required name, never silently accepting an empty policy.
func (e *Engine) ValidatePolicy(v paperless.Vocabulary) error {
	_, err := e.exclusionIDs(v)
	return err
}

func (e *Engine) exclusionIDs(v paperless.Vocabulary) (map[int]bool, error) {
	names := e.ExcludedTags
	if names == nil {
		names = []string{"Export financier"}
	}
	ids := map[int]bool{}
	valid := len(names) > 0
	for _, name := range names {
		required := tagName(name)
		found := false
		for _, t := range v.Tags {
			if required != "" && t.ID > 0 && tagName(t.Name) == required {
				ids[t.ID] = true
				found = true
			}
		}
		valid = valid && found
	}
	if !valid {
		return ids, ErrExclusionPolicy
	}
	return ids, nil
}
