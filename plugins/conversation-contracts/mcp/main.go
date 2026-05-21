// Package main implements the conversation-contracts MCP server.
//
// It speaks JSON-RPC 2.0 over stdin/stdout (the MCP stdio transport).
// Claude Code passes CLAUDE_SESSION_ID as an env variable when invoking
// the server, which is how the server knows which session jsonl to write to.
//
// Supported MCP methods:
//   - initialize              – capability negotiation
//   - tools/list              – returns [open_contract, close_contract]
//   - tools/call open_contract  – generates a contract, writes a tool_use
//                                 event to the calling session's jsonl
//   - tools/call close_contract – writes a close_contract tool_use event to
//                                 the calling session's jsonl (L1.3/L1.5)
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---- JSON-RPC 2.0 wire types -----------------------------------------------

type rpcRequest struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	Jsonrpc string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---- MCP types --------------------------------------------------------------

type toolParam struct {
	Type        string                 `json:"type"`
	Description string                 `json:"description,omitempty"`
	Properties  map[string]toolParam   `json:"properties,omitempty"`
	Required    []string               `json:"required,omitempty"`
	Enum        []string               `json:"enum,omitempty"`
}

type toolDef struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	InputSchema toolParam `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ---- Session jsonl types ----------------------------------------------------

// sessionRecord is the outer wrapper of every line in a session jsonl.
// For a synthetic tool_use the "message" field carries the assistant content.
type sessionRecord struct {
	Type    string          `json:"type"`
	Message sessionMessage  `json:"message"`
	UUID    string          `json:"uuid"`
	Timestamp string        `json:"timestamp"`
}

type sessionMessage struct {
	Role    string           `json:"role"`
	Content []messageContent `json:"content"`
}

type messageContent struct {
	Type  string      `json:"type"`
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Input interface{} `json:"input"`
}

// ---- Contract event payload ------------------------------------------------

// openContractInput is the tool_use input written to the jsonl.
type openContractInput struct {
	ID          string `json:"id"`
	Deliverable string `json:"deliverable"`
	CreatedAt   string `json:"createdAt"`
}

// closeContractInput is the tool_use input written to the jsonl for close_contract.
// closedNote and aboutSessionId are optional (omitempty).
type closeContractInput struct {
	ID             string `json:"id"`
	ClosedAt       string `json:"closedAt"`
	ClosedReason   string `json:"closedReason"`
	ClosedNote     string `json:"closedNote,omitempty"`
	AboutSessionId string `json:"aboutSessionId,omitempty"`
}

// ---- MCP tools list --------------------------------------------------------

var knownTools = []toolDef{
	{
		Name:        "open_contract",
		Description: "Open a new contract and write a tool_use event to the calling session's jsonl. Returns the generated contract id (C-YYYY-MM-DD-XXXXXXXX).",
		InputSchema: toolParam{
			Type: "object",
			Properties: map[string]toolParam{
				"deliverable": {
					Type:        "string",
					Description: "What this contract covers — what must be true for the contract to close as delivered.",
				},
			},
			Required: []string{"deliverable"},
		},
	},
	{
		Name:        "close_contract",
		Description: "Closes a contract by writing a close_contract event to the calling session's JSONL. Takes a contract id, a reason (\"delivered\" or \"abandoned\"), and optionally an aboutSessionId for non-owner abandons; the fold projection picks up the event on the next refresh.",
		InputSchema: toolParam{
			Type: "object",
			Properties: map[string]toolParam{
				"id": {
					Type:        "string",
					Description: "The contract id (C-YYYY-MM-DD-XXXXXXXX) to close.",
				},
				"reason": {
					Type:        "string",
					Enum:        []string{"delivered", "abandoned"},
					Description: "Reason for closing the contract.",
				},
			},
			Required: []string{"id", "reason"},
		},
	},
}

// ---- Session jsonl path resolution -----------------------------------------

// sessionJsonlPath resolves the absolute path to the calling session's jsonl.
//
// Path pattern: <projectsRoot>/<encoded-cwd>/<session-uuid>.jsonl
// Default root: ~/.claude/projects (overridable via CLAUDE_PROJECTS_DIR so the
// provider and MCP writer stay in sync when tests or alternate installs move
// the tree).
// Encoding: every '/' and '.' in the absolute cwd path becomes '-'.
//
// The server reads:
//   CLAUDE_SESSION_ID    – the calling session UUID
//   CLAUDE_PROJECTS_DIR  – optional projects-root override
//   HOME                 – the user home directory (fallback when no override)
//   PWD                  – the current working directory when Claude invoked the server
func sessionJsonlPath() (string, error) {
	sessionID := os.Getenv("CLAUDE_SESSION_ID")
	if sessionID == "" {
		return "", fmt.Errorf("CLAUDE_SESSION_ID env var is not set")
	}
	projectsRoot := os.Getenv("CLAUDE_PROJECTS_DIR")
	if projectsRoot == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("HOME env var is not set")
		}
		projectsRoot = filepath.Join(home, ".claude", "projects")
	}
	cwd := os.Getenv("PWD")
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cannot determine cwd: %w", err)
		}
	}
	encoded := encodeCwd(cwd)
	return filepath.Join(projectsRoot, encoded, sessionID+".jsonl"), nil
}

// encodeCwd converts an absolute path to the encoded directory name Claude
// uses under ~/.claude/projects/.  Both '/' and '.' are replaced with '-'.
//
// Examples:
//   /home/user/workspace          → -home-user-workspace
//   /home/user/workspace/foo/.bar → -home-user-workspace-foo--bar
func encodeCwd(cwd string) string {
	r := strings.NewReplacer("/", "-", ".", "-")
	return r.Replace(cwd)
}

// ---- Contract ID generation ------------------------------------------------

// newContractID returns a contract id in the form C-YYYY-MM-DD-XXXXXXXX.
// XXXXXXXX is 8 lowercase hex characters from crypto/rand — the fold
// keys map[ContractID]Contract by this id, so a same-nanosecond collision
// between two parallel sessions would silently merge distinct contracts.
// crypto/rand prevents that; nanosecond fallback keeps the function pure
// in pathological no-entropy environments.
func newContractID(now time.Time) string {
	date := now.UTC().Format("2006-01-02")
	return fmt.Sprintf("C-%s-%s", date, randomHex8(now))
}

// randomHex8 returns 8 lowercase hex chars from crypto/rand, or a
// nanosecond-derived fallback if the rng read fails.
func randomHex8(now time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%08x", uint32(now.UnixNano()))
}

// ---- open_contract handler -------------------------------------------------

// openContractArgs is the unmarshalled arguments for the open_contract call.
type openContractArgs struct {
	Deliverable string `json:"deliverable"`
}

// handleOpenContract implements the open_contract MCP tool.
// It generates a contract ID, writes a tool_use event to the calling
// session's jsonl, and returns the contract id.
//
// jsonlPath is injected so tests can override it without touching env.
func handleOpenContract(args openContractArgs, jsonlPath string, now time.Time) (string, error) {
	contractID := newContractID(now)
	createdAt := now.UTC().Format(time.RFC3339)

	input := openContractInput{
		ID:          contractID,
		Deliverable: args.Deliverable,
		CreatedAt:   createdAt,
	}

	record := sessionRecord{
		Type:      "assistant",
		Timestamp: createdAt,
		UUID:      contractID, // use contract id as the record UUID for traceability
		Message: sessionMessage{
			Role: "assistant",
			Content: []messageContent{
				{
					Type:  "tool_use",
					ID:    contractID,
					Name:  "open_contract",
					Input: input,
				},
			},
		},
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(jsonlPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open jsonl %s: %w", jsonlPath, err)
	}

	line, err := json.Marshal(record)
	if err != nil {
		_ = f.Close()
		return "", fmt.Errorf("marshal record: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write jsonl: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close jsonl %s: %w", jsonlPath, err)
	}

	return contractID, nil
}

// ---- close_contract handler ------------------------------------------------

// closeContractArgs is the unmarshalled arguments for the close_contract call.
type closeContractArgs struct {
	ID             string `json:"id"`
	Reason         string `json:"reason"`
	ClosedNote     string `json:"closedNote,omitempty"`
	AboutSessionId string `json:"aboutSessionId,omitempty"`
}

// handleCloseContract implements the close_contract MCP tool.
// It writes a close_contract tool_use event to the calling session's jsonl.
// For F2 (non-owner abandon), the caller passes aboutSessionId pointing at the
// owner session; the event is still written to the CALLER's jsonl (jsonlPath).
//
// jsonlPath is injected so tests can override it without touching env.
func handleCloseContract(args closeContractArgs, jsonlPath string, now time.Time) error {
	if args.ID == "" {
		return fmt.Errorf("id is required")
	}
	if args.Reason != "delivered" && args.Reason != "abandoned" {
		return fmt.Errorf("reason must be 'delivered' or 'abandoned', got %q", args.Reason)
	}

	closedAt := now.UTC().Format(time.RFC3339)
	// Use a collision-resistant suffix for the event record UUID so two
	// close_contract calls in the same nanosecond can't share a tool_use ID
	// (which would break tool_use/tool_result correlation).
	eventUUID := fmt.Sprintf("close-%s-%s", args.ID, randomHex8(now))

	input := closeContractInput{
		ID:             args.ID,
		ClosedAt:       closedAt,
		ClosedReason:   args.Reason,
		ClosedNote:     args.ClosedNote,
		AboutSessionId: args.AboutSessionId,
	}

	record := sessionRecord{
		Type:      "assistant",
		Timestamp: closedAt,
		UUID:      eventUUID,
		Message: sessionMessage{
			Role: "assistant",
			Content: []messageContent{
				{
					Type:  "tool_use",
					ID:    eventUUID,
					Name:  "close_contract",
					Input: input,
				},
			},
		},
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(jsonlPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open jsonl %s: %w", jsonlPath, err)
	}

	line, err := json.Marshal(record)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("marshal record: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		_ = f.Close()
		return fmt.Errorf("write jsonl: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close jsonl %s: %w", jsonlPath, err)
	}

	return nil
}

// ---- MCP request dispatch --------------------------------------------------

func handleRequest(req rpcRequest) rpcResponse {
	base := rpcResponse{Jsonrpc: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		base.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "conversation-contracts", "version": "0.8.0"},
		}

	case "notifications/initialized":
		// Client notification — no response required, but we need to handle it
		// without erroring. Return nil result; the caller will skip writing.
		return rpcResponse{} // sentinel: empty Jsonrpc means skip

	case "tools/list":
		base.Result = toolsListResult{Tools: knownTools}

	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			base.Error = &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
			return base
		}

		switch params.Name {
		case "open_contract":
			var args openContractArgs
			if err := json.Unmarshal(params.Arguments, &args); err != nil {
				base.Error = &rpcError{Code: -32602, Message: "invalid arguments: " + err.Error()}
				return base
			}
			if args.Deliverable == "" {
				base.Error = &rpcError{Code: -32602, Message: "deliverable is required"}
				return base
			}
			jsonlPath, err := sessionJsonlPath()
			if err != nil {
				base.Error = &rpcError{Code: -32603, Message: "session resolution: " + err.Error()}
				return base
			}
			contractID, err := handleOpenContract(args, jsonlPath, time.Now())
			if err != nil {
				base.Error = &rpcError{Code: -32603, Message: "open_contract: " + err.Error()}
				return base
			}
			base.Result = toolCallResult{
				Content: []toolContent{{Type: "text", Text: contractID}},
			}

		case "close_contract":
			var args closeContractArgs
			if err := json.Unmarshal(params.Arguments, &args); err != nil {
				base.Error = &rpcError{Code: -32602, Message: "invalid arguments: " + err.Error()}
				return base
			}
			if args.ID == "" {
				base.Error = &rpcError{Code: -32602, Message: "id is required"}
				return base
			}
			if args.Reason != "delivered" && args.Reason != "abandoned" {
				base.Error = &rpcError{Code: -32602, Message: "reason must be 'delivered' or 'abandoned'"}
				return base
			}
			jsonlPath, err := sessionJsonlPath()
			if err != nil {
				base.Error = &rpcError{Code: -32603, Message: "session resolution: " + err.Error()}
				return base
			}
			if err := handleCloseContract(args, jsonlPath, time.Now()); err != nil {
				base.Error = &rpcError{Code: -32603, Message: "close_contract: " + err.Error()}
				return base
			}
			base.Result = toolCallResult{
				Content: []toolContent{{Type: "text", Text: args.ID}},
			}

		default:
			base.Error = &rpcError{Code: -32601, Message: "unknown tool: " + params.Name}
		}

	default:
		base.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}

	return base
}

// ---- main ------------------------------------------------------------------

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := rpcResponse{
				Jsonrpc: "2.0",
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			}
			_ = encoder.Encode(resp)
			continue
		}

		resp := handleRequest(req)
		// Empty Jsonrpc is the sentinel for "no response" (notifications).
		if resp.Jsonrpc == "" {
			continue
		}
		_ = encoder.Encode(resp)
	}
}
