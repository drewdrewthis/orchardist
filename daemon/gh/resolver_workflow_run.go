// resolver_workflow_run.go — resolvers for the WorkflowRun GraphQL type.
//
// R6: ONE file per GraphQL type.
// S16a: typed core — Query.workflowRuns, WorkflowRun.* field resolvers.
package gh

import "context"

// WorkflowRunResolver provides the resolver bodies for the WorkflowRun type
// and for Query.workflowRuns.
type WorkflowRunResolver struct {
	Svc    Service
	Loader *WorkflowRunLoader
}

// NewWorkflowRunResolver constructs a WorkflowRunResolver.
func NewWorkflowRunResolver(svc Service) *WorkflowRunResolver {
	return &WorkflowRunResolver{
		Svc:    svc,
		Loader: NewWorkflowRunLoader(svc),
	}
}

// QueryWorkflowRuns implements Query.workflowRuns(repo).
func (r *WorkflowRunResolver) QueryWorkflowRuns(ctx context.Context, repo string) ([]WorkflowRun, error) {
	if r.Svc == nil {
		return nil, ErrGHNotConfigured
	}
	owner, name, err := SplitRepo(repo)
	if err != nil {
		return nil, err
	}
	return r.Svc.ListWorkflowRuns(ctx, owner, name)
}

// Load fetches a single WorkflowRun via the loader (R3, O1 ByKey axis).
func (r *WorkflowRunResolver) Load(ctx context.Context, key WorkflowRunKey) (WorkflowRun, error) {
	if r.Loader != nil {
		return r.Loader.Load(ctx, key)
	}
	return r.Svc.GetWorkflowRun(ctx, key)
}
