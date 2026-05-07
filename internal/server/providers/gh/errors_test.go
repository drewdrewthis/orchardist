package gh

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsNotFound covers the IsNotFound helper added with #436 — the
// resolver layer uses it to translate GitHub 404s into a `null` GraphQL
// response (rather than a per-field error) for `Query.issue` /
// `Query.pullRequest` lookups.
func TestIsNotFound_OnHTTP404(t *testing.T) {
	err := &httpError{Status: 404, Message: "not found", Endpoint: "/repos/x/y/issues/9999"}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound(404 httpError) = false, want true")
	}
}

func TestIsNotFound_OnHTTP500(t *testing.T) {
	err := &httpError{Status: 500, Message: "boom", Endpoint: "/repos/x/y/issues/1"}
	if IsNotFound(err) {
		t.Errorf("IsNotFound(500 httpError) = true, want false (only 404 counts)")
	}
}

func TestIsNotFound_OnWrappedHTTP404(t *testing.T) {
	inner := &httpError{Status: 404}
	wrapped := fmt.Errorf("layer 2: %w", inner)
	if !IsNotFound(wrapped) {
		t.Errorf("IsNotFound on wrapped 404 = false, want true (errors.As must unwrap)")
	}
}

func TestIsNotFound_OnUnrelatedError(t *testing.T) {
	if IsNotFound(errors.New("anything")) {
		t.Errorf("IsNotFound on plain error = true, want false")
	}
}

func TestIsNotFound_OnNil(t *testing.T) {
	if IsNotFound(nil) {
		t.Errorf("IsNotFound(nil) = true, want false")
	}
}
