package resolvers

import (
	"testing"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// strPtr returns a pointer to the given string. Local helper to keep the
// test cases short.
func strPtr(s string) *string { return &s }

// statePtr returns a pointer to the given HostServiceState.
func statePtr(s graphql1.HostServiceState) *graphql1.HostServiceState { return &s }

// fixtureHostService builds a HostService bound to a host with the given
// machine id, name, and state.
func fixtureHostService(host, name string, state graphql1.HostServiceState) *graphql1.HostService {
	return &graphql1.HostService{
		ID:    "HostService:" + host + ":" + name,
		Host:  &graphql1.Host{ID: "Host:" + host, MachineID: host},
		Name:  name,
		State: state,
	}
}

func TestHostServiceMatchesFilter_NilFilter(t *testing.T) {
	svc := fixtureHostService("local", "orchard", graphql1.HostServiceStateActive)
	if !hostServiceMatchesFilter(svc, nil) {
		t.Fatal("nil filter should pass-through")
	}
}

func TestHostServiceMatchesFilter_NilService(t *testing.T) {
	if hostServiceMatchesFilter(nil, nil) {
		t.Fatal("nil service must never match")
	}
}

func TestHostServiceMatchesFilter_HostMatch(t *testing.T) {
	svc := fixtureHostService("local-machine", "orchard", graphql1.HostServiceStateActive)
	if !hostServiceMatchesFilter(svc, &graphql1.HostServiceFilter{Host: strPtr("local-machine")}) {
		t.Fatal("host match must keep the service")
	}
	if hostServiceMatchesFilter(svc, &graphql1.HostServiceFilter{Host: strPtr("other")}) {
		t.Fatal("non-matching host must drop the service")
	}
}

func TestHostServiceMatchesFilter_NameExactMatch(t *testing.T) {
	svc := fixtureHostService("local", "com.gitorchard.orchard", graphql1.HostServiceStateActive)
	if !hostServiceMatchesFilter(svc, &graphql1.HostServiceFilter{Name: strPtr("com.gitorchard.orchard")}) {
		t.Fatal("exact name match must keep")
	}
	if hostServiceMatchesFilter(svc, &graphql1.HostServiceFilter{Name: strPtr("orchard")}) {
		t.Fatal("substring name must NOT match (filter is exact)")
	}
}

func TestHostServiceMatchesFilter_State(t *testing.T) {
	svc := fixtureHostService("local", "orchard", graphql1.HostServiceStateFailed)
	if !hostServiceMatchesFilter(svc, &graphql1.HostServiceFilter{State: statePtr(graphql1.HostServiceStateFailed)}) {
		t.Fatal("matching state must keep")
	}
	if hostServiceMatchesFilter(svc, &graphql1.HostServiceFilter{State: statePtr(graphql1.HostServiceStateActive)}) {
		t.Fatal("non-matching state must drop")
	}
}

func TestHostServiceMatchesFilter_AndCombined(t *testing.T) {
	svc := fixtureHostService("local", "com.gitorchard.orchard", graphql1.HostServiceStateActive)
	all := &graphql1.HostServiceFilter{
		Host:  strPtr("local"),
		Name:  strPtr("com.gitorchard.orchard"),
		State: statePtr(graphql1.HostServiceStateActive),
	}
	if !hostServiceMatchesFilter(svc, all) {
		t.Fatal("all filters matching should keep")
	}
	mismatch := &graphql1.HostServiceFilter{
		Host:  strPtr("local"),
		Name:  strPtr("com.gitorchard.orchard"),
		State: statePtr(graphql1.HostServiceStateInactive),
	}
	if hostServiceMatchesFilter(svc, mismatch) {
		t.Fatal("any non-matching field should drop (AND semantics)")
	}
}

func TestHostServiceMatchesFilter_HostMissingHostRef(t *testing.T) {
	// A HostService without a host ref (defensive — shouldn't happen in
	// practice) does not match a host filter.
	svc := &graphql1.HostService{
		ID:    "HostService:?:orphan",
		Name:  "orphan",
		State: graphql1.HostServiceStateUnknown,
	}
	if hostServiceMatchesFilter(svc, &graphql1.HostServiceFilter{Host: strPtr("local")}) {
		t.Fatal("orphaned service must not satisfy a host filter")
	}
	if !hostServiceMatchesFilter(svc, nil) {
		t.Fatal("orphaned service still matches a nil filter")
	}
}
