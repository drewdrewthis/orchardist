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

// exitVerbs is the set of bare verbs that trigger a virtual conversation-
// contract close when found inside a local_command content block.
var exitVerbs = map[string]bool{
	"exit": true,
	"quit": true,
	"bye":  true,
}

// isExitRecord returns true when rec is a system record containing a
// local_command tag for one of the exit verbs (/exit, /quit, /bye).
func isExitRecord(rec SessionRecord) bool {
	if rec.Type != "system" || rec.Content == "" {
		return false
	}
	for verb := range exitVerbs {
		if strings.Contains(rec.Content, "<command-name>/"+verb+"</command-name>") {
			return true
		}
	}
	return false
}

// applyExitRecord scans state for the open conversation contract owned by
// localSessionID and closes it with reason "delivered". If no such contract
// exists the call is a no-op.
//
// Called from applySessionRecord when an exit/quit/bye local_command record
// is detected. The timestamp from the exit record is used as the closure
// timestamp so UpdatedAt/LastEventAt reflect when the session ended.
func applyExitRecord(state map[ContractID]Contract, rec SessionRecord, localSessionID string) {
	if !isExitRecord(rec) {
		return
	}
	closedAt := parseRFC3339(rec.Timestamp)

	// Find the open conversation contract for this session. We iterate over
	// state rather than relying on any index because the map is small and this
	// path is cold (executed at most once per session replay).
	for id, c := range state {
		if c.OwnerSessionID != localSessionID {
			continue
		}
		if c.Statement != ConversationContractDeliverable {
			continue
		}
		if c.Status != "open" {
			continue
		}
		// Close the conversation contract in-memory.
		c.Status = "closed"
		c.ClosedReason = "delivered"
		if !closedAt.IsZero() {
			c.UpdatedAt = closedAt
			c.LastEventAt = closedAt
		}
		state[id] = c
		return // only one open conversation contract per session
	}
}
