package contracts

// fold_exit.go implements the L2.11/L2.12 exit auto-close extension for the
// v0.8 ContractFold projection.
//
// When the user types /exit, /quit, or /bye in a Claude session, Claude Code
// appends a system record to the session JSONL with a content field matching
// the pattern:
//
//	<command-name>/exit</command-name>
//	<command-name>/quit</command-name>
//	<command-name>/bye</command-name>
//
// The fold detects these records and synthesises a virtual
// close_contract(reason:"delivered") for the open conversation contract of
// the session. No write to the JSONL is performed; the close is derived
// entirely in-memory during the fold pass.
//
// Only the conversation contract (Statement == ConversationContractDeliverable)
// is auto-closed. Other open contracts are left untouched.

import "strings"

// exitPatterns is the set of <command-name>…</command-name> needles that
// identify an exit/quit/bye local_command record. Pre-built at package
// init so isExitRecord does not allocate a fresh "<command-name>/…</…>"
// string on every system record.
var exitPatterns = []string{
	"<command-name>/exit</command-name>",
	"<command-name>/quit</command-name>",
	"<command-name>/bye</command-name>",
}

// isExitRecord returns true when rec is a system record containing a
// local_command tag for one of the exit verbs (/exit, /quit, /bye).
func isExitRecord(rec SessionRecord) bool {
	if rec.Type != "system" || rec.Content == "" {
		return false
	}
	for _, needle := range exitPatterns {
		if strings.Contains(rec.Content, needle) {
			return true
		}
	}
	return false
}

// applyExitRecord uses the OpenIndex to find the open conversation contract
// owned by localSessionID in O(1) and closes it with reason "delivered". If
// no such contract exists the call is a no-op.
//
// Called from applySessionRecord when an exit/quit/bye local_command record
// is detected. The timestamp from the exit record is used as the closure
// timestamp so UpdatedAt/LastEventAt reflect when the session ended.
func applyExitRecord(state *FoldState, rec SessionRecord, localSessionID string) {
	if !isExitRecord(rec) {
		return
	}
	closedAt := parseRFC3339(rec.Timestamp)

	key := OpenKey{OwnerSessionID: localSessionID, Deliverable: ConversationContractDeliverable}
	id, ok := state.OpenIndex[key]
	if !ok {
		return
	}

	c := state.Contracts[id]
	c.Status = "closed"
	c.ClosedReason = "delivered"
	if !closedAt.IsZero() {
		c.UpdatedAt = closedAt
		c.LastEventAt = closedAt
	}
	state.Contracts[id] = c
	delete(state.OpenIndex, key)
}
