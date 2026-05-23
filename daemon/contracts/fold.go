package contracts

import "time"

// Fold reduces an ordered list of events into the current state of every
// contract they touch. Pure function — no IO, no clock, no globals.
//
// Event kinds Fold recognises:
//
//   - "contract": creation. Initialises a Contract record.
//   - "status_change": updates Status. Fold trusts the `to` field.
//   - "criterion_added": appends to Criteria, in event order.
//   - "question_asked": appends to OpenQuestions.
//   - "question_answered": removes the matching QuestionID from OpenQuestions.
//   - "question_timed_out": removes the matching QuestionID from OpenQuestions.
//   - "cancel_requested" / "cancel_acked" / "cancel_withdrawn" /
//     "judge_run" / "judge_run_failed" / "cooldown_set" / "wait_started" /
//     "child_linked" / "child_cancelled": LastEventAt only — the status moves
//     via the paired status_change event the plugin always writes.
//
// Unknown kinds are silently dropped so plugin extensions do not break the
// daemon. Events that arrive before their contract's creation record are also
// dropped — a pathological shape that should not occur in practice.
//
// Events are processed in the slice order; callers must pre-sort by timestamp
// if their input is unsorted. On-disk JSONL is naturally in append order.
func Fold(events []Event) map[ContractID]Contract {
	state := make(map[ContractID]Contract)
	for _, ev := range events {
		applyEvent(state, ev)
	}
	return state
}

// applyEvent dispatches one event into the fold. Extracted from Fold so
// streaming callers (the provider's incremental update path) can apply events
// one at a time without rebuilding the whole map.
func applyEvent(state map[ContractID]Contract, ev Event) {
	switch ev.Kind {
	case "contract":
		applyCreation(state, ev)
	case "status_change":
		applyStatusChange(state, ev)
	case "criterion_added":
		applyCriterionAdded(state, ev)
	case "question_asked":
		applyQuestionAsked(state, ev)
	case "question_answered", "question_timed_out":
		applyQuestionResolved(state, ev)
	case "cancel_requested",
		"cancel_acked",
		"cancel_withdrawn",
		"judge_run",
		"judge_run_failed",
		"cooldown_set",
		"wait_started",
		"child_linked",
		"child_cancelled":
		touchLastEvent(state, ContractID(ev.ID), eventTime(ev))
	}
}

// touchLastEvent updates LastEventAt on the named contract, leaving all
// other fields untouched. Used for events that signal activity but whose
// effect on contract state is captured by a paired event.
func touchLastEvent(state map[ContractID]Contract, id ContractID, at time.Time) {
	c, ok := state[id]
	if !ok {
		return
	}
	if !at.IsZero() {
		c.LastEventAt = at
	}
	state[id] = c
}

// applyCreation initialises a contract from a `kind: contract` row. If the
// contract already exists (re-creation), the new fields overwrite in place.
func applyCreation(state map[ContractID]Contract, ev Event) {
	if ev.ID == "" {
		return
	}
	id := ContractID(ev.ID)

	created := zeroOr(ev.CreatedOn)
	updated := zeroOr(ev.UpdatedOn)
	if updated.IsZero() {
		updated = created
	}

	status := rawToStatus(ev.InitialStatus)

	c := Contract{
		ID:          id,
		Statement:   ev.Statement,
		Status:      status,
		CreatedAt:   created,
		UpdatedAt:   updated,
		LastEventAt: updated,
	}
	if ev.Owner != nil {
		c.OwnerSessionID = ev.Owner.SessionID
		if ev.Owner.AgentName != nil {
			c.OwnerAgentName = *ev.Owner.AgentName
		}
	}
	c.ReportsTo = renderParty(ev.ReportsTo)
	if ev.Parent != nil {
		c.ParentContractID = *ev.Parent
	}

	// Preserve criteria/questions accumulated before the creation row in
	// degenerate inputs — not observed in practice but cheap to guard.
	if existing, ok := state[id]; ok {
		c.Criteria = existing.Criteria
		c.OpenQuestions = existing.OpenQuestions
		if !existing.LastEventAt.IsZero() && existing.LastEventAt.After(c.LastEventAt) {
			c.LastEventAt = existing.LastEventAt
		}
	}
	state[id] = c
}

