package claudeinstance

// Tests for ClaudeInstance.lastActivityAt — schema-shape unit test for AC 1
// of issue #443.
//
// Behavioral tests for lastActivityAt sourcing live in composer_jsonl_test.go
// (jsonl-derived, the current authoritative source) and last_activity_at_ac3_test.go
// (subscription change-detection). The earlier heartbeat-driven scenarios
// (RFC3339FromHeartbeat, RFC3339NanoSubSecond) were retired in issue #603
// phase 3 when the heartbeat's last_activity field was removed.

import (
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// TestLastActivityAt_FieldExistsAndIsNullable verifies that the
// ClaudeInstance GraphQL model carries a nullable *string LastActivityAt
// field — the schema contract.
//
// This test will fail to compile if the field is absent or has the wrong
// type, which is sufficient proof that the schema field exists.
func TestLastActivityAt_FieldExistsAndIsNullable(t *testing.T) {
	inst := graphql.ClaudeInstance{}
	// Nullable means the zero value is nil (pointer).
	if inst.LastActivityAt != nil {
		t.Errorf("zero-value ClaudeInstance.LastActivityAt should be nil (nullable); got %v", inst.LastActivityAt)
	}
	// Assign to confirm the type is *string (compile-time check).
	v := "2026-05-07T18:42:11Z"
	inst.LastActivityAt = &v
	if inst.LastActivityAt == nil || *inst.LastActivityAt != v {
		t.Errorf("LastActivityAt = %v, want %q", inst.LastActivityAt, v)
	}
}
