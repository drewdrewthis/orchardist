// webhook.go — GitHub webhook handler for cache invalidation.
//
// R16: cache is dropped BEFORE Invalidate is called so subscribers see
// fresh data when they re-fetch in response to the event.
//
// This handler is a cache-invalidation side channel, not a mutation
// surface. The full webhook subscription / delivery loop is a post-v1
// concern; this stub provides the endpoint shape.
package gh

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WebhookHandler validates GitHub webhook deliveries and invalidates
// the appropriate provider cache entries.
type WebhookHandler struct {
	Provider *Provider
	// Secret is the HMAC-SHA256 secret. When empty, signature validation
	// is skipped (dev / test mode).
	Secret string
	// Clock injects time.Now for tests.
	Clock func() time.Time
}

// NewWebhookHandler constructs a handler bound to a Provider.
func NewWebhookHandler(p *Provider, secret string) *WebhookHandler {
	return &WebhookHandler{
		Provider: p,
		Secret:   secret,
		Clock:    time.Now,
	}
}

// ServeHTTP implements http.Handler. Mounted by the daemon at /webhook/github.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	if h.Secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !validWebhookSignature(sig, h.Secret, body) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	event := r.Header.Get("X-GitHub-Event")
	if event == "" {
		http.Error(w, "missing X-GitHub-Event header", http.StatusBadRequest)
		return
	}

	if h.Provider == nil {
		http.Error(w, "gh provider not configured", http.StatusServiceUnavailable)
		return
	}

	at := h.clock()
	nodeID, reason, err := h.decodeAndInvalidate(event, body, at)
	if err != nil {
		h.Provider.logger.Warn("gh webhook: decode skipped",
			"event", event, "err", err)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"ignored"}`)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"status":"ok","node_id":"%s","reason":"%s"}`, nodeID, reason)
}

func (h *WebhookHandler) clock() time.Time {
	if h.Clock != nil {
		return h.Clock()
	}
	return time.Now()
}

// validWebhookSignature compares the X-Hub-Signature-256 header against
// the expected HMAC of body using secret.
func validWebhookSignature(header, secret string, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	got := mac.Sum(nil)
	return hmac.Equal(want, got)
}

// decodeAndInvalidate extracts the touched node and pushes an
// InvalidationEvent. R16: cache is dropped BEFORE Invalidate is called.
func (h *WebhookHandler) decodeAndInvalidate(event string, body []byte, at time.Time) (string, string, error) {
	p := h.Provider
	switch event {
	case "pull_request":
		var payload struct {
			PullRequest struct {
				Number int `json:"number"`
			} `json:"pull_request"`
			Repository struct {
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name string `json:"name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", "", fmt.Errorf("decode pull_request: %w", err)
		}
		owner := payload.Repository.Owner.Login
		name := payload.Repository.Name
		num := payload.PullRequest.Number
		if owner == "" || name == "" || num == 0 {
			return "", "", errors.New("missing repository or pull_request fields")
		}
		nodeID := fmt.Sprintf("PullRequest:%s/%s#%d", owner, name, num)
		// R16: drop cache BEFORE broadcasting invalidation.
		p.DropPRCache(PullRequestKey{Owner: owner, Name: name, Number: num})
		p.Invalidate(nodeID, "webhook:pull_request", at)
		return nodeID, "webhook:pull_request", nil

	case "issues":
		var payload struct {
			Issue struct {
				Number int `json:"number"`
			} `json:"issue"`
			Repository struct {
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name string `json:"name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", "", fmt.Errorf("decode issues: %w", err)
		}
		owner := payload.Repository.Owner.Login
		name := payload.Repository.Name
		num := payload.Issue.Number
		if owner == "" || name == "" || num == 0 {
			return "", "", errors.New("missing repository or issue fields")
		}
		nodeID := fmt.Sprintf("Issue:%s/%s#%d", owner, name, num)
		p.DropIssueCache(IssueKey{Owner: owner, Name: name, Number: num})
		p.Invalidate(nodeID, "webhook:issues", at)
		return nodeID, "webhook:issues", nil

	case "workflow_run":
		var payload struct {
			WorkflowRun struct {
				ID int64 `json:"id"`
			} `json:"workflow_run"`
			Repository struct {
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name string `json:"name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", "", fmt.Errorf("decode workflow_run: %w", err)
		}
		owner := payload.Repository.Owner.Login
		name := payload.Repository.Name
		runID := payload.WorkflowRun.ID
		if owner == "" || name == "" || runID == 0 {
			return "", "", errors.New("missing repository or workflow_run fields")
		}
		nodeID := fmt.Sprintf("WorkflowRun:%s/%s#%d", owner, name, runID)
		p.DropRunCache(WorkflowRunKey{Owner: owner, Name: name, RunID: runID})
		p.Invalidate(nodeID, "webhook:workflow_run", at)
		return nodeID, "webhook:workflow_run", nil

	default:
		return "", "", fmt.Errorf("event %q not surfaced", event)
	}
}
