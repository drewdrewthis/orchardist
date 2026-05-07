// Tests for prettyGraphQLErrors — the helper that turns a GraphQL
// validation-error envelope into operator-friendly text. Resolves #398.

package query

import (
	"strings"
	"testing"
)

func TestPrettyGraphQLErrors_SingleErrorWithLocation(t *testing.T) {
	body := []byte(`{"errors":[{"message":"Cannot query field \"services\" on type \"Host\". Did you mean \"hostServices\"?","locations":[{"line":1,"column":16}]}],"data":null}`)
	got := prettyGraphQLErrors(body)
	wantSubstrs := []string{
		`Cannot query field "services" on type "Host"`,
		`Did you mean "hostServices"?`,
		"at line 1, col 16",
	}
	for _, want := range wantSubstrs {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestPrettyGraphQLErrors_MultipleErrorsAreSeparated(t *testing.T) {
	body := []byte(`{"errors":[
		{"message":"first error","locations":[{"line":1,"column":1}]},
		{"message":"second error","locations":[{"line":2,"column":5}]}
	]}`)
	got := prettyGraphQLErrors(body)
	if !strings.Contains(got, "first error") {
		t.Errorf("missing first error: %s", got)
	}
	if !strings.Contains(got, "second error") {
		t.Errorf("missing second error: %s", got)
	}
	if !strings.Contains(got, "line 2, col 5") {
		t.Errorf("missing second location: %s", got)
	}
}

func TestPrettyGraphQLErrors_NoLocations(t *testing.T) {
	body := []byte(`{"errors":[{"message":"server is angry"}]}`)
	got := prettyGraphQLErrors(body)
	if !strings.HasPrefix(got, "error: server is angry") {
		t.Errorf("got: %q", got)
	}
	if strings.Contains(got, "at line") {
		t.Errorf("should not synthesise a fake location: %s", got)
	}
}

func TestPrettyGraphQLErrors_PathRendered(t *testing.T) {
	body := []byte(`{"errors":[{"message":"resolver panic","path":["pullRequests",2,"reviews"]}]}`)
	got := prettyGraphQLErrors(body)
	if !strings.Contains(got, "path: pullRequests.2.reviews") {
		t.Errorf("path not rendered: %s", got)
	}
}

func TestPrettyGraphQLErrors_EmptyErrorsArrayReturnsEmpty(t *testing.T) {
	body := []byte(`{"errors":[],"data":{"foo":"bar"}}`)
	if got := prettyGraphQLErrors(body); got != "" {
		t.Errorf("expected empty string for empty errors[], got: %q", got)
	}
}

func TestPrettyGraphQLErrors_NonJsonReturnsEmpty(t *testing.T) {
	body := []byte("not json at all")
	if got := prettyGraphQLErrors(body); got != "" {
		t.Errorf("expected empty for non-JSON body, got: %q", got)
	}
}

func TestPrettyGraphQLErrors_NoErrorsFieldReturnsEmpty(t *testing.T) {
	// Healthy response — no `errors` key. Helper must return "" so the
	// caller knows there's nothing to pretty-print.
	body := []byte(`{"data":{"health":{"status":"ok"}}}`)
	if got := prettyGraphQLErrors(body); got != "" {
		t.Errorf("expected empty for error-less body, got: %q", got)
	}
}
