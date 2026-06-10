package git

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// gitSchemaPath returns the absolute path to daemon/git/schema.graphql.
func gitSchemaPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "schema.graphql"))
}

// readGitSchema returns the contents of daemon/git/schema.graphql.
func readGitSchema(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(gitSchemaPath(t))
	if err != nil {
		t.Fatalf("read daemon/git/schema.graphql: %v", err)
	}
	return string(data)
}

// TestSchemaDeclaresWorktreeRemoveMutation verifies that daemon/git/schema.graphql
// contains an "extend type Mutation" block with a "worktreeRemove" field (AC1, M1/S15a).
// This is the SDL partial that feeds the schema_combined.graphql SDL surface.
func TestSchemaDeclaresWorktreeRemoveMutation(t *testing.T) {
	schema := readGitSchema(t)
	if !strings.Contains(schema, "extend type Mutation") {
		t.Error("daemon/git/schema.graphql: missing \"extend type Mutation\" block")
	}
	if !strings.Contains(schema, "worktreeRemove") {
		t.Error("daemon/git/schema.graphql: missing \"worktreeRemove\" field in extend type Mutation block")
	}
}

// TestSchemaIdempotencyLiteral verifies that the worktreeRemove field's doc-string
// carries the exact literal "Idempotency: idempotent" (AC10, scenario 3).
// This mirrors the literal already in daemon/git/mutations.go.
func TestSchemaIdempotencyLiteral(t *testing.T) {
	schema := readGitSchema(t)
	const want = "Idempotency: idempotent"
	if !strings.Contains(schema, want) {
		t.Errorf("daemon/git/schema.graphql: missing exact literal %q\n"+
			"The worktreeRemove field doc-string must carry this literal to mirror\n"+
			"the comment in daemon/git/mutations.go WorktreeRemove resolver.", want)
	}
}

// TestSchemaWorktreeRemoveInputType verifies that the input type for worktreeRemove
// is correctly declared in the schema (M3/S4: single Input object).
func TestSchemaWorktreeRemoveInputType(t *testing.T) {
	schema := readGitSchema(t)
	// The extend type Mutation block should reference WorktreeRemoveInput.
	if !strings.Contains(schema, "WorktreeRemoveInput") {
		t.Error("daemon/git/schema.graphql: missing WorktreeRemoveInput reference in worktreeRemove field")
	}
}

// TestSchemaMutationsGoNowExistsComment verifies the comment was updated
// to reflect that worktreeRemove is now wired (not "to be added").
func TestSchemaMutationsGoNowExistsComment(t *testing.T) {
	schema := readGitSchema(t)
	// The old stale comment "Mutations (to be added in #613)" should be gone
	// or updated to reflect wired status.
	if strings.Contains(schema, "Mutations (to be added in #613):") {
		t.Error("daemon/git/schema.graphql: stale comment 'Mutations (to be added in #613):' still present; " +
			"update to reflect that worktreeRemove is now wired")
	}
}
