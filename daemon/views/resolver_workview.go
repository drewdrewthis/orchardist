// resolver_workview.go — thin GraphQL resolver for the WorkView type.
//
// Per R6, this file owns exactly one GraphQL type: WorkView (plus the
// Query.workView entry-point). All join logic lives in Service.GetWorkView;
// this file is a projection shim only.
//
// Per S14, the WorkView resolver DELEGATES to per-domain services — it never
// re-implements a join. O2 lazy field resolution is guaranteed by gqlgen:
// only the fields the client selects are resolved.

package views

import (
	"context"

	graphql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// WorkViewResolver is the thin GraphQL resolver for the WorkView composite
// type. Embed one in your aggregate resolver and wire the service.
type WorkViewResolver struct {
	svc WorkViewService
}

// NewWorkViewResolver constructs a WorkViewResolver backed by the given
// WorkViewService.
func NewWorkViewResolver(svc WorkViewService) *WorkViewResolver {
	return &WorkViewResolver{svc: svc}
}

// WorkView is the Query.workView resolver. Delegates entirely to Service.GetWorkView
// per S14 — no join logic here.
func (r *WorkViewResolver) WorkView(ctx context.Context) (*graphql.WorkView, error) {
	return r.svc.GetWorkView(ctx)
}
