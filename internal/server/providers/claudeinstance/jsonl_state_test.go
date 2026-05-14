package claudeinstance

import (
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

var testNow = time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

// ts builds a test timestamp offset from testNow.
func ts(secOffset int) time.Time {
	return testNow.Add(time.Duration(secOffset) * time.Second)
}

func assistantRecord(t time.Time, stopReason string, content []ContentItem) Record {
	return Record{
		Timestamp: t,
		Type:      "assistant",
		Message: &Message{
			StopReason: stopReason,
			Content:    content,
		},
	}
}

func userRecord(t time.Time, content []ContentItem) Record {
	return Record{
		Timestamp: t,
		Type:      "user",
		Message:   &Message{Content: content},
	}
}

func systemRecord(t time.Time, subtype string) Record {
	return Record{
		Timestamp: t,
		Type:      "system",
		System:    &SystemInfo{Subtype: subtype},
	}
}

func toolUseContent(id, name string) ContentItem {
	return ContentItem{Type: "tool_use", ID: id, Name: name}
}

func toolResultContent(toolUseID string) ContentItem {
	return ContentItem{Type: "tool_result", ToolUseID: toolUseID}
}

// TestClassifyState_Idle_EndTurn: tail ends with end_turn then turn_duration.
// State must be idle with no inflight tools.
func TestClassifyState_Idle_EndTurn(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "end_turn", nil),
		systemRecord(ts(2), "turn_duration"),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle", snap.State)
	}
	if snap.InflightToolCount != 0 {
		t.Errorf("inflight = %d, want 0", snap.InflightToolCount)
	}
}

// TestClassifyState_Idle_MaxTokens: tail ends with max_tokens.
// State must be idle.
func TestClassifyState_Idle_MaxTokens(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "max_tokens", nil),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle", snap.State)
	}
}

// TestClassifyState_Working_OpenToolUse: last assistant has stop_reason tool_use
// and one open tool_use with no matching tool_result.
// State must be working with inflight==1.
func TestClassifyState_Working_OpenToolUse(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("toolu_X", "Bash"),
		}),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working", snap.State)
	}
	if snap.InflightToolCount != 1 {
		t.Errorf("inflight = %d, want 1", snap.InflightToolCount)
	}
}

// TestClassifyState_Working_ToolUseWithResults: 4 parallel tool_use, 2 tool_results matched.
// State must be working with inflight==2.
func TestClassifyState_Working_ToolUseWithResults(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("tu_1", "Bash"),
			toolUseContent("tu_2", "Bash"),
			toolUseContent("tu_3", "Bash"),
			toolUseContent("tu_4", "Bash"),
		}),
		userRecord(ts(2), []ContentItem{
			toolResultContent("tu_1"),
			toolResultContent("tu_2"),
		}),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working", snap.State)
	}
	if snap.InflightToolCount != 2 {
		t.Errorf("inflight = %d, want 2", snap.InflightToolCount)
	}
}

// TestClassifyState_Working_PostToolResultBeforeAssistant: last record is a
// user tool_result with no end_turn after it.
// State must be working.
func TestClassifyState_Working_PostToolResultBeforeAssistant(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("tu_A", "Bash"),
		}),
		userRecord(ts(2), []ContentItem{
			toolResultContent("tu_A"),
		}),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working (tool result delivered, no end_turn yet)", snap.State)
	}
}

// TestClassifyState_Input_AskUserQuestion: open AskUserQuestion tool_use
// with no matching tool_result. State must be input.
func TestClassifyState_Input_AskUserQuestion(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("ask_99", "AskUserQuestion"),
		}),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateInput {
		t.Errorf("state = %s, want input", snap.State)
	}
}

// TestClassifyState_Input_AskUserQuestionAnswered: AskUserQuestion answered,
// followed by end_turn. State must be idle (the question was answered).
func TestClassifyState_Input_AskUserQuestionAnswered(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("ask_99", "AskUserQuestion"),
		}),
		userRecord(ts(2), []ContentItem{
			toolResultContent("ask_99"),
		}),
		assistantRecord(ts(3), "end_turn", nil),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle (AskUserQuestion answered, end_turn followed)", snap.State)
	}
}

