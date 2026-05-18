// resolver_claude_instance.go — GraphQL resolver for the ClaudeInstance type.
//
// Per R6: one file, one GraphQL type.
//
// This resolver satisfies Query.claudeInstances. Cross-domain back-edge
// fields (pane, process, account, worktree, conversation) are projected from
// the in-domain *Instance onto the wire-level graphql types that the gqlgen
// runtime expects. The actual cross-domain JOIN is performed by Provider.List
// (service.go); this file is a thin projection layer per R3/L4.
package claudeinstance

import (
	"context"
	"fmt"
	"time"
)

// ─── Projection helpers ────────────────────────────────────────────────────────

// GraphQLInstance is the wire-level ClaudeInstance projection. In the target
// layout this maps to graphql.ClaudeInstance from the generated models. We
// define a local alias here so the domain module compiles independently of the
// generated code during the swarm. The gqlgen-wired resolver in
// daemon/resolvers/ will adapt between this type and the generated model.
//
// NOTE: In Phase 1 wiring (after make generate on the composed schema), the
// generated graphql.ClaudeInstance replaces this struct. For now the domain
// module owns this projection type per R2.
type GraphQLInstance struct {
	ID                string
	State             string // InstanceState enum value as string
	RcEnabled         bool
	RcURL             *string
	SessionUUID       *string
	StartedAt         *string
	LastActivityAt    *string
	Model             *string
	InflightToolCount int64
	// Back-edge ids — resolved by other domains via extend type.
	PaneID         string // tmux pane id for TmuxPane back-edge
	AccountID      string // account id for ClaudeAccount back-edge
	ConversationID string // conversation id for Conversation back-edge
	Host           string // host for federation
	Pid            int    // pid for Process back-edge
}

// stateString converts an InstanceState to its GraphQL enum string.
func stateString(s InstanceState) string {
	switch s {
	case StateWorking:
		return "working"
	case StateIdle:
		return "idle"
	case StateInput:
		return "input"
	case StateStalled:
		return "stalled"
	case StateDead:
		return "dead"
	default:
		return "no_claude"
	}
}

// ProjectInstance converts an in-domain *Instance to the GraphQL projection.
// Returns nil when inst is nil.
func ProjectInstance(inst *Instance) *GraphQLInstance {
	if inst == nil {
		return nil
	}

	out := &GraphQLInstance{
		ID:                inst.ID,
		State:             stateString(inst.State),
		RcEnabled:         false, // populated by adapter when rc data is available
		InflightToolCount: int64(inst.InflightToolCount),
		PaneID:            inst.PaneID,
		ConversationID:    inst.ConversationID,
		Host:              inst.Host,
		Pid:               inst.Pid,
	}

	if inst.SessionUUID != "" {
		v := inst.SessionUUID
		out.SessionUUID = &v
	}
	if inst.Model != "" {
		v := inst.Model
		out.Model = &v
	}
	if !inst.LastActivityAt.IsZero() {
		v := inst.LastActivityAt.UTC().Truncate(time.Second).Format(time.RFC3339)
		out.LastActivityAt = &v
	}
	if inst.Account != nil {
		out.AccountID = inst.Account.ID
	}

	return out
}

// ─── Query resolver ────────────────────────────────────────────────────────────

// Resolver is the GraphQL resolver for the claude-instance domain. It wraps
// a Service and a Loaders bundle. In the composed daemon this struct is
// embedded in the aggregate daemon/resolvers Resolver.
type Resolver struct {
	svc     Service
	loaders *Loaders
}

// NewResolver constructs a Resolver.
func NewResolver(svc Service, loaders *Loaders) *Resolver {
	return &Resolver{svc: svc, loaders: loaders}
}

// ClaudeInstances resolves Query.claudeInstances.
//
// Uses the InstancesByHost loader when available (hot path, R3); falls
// back to svc.List directly (e.g. subscription emit context where no
// loader is attached).
func (r *Resolver) ClaudeInstances(ctx context.Context) ([]*GraphQLInstance, error) {
	if r.svc == nil {
		return []*GraphQLInstance{}, nil
	}

	var instances []*Instance
	if r.loaders != nil {
		host := "local"
		result, err := r.loaders.InstancesByHost.Load(ctx, HostKey{HostID: host})()
		if err != nil {
			return nil, fmt.Errorf("claude-instance: loader: %w", err)
		}
		instances = result
	} else {
		var err error
		instances, err = r.svc.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("claude-instance: list: %w", err)
		}
	}

	out := make([]*GraphQLInstance, 0, len(instances))
	for _, inst := range instances {
		if proj := ProjectInstance(inst); proj != nil {
			out = append(out, proj)
		}
	}
	return out, nil
}
