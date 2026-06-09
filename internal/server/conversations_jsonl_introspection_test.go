// Tests for AC9 — schema doc-text contract on Conversation.jsonlPath.
//
// AC9: The field description must mention the sibling HTTP endpoint so
// clients can discover the URL from `__schema` introspection alone,
// without needing the daemon source or separate documentation.
//
// Two layers of defence:
//   - TestConversationJsonlPath_DocMentionsHTTPEndpoint: the load-bearing one.
//     Runs a real __type introspection against the live GraphQL handler and
//     asserts the description contains the two required substrings.
//   - TestSchemaGraphQL_JsonlPathDocText: source-of-truth regression catch.
//     Reads schema.graphql at the repo root and asserts the same substrings
//     appear in the file. If gqlgen ever regenerates without the description,
//     the introspection test above catches the runtime drift; this test
//     catches the source drift before codegen runs.
package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/drewdrewthis/orchardist/internal/server/resolvers"
)

// TestConversationJsonlPath_DocMentionsHTTPEndpoint asserts that the
// Conversation.jsonlPath field description surfaced via __schema introspection
// contains:
//  1. the path pattern identifying the sibling HTTP endpoint, and
//  2. a reference to the same listener as /graphql.
//
// This is the load-bearing test for AC9: it validates what clients actually
// see when they introspect the running daemon schema.
func TestConversationJsonlPath_DocMentionsHTTPEndpoint(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "") // default ON

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	// Fetch the description of Conversation.jsonlPath via __type introspection.
	query := `{
		__type(name: "Conversation") {
			fields {
				name
				description
			}
		}
	}`
	raw := postQuery(t, srv.URL, query)

	// Parse the envelope to extract the field description.
	var resp struct {
		Data struct {
			Type struct {
				Fields []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"fields"`
			} `json:"__type"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("failed to parse introspection response: %v\nraw: %s", err, raw)
	}

	var desc string
	for _, f := range resp.Data.Type.Fields {
		if f.Name == "jsonlPath" {
			desc = f.Description
			break
		}
	}

	if desc == "" {
		t.Fatalf("jsonlPath field not found in Conversation type or has empty description; raw response: %s", raw)
	}

	// AC9 assertion 1: description names the endpoint path pattern.
	if !strings.Contains(desc, "/v1/conversations/") {
		t.Errorf("jsonlPath description must mention the HTTP endpoint path pattern '/v1/conversations/';\ngot description: %q", desc)
	}

	// AC9 assertion 2: description names /graphql so clients know which
	// endpoint the new route co-locates with.
	if !strings.Contains(desc, "/graphql") {
		t.Errorf("jsonlPath description must mention /graphql;\ngot description: %q", desc)
	}

	// AC9 assertion 3: description states the co-location explicitly. Both
	// signals must be present — naming /graphql alone could just be a "see
	// also" reference, and "same listener" alone leaves the named endpoint
	// implicit. AC9 requires clients to derive the full contract from
	// __schema introspection.
	if !strings.Contains(desc, "same listener") {
		t.Errorf("jsonlPath description must state co-location on the same listener;\ngot description: %q", desc)
	}
}

// TestSchemaGraphQL_JsonlPathDocText is a source-of-truth regression catch.
// It reads the canonical schema.graphql at the repo root and asserts that the
// jsonlPath field declaration carries a doc comment that mentions both the HTTP
// endpoint path pattern and the /graphql listener co-location.
//
// If the runtime introspection test (TestConversationJsonlPath_DocMentionsHTTPEndpoint)
// passes but this test fails, it means schema.graphql was edited without the
// required doc text, and the next `make generate` run will break the runtime
// contract.
func TestSchemaGraphQL_JsonlPathDocText(t *testing.T) {
	// schema.graphql lives at the repo root; this test is in internal/server/.
	// Skip only when the file is genuinely absent (e.g. tests running outside
	// the checked-out repo). Any other read error fails fast — silent skip on
	// a permission error or transient I/O failure would let AC9's source-of-
	// truth guardrail rot undetected in CI.
	raw, err := os.ReadFile("../../schema.graphql")
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("repo-root schema.graphql not present (%v) — skipping source-of-truth check", err)
		}
		t.Fatalf("read repo-root schema.graphql: %v", err)
	}
	schema := string(raw)

	// Find the jsonlPath field declaration.
	idx := strings.Index(schema, "jsonlPath: String!")
	if idx < 0 {
		t.Fatal("jsonlPath: String! not found in schema.graphql")
	}

	// Walk backwards from the field declaration to find the preceding doc block.
	// Doc blocks are triple-quoted strings immediately preceding the field.
	// We search for the nearest """ before the field declaration.
	before := schema[:idx]
	docEnd := strings.LastIndex(before, `"""`)
	if docEnd < 0 {
		t.Fatal("no triple-quoted doc block found before jsonlPath in schema.graphql")
	}
	docStart := strings.LastIndex(before[:docEnd], `"""`)
	if docStart < 0 {
		t.Fatal("could not find opening triple-quote of jsonlPath doc block in schema.graphql")
	}
	docBlock := schema[docStart : docEnd+3]

	// AC9 assertion 1: doc block names the HTTP endpoint path pattern.
	if !strings.Contains(docBlock, "/v1/conversations/") {
		t.Errorf("schema.graphql jsonlPath doc block must mention '/v1/conversations/';\ngot block: %q", docBlock)
	}

	// AC9 assertion 2: doc block names /graphql so the runtime introspection
	// test sees the same signal in both places.
	if !strings.Contains(docBlock, "/graphql") {
		t.Errorf("schema.graphql jsonlPath doc block must mention /graphql;\ngot block: %q", docBlock)
	}

	// AC9 assertion 3: doc block states co-location explicitly. Both signals
	// must be present, mirroring the introspection assertion above.
	if !strings.Contains(docBlock, "same listener") {
		t.Errorf("schema.graphql jsonlPath doc block must state co-location on the same listener;\ngot block: %q", docBlock)
	}
}
