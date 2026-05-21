package server

// contracts_schema_test.go verifies the v0.8 GraphQL schema shape for
// the Contract domain (L1.9, L1.10, L1.11 from the auto-conversation-contract
// feature spec).
//
//   L1.9: enum ContractStatus has exactly the values SIGNED and CLOSED.
//   L1.10: enum ContractReason has exactly the values DELIVERED and ABANDONED.
//   L1.11: type Contract no longer exposes criteria, openQuestions, reportsTo,
//          parentContractId; ContractFilter no longer exposes parentContractId.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// enumValues fetches the values of a named GraphQL enum via introspection.
func enumValues(t *testing.T, url, enumName string) []string {
	t.Helper()
	q := `{ __type(name: "` + enumName + `") { enumValues { name } } }`
	body := postQuery(t, url, q)

	var resp struct {
		Data struct {
			Type *struct {
				EnumValues []struct {
					Name string `json:"name"`
				} `json:"enumValues"`
			} `json:"__type"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("parse introspect response for %s: %v\nbody: %s", enumName, err, body)
	}
	if resp.Data.Type == nil {
		t.Fatalf("enum %s not found in schema", enumName)
	}
	names := make([]string, 0, len(resp.Data.Type.EnumValues))
	for _, ev := range resp.Data.Type.EnumValues {
		names = append(names, ev.Name)
	}
	return names
}

// typeFields fetches the field names of a named GraphQL type via introspection.
func typeFields(t *testing.T, url, typeName string) []string {
	t.Helper()
	q := `{ __type(name: "` + typeName + `") { fields { name } } }`
	body := postQuery(t, url, q)

	var resp struct {
		Data struct {
			Type *struct {
				Fields []struct {
					Name string `json:"name"`
				} `json:"fields"`
			} `json:"__type"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("parse introspect response for %s: %v\nbody: %s", typeName, err, body)
	}
	if resp.Data.Type == nil {
		t.Fatalf("type %s not found in schema", typeName)
	}
	names := make([]string, 0, len(resp.Data.Type.Fields))
	for _, f := range resp.Data.Type.Fields {
		names = append(names, f.Name)
	}
	return names
}

// inputFields fetches the input-field names of a named GraphQL input type.
func inputFields(t *testing.T, url, typeName string) []string {
	t.Helper()
	q := `{ __type(name: "` + typeName + `") { inputFields { name } } }`
	body := postQuery(t, url, q)

	var resp struct {
		Data struct {
			Type *struct {
				InputFields []struct {
					Name string `json:"name"`
				} `json:"inputFields"`
			} `json:"__type"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("parse introspect response for %s: %v\nbody: %s", typeName, err, body)
	}
	if resp.Data.Type == nil {
		t.Fatalf("input type %s not found in schema", typeName)
	}
	names := make([]string, 0, len(resp.Data.Type.InputFields))
	for _, f := range resp.Data.Type.InputFields {
		names = append(names, f.Name)
	}
	return names
}

// containsStr returns true if haystack contains needle.
func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestContractStatus_ExactlyTwoValues — L1.9
// ContractStatus must expose exactly SIGNED and CLOSED; historical values
// (OPEN, COOLDOWN, WAITING, etc.) must be absent.
func TestContractStatus_ExactlyTwoValues(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "1")

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	values := enumValues(t, srv.URL, "ContractStatus")

	if len(values) != 2 {
		t.Errorf("ContractStatus has %d values, want exactly 2; got: %v", len(values), values)
	}

	want := []string{"SIGNED", "CLOSED"}
	for _, w := range want {
		if !containsStr(values, w) {
			t.Errorf("ContractStatus missing value %q; got: %v", w, values)
		}
	}

	// Historical values must be absent.
	forbidden := []string{
		"OPEN", "DELIVERED_PENDING_VALIDATION", "DELIVERED_PENDING_PARENT_VALIDATION",
		"PENDING_DREW_APPROVAL", "AWAITING_CANCEL_ACK", "WAITING_EXTERNAL",
		"SATISFIED", "CANCELLED", "JUDGE_REJECTED_TERMINAL",
		"COOLDOWN", "WAITING", "JUDGE_RUN", "JUDGE_RUN_FAILED",
	}
	for _, f := range forbidden {
		if containsStr(values, f) {
			t.Errorf("ContractStatus contains removed value %q", f)
		}
	}
}

// TestContractReason_ExactlyTwoValues — L1.10
// ContractReason must expose exactly DELIVERED and ABANDONED.
func TestContractReason_ExactlyTwoValues(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "1")

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	values := enumValues(t, srv.URL, "ContractReason")

	if len(values) != 2 {
		t.Errorf("ContractReason has %d values, want exactly 2; got: %v", len(values), values)
	}

	want := []string{"DELIVERED", "ABANDONED"}
	for _, w := range want {
		if !containsStr(values, w) {
			t.Errorf("ContractReason missing value %q; got: %v", w, values)
		}
	}
}

// TestContract_ClosedReasonFieldPresent — L1.10
// Contract must have a closedReason field.
func TestContract_ClosedReasonFieldPresent(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "1")

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	fields := typeFields(t, srv.URL, "Contract")
	if !containsStr(fields, "closedReason") {
		t.Errorf("Contract missing field closedReason; got fields: %v", fields)
	}
}

// TestContract_RemovedFieldsAbsent — L1.11
// The v0.8 schema break removes criteria, openQuestions, reportsTo, and
// parentContractId from the Contract type. This test asserts they are absent.
func TestContract_RemovedFieldsAbsent(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "1")

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	fields := typeFields(t, srv.URL, "Contract")
	removed := []string{"criteria", "openQuestions", "reportsTo", "parentContractId"}
	for _, f := range removed {
		if containsStr(fields, f) {
			t.Errorf("Contract still has removed field %q (should be absent in v0.8)", f)
		}
	}
}

// TestContractQuestion_TypeAbsent — L1.11
// ContractQuestion is removed from the v0.8 schema entirely.
func TestContractQuestion_TypeAbsent(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "1")

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	// Introspect all types and verify ContractQuestion is not present.
	body := postQuery(t, srv.URL, `{ __schema { types { name } } }`)
	if strings.Contains(body, "ContractQuestion") {
		t.Errorf("ContractQuestion type still appears in schema (should be removed in v0.8); body fragment: %s",
			body[:min(len(body), 500)])
	}
}

// TestContractFilter_NoParentContractId — L1.11
// ContractFilter no longer exposes parentContractId.
func TestContractFilter_NoParentContractId(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "1")

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	fields := inputFields(t, srv.URL, "ContractFilter")
	if containsStr(fields, "parentContractId") {
		t.Errorf("ContractFilter still has parentContractId (should be removed in v0.8)")
	}
}

// min returns the smaller of a and b. Inlined to avoid importing math.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