func applyStatusChange(state map[ContractID]Contract, ev Event) {
	id := ContractID(ev.ID)
	c, ok := state[id]
	if !ok {
		// status_change before creation — drop.
		return
	}
	if ev.To != "" {
		c.Status = rawToStatus(ev.To)
	}
	t := eventTime(ev)
	if !t.IsZero() {
		c.UpdatedAt = t
		c.LastEventAt = t
	}
	state[id] = c
}

func applyCriterionAdded(state map[ContractID]Contract, ev Event) {
	id := ContractID(ev.ID)
	c, ok := state[id]
	if !ok {
		return
	}
	if ev.Criterion != "" {
		c.Criteria = append(c.Criteria, ev.Criterion)
	}
	if t := eventTime(ev); !t.IsZero() {
		c.LastEventAt = t
	}
	state[id] = c
}

func applyQuestionAsked(state map[ContractID]Contract, ev Event) {
	id := ContractID(ev.ID)
	c, ok := state[id]
	if !ok {
		return
	}
	q := OpenQuestion{
		QuestionID:  ev.QuestionID,
		Text:        ev.QuestionText,
		AskedBy:     ev.By,
		AskedAt:     zeroOr(ev.Timestamp),
		Deadline:    ev.QuestionDeadline,
		BlocksClose: ev.QuestionBlocks == nil || *ev.QuestionBlocks,
	}
	c.OpenQuestions = append(c.OpenQuestions, q)
	if t := eventTime(ev); !t.IsZero() {
		c.LastEventAt = t
	}
	state[id] = c
}

func applyQuestionResolved(state map[ContractID]Contract, ev Event) {
	id := ContractID(ev.ID)
	c, ok := state[id]
	if !ok {
		return
	}
	if ev.QuestionID != "" {
		filtered := c.OpenQuestions[:0]
		for _, q := range c.OpenQuestions {
			if q.QuestionID == ev.QuestionID {
				continue
			}
			filtered = append(filtered, q)
		}
		// Re-allocate so the trimmed slice does not retain backing memory.
		c.OpenQuestions = append([]OpenQuestion(nil), filtered...)
	}
	if t := eventTime(ev); !t.IsZero() {
		c.LastEventAt = t
	}
	state[id] = c
}

// rawToStatus maps the plugin's raw lowercase status string to the domain's
// ContractStatus constant. Unknown values fall through to StatusOpen — the
// safest default for a future-tense enum addition.
func rawToStatus(raw string) ContractStatus {
	switch raw {
	case "open":
		return StatusOpen
	case "delivered_pending_validation":
		return StatusDeliveredPendingValidation
	case "delivered_pending_parent_validation":
		return StatusDeliveredPendingParentValidation
	case "pending_user_approval", "pending_drew_approval":
		// Accept both old and new plugin status strings.
		return StatusPendingUserApproval
	case "awaiting_cancel_ack":
		return StatusAwaitingCancelAck
	case "waiting_external":
		return StatusWaitingExternal
	case "satisfied":
		return StatusSatisfied
	case "cancelled":
		return StatusCancelled
	case "judge_rejected_terminal":
		return StatusJudgeRejectedTerminal
	default:
		return StatusOpen
	}
}

// renderParty turns a Party pointer into the schema's flat reportsTo string.
func renderParty(p *Party) string {
	if p == nil {
		return ""
	}
	if p.Kind == "drew" {
		return "drew"
	}
	if p.Kind == "agent" && p.AgentName != nil && *p.AgentName != "" {
		return "agent:" + *p.AgentName
	}
	if p.Kind != "" {
		return p.Kind
	}
	return ""
}

// eventTime returns the most relevant timestamp for the event.
func eventTime(ev Event) time.Time {
	if ev.Kind == "contract" {
		t := zeroOr(ev.UpdatedOn)
		if t.IsZero() {
			t = zeroOr(ev.CreatedOn)
		}
		return t
	}
	return zeroOr(ev.Timestamp)
}

// zeroOr unwraps a *time.Time to a zero time.Time when nil.
func zeroOr(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
