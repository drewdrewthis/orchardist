package claudeaccount

import (
	"context"
	"errors"
	"time"
)

// ResolvedAccount is the wire-level projection of a ClaudeAccount node
// that this domain's resolvers return. It avoids importing the gqlgen
// generated types so the domain package stays independent of the
// generated code layer — the resolver wiring in daemon/resolvers does
// the final mapping into *graphql.ClaudeAccount.
//
// Cross-domain edges (host, instances) are represented as the stub types
// defined in service.go. Full resolution happens after the relevant
// agents land their own PRs (host-identity, claude-instance).
type ResolvedAccount struct {
	ID             string
	Email          string
	QuotaUsed      *float64
	QuotaCap       *float64
	QuotaEstimated bool
	QuotaResetsAt  *time.Time
	HostID         string
	// Instances is always empty in v1; claude-instance will populate.
	Instances []InstanceStub
}

// ClaudeAccountResolver provides field-level resolution for the
// ClaudeAccount GraphQL type (R6: one file per type).
//
// Field resolvers go through loaders per R3; no Snapshot() or full-clone
// in the hot path.
type ClaudeAccountResolver struct {
	svc     Service
	loaders *Loaders
}

// NewClaudeAccountResolver constructs the resolver from the domain service.
func NewClaudeAccountResolver(svc Service) *ClaudeAccountResolver {
	return &ClaudeAccountResolver{
		svc:     svc,
		loaders: NewLoaders(svc),
	}
}

// QueryClaudeAccounts resolves Query.claudeAccounts.
//
// Routes through the AccountsByHost loader (R3) rather than calling
// svc.List directly, so that parallel resolvers in the same request coalesce
// into a single underlying fetch (T5).
func (r *ClaudeAccountResolver) QueryClaudeAccounts(ctx context.Context, hostID string) ([]*ResolvedAccount, error) {
	thunk := r.loaders.AccountsByHost.Load(ctx, hostID)
	accounts, err := thunk()
	if err != nil {
		var notInstalled *ToolNotInstalledError
		if errors.As(err, &notInstalled) {
			// Surface as per-field GraphQL error (not daemon collapse).
			return nil, notInstalled
		}
		return nil, err
	}
	out := make([]*ResolvedAccount, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, projectAccount(a))
	}
	return out, nil
}

// GetClaudeAccount resolves Query.node(id) for ClaudeAccount nodes.
func (r *ClaudeAccountResolver) GetClaudeAccount(ctx context.Context, key AccountID) (*ResolvedAccount, error) {
	thunk := r.loaders.AccountByID.Load(ctx, key)
	acc, _, err := thunk()
	if err != nil {
		return nil, err
	}
	return projectAccount(acc), nil
}

// projectAccount maps the in-memory Account onto the wire-level
// ResolvedAccount. Pure function — no I/O (functional core per architecture).
func projectAccount(a Account) *ResolvedAccount {
	return &ResolvedAccount{
		ID:             a.ID.GraphQLID(),
		Email:          a.ID.Email,
		QuotaUsed:      a.QuotaUsed,
		QuotaCap:       a.QuotaCap,
		QuotaEstimated: a.QuotaEstimated,
		QuotaResetsAt:  a.QuotaResetsAt,
		HostID:         a.ID.HostID,
		Instances:      []InstanceStub{},
	}
}
