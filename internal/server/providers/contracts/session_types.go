package contracts

import "encoding/json"

// SessionRecord is one line from a Claude Code session JSONL file
// (~/.claude/projects/<encoded-cwd>/<uuid>.jsonl). Only the fields
// relevant to ContractFold are decoded; everything else is ignored.
//
// The type discriminator is the `type` field. ContractFold cares about
// records whose type is "assistant" and whose message.content includes
// a tool_use entry with name "open_contract" or "close_contract".
type SessionRecord struct {
	// Type is the record's kind ("assistant", "user", "system", etc.).
	Type string `json:"type"`

	// SessionID is the session UUID embedded in every record by Claude Code.
	// Used to derive ownerSessionId for open_contract events.
	SessionID string `json:"sessionId"`

	// Timestamp is the record's RFC 3339 timestamp. Used as a fallback
	// for the contract's CreatedAt when the open_contract input omits it.
	Timestamp string `json:"timestamp,omitempty"`

	// Message is non-nil for "assistant" and "user" type records.
	Message *SessionMessage `json:"message,omitempty"`
}

// SessionMessage is the message payload inside an assistant or user record.
type SessionMessage struct {
	Role    string                 `json:"role"`
	Content []SessionContentBlock  `json:"content"`
}

// SessionContentBlock is one element of message.content. Each block has a
// `type` discriminator; ContractFold reads "tool_use" blocks.
type SessionContentBlock struct {
	// Type is "text", "tool_use", "tool_result", etc.
	Type string `json:"type"`

	// Name is set for tool_use blocks (e.g. "open_contract", "close_contract").
	Name string `json:"name,omitempty"`

	// Input holds the tool arguments. Raw JSON so we can decode lazily.
	Input json.RawMessage `json:"input,omitempty"`
}

// OpenContractInput is the input shape for the open_contract MCP tool.
// The plugin writes: id, deliverable (or "statement"), createdAt.
type OpenContractInput struct {
	// ID is the contract id (e.g. "C-2026-05-21-ABCD1234").
	ID string `json:"id"`

	// Deliverable is the contract's statement / terms.
	// Some versions of the plugin use the key "statement" instead;
	// both are accepted.
	Deliverable string `json:"deliverable"`
	Statement   string `json:"statement"` // alias

	// CreatedAt is the RFC 3339 creation timestamp.
	CreatedAt string `json:"createdAt"`
}

// effectiveDeliverable returns the non-empty one of Deliverable / Statement.
func (o OpenContractInput) effectiveDeliverable() string {
	if o.Deliverable != "" {
		return o.Deliverable
	}
	return o.Statement
}

// CloseContractInput is the input shape for the close_contract MCP tool.
type CloseContractInput struct {
	// ID is the contract id being closed.
	ID string `json:"id"`

	// ClosedAt is the RFC 3339 closure timestamp.
	ClosedAt string `json:"closedAt"`

	// ClosedReason is "delivered" or "abandoned".
	ClosedReason string `json:"closedReason"`

	// AboutSessionId is set on F2 non-owner abandon: the close event
	// lives in a different session's jsonl but targets a contract owned
	// by the session named here. When present it does NOT change the
	// contract's OwnerSessionID — it is only used to look up the
	// contract in the global index.
	AboutSessionID string `json:"aboutSessionId"`
}