// TestClassifyState_NotificationLike_CollapsesToWorking: a Bash tool_use is
// open with no tool_result. No AskUserQuestion anywhere. The hook would have
// written this as `input` on a Notification event, but the classifier MUST
// return working — this is the load-bearing negative test for the whole refactor.
func TestClassifyState_NotificationLike_CollapsesToWorking(t *testing.T) {
	records := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("bash_001", "Bash"),
		}),
	}
	snap := ClassifyState(records, testNow)
	if snap.State == graphql.InstanceStateInput {
		t.Error("state = input, want working — Notification-driven input must not appear in jsonl classifier")
	}
	if snap.State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working (non-AskUserQuestion open tool_use)", snap.State)
	}
}

// TestClassifyState_OrphanedToolUseBeforeBoundary: an orphaned tool_use from
// an old turn, then 3 end_turn boundaries. State must be idle with inflight==0.
func TestClassifyState_OrphanedToolUseBeforeBoundary(t *testing.T) {
	records := []Record{
		// Turn 1: orphan tool_use, never answered
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("orphan_1", "Bash"),
		}),
		assistantRecord(ts(2), "end_turn", nil),
		systemRecord(ts(3), "turn_duration"),
		// Turn 2
		userRecord(ts(4), nil),
		assistantRecord(ts(5), "end_turn", nil),
		systemRecord(ts(6), "turn_duration"),
		// Turn 3
		userRecord(ts(7), nil),
		assistantRecord(ts(8), "end_turn", nil),
		systemRecord(ts(9), "turn_duration"),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle (orphan before boundaries)", snap.State)
	}
	if snap.InflightToolCount != 0 {
		t.Errorf("inflight = %d, want 0 (orphan scoped to old turn)", snap.InflightToolCount)
	}
}

// TestClassifyState_EmptyFile: no records at all. State must be idle.
func TestClassifyState_EmptyFile(t *testing.T) {
	snap := ClassifyState(nil, testNow)
	if snap.State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle for empty records", snap.State)
	}
	snap2 := ClassifyState([]Record{}, testNow)
	if snap2.State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle for empty slice", snap2.State)
	}
}

// TestClassifyState_SidechainExcluded: the classifier receives records with
// sidechain already filtered out (by readRecordsFromPath). Verify that if we
// call ClassifyState with only non-sidechain records, the sidechain tool_use
// does not affect the result. Simulate by building a filtered record set.
func TestClassifyState_SidechainExcluded(t *testing.T) {
	// The sidechain record would be isSidechain=true — but readRecordsFromPath
	// strips it before ClassifyState sees it. We simulate by only passing the
	// non-sidechain records.
	nonSidechain := []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "end_turn", nil),
		systemRecord(ts(2), "turn_duration"),
	}
	// A sidechain record with open tool_use was excluded; we never pass it.
	snap := ClassifyState(nonSidechain, testNow)
	if snap.State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle (sidechain tool_use excluded)", snap.State)
	}
}

// TestClassifyState_InflightScopedToCurrentTurn: 1 orphan tool_use in a
// completed turn, then a fresh turn with 2 open tool_use. Inflight must
// be 2, not 3.
func TestClassifyState_InflightScopedToCurrentTurn(t *testing.T) {
	records := []Record{
		// Completed prior turn with orphan
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{
			toolUseContent("orphan_tu", "Bash"),
		}),
		assistantRecord(ts(2), "end_turn", nil),
		systemRecord(ts(3), "turn_duration"),
		// Current turn: 2 open tool_use
		userRecord(ts(4), nil),
		assistantRecord(ts(5), "tool_use", []ContentItem{
			toolUseContent("cur_tu_1", "Bash"),
			toolUseContent("cur_tu_2", "Read"),
		}),
	}
	snap := ClassifyState(records, testNow)
	if snap.State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working", snap.State)
	}
	if snap.InflightToolCount != 2 {
		t.Errorf("inflight = %d, want 2 (scoped to current turn, orphan excluded)", snap.InflightToolCount)
	}
}
