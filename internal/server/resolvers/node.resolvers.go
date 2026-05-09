package resolvers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
	claudeinstanceprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeinstance"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/contracts"
	ghprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	hostprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/hostservice"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

// resolveNode parses the id and dispatches to the right provider. The
// orchard schema does not require a uniform Node id prefix — each type
// documents its own format — so the dispatcher uses prefix detection
// plus a numeric-pid heuristic for Process. See schema.graphql.
//
// Returns (nil, nil) for well-formed-but-not-found ids per the
// schema's nullable Node return type. Errors are reserved for
// malformed ids or provider failures.
func resolveNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	switch {
	case strings.HasPrefix(id, "Host:"):
		return resolveHostNode(ctx, r, id)
	case strings.HasPrefix(id, "HostService:"):
		return resolveHostServiceNode(ctx, r, id)
	case strings.HasPrefix(id, "Conversation:"):
		return resolveConversationNode(ctx, r, id)
	case strings.HasPrefix(id, "ClaudeAccount:"):
		return resolveClaudeAccountNode(ctx, r, id)
	case strings.HasPrefix(id, "ClaudeInstance:"):
		return resolveClaudeInstanceNode(ctx, r, id)
	case strings.HasPrefix(id, "Contract:"):
		return resolveContractNode(ctx, r, id)
	case strings.HasPrefix(id, "TmuxServer:"):
		return resolveTmuxServerNode(ctx, r, id)
	case strings.HasPrefix(id, "TmuxSession:"):
		return resolveTmuxSessionNode(ctx, r, id)
	case strings.HasPrefix(id, "TmuxWindow:"):
		return resolveTmuxWindowNode(ctx, r, id)
	case strings.HasPrefix(id, "TmuxPane:"):
		return resolveTmuxPaneNode(ctx, r, id)
	case strings.HasPrefix(id, "TmuxClient:"):
		return resolveTmuxClientNode(ctx, r, id)
	case strings.HasPrefix(id, "PullRequest:"):
		return resolvePullRequestNode(ctx, r, id)
	case strings.HasPrefix(id, "Issue:"):
		return resolveIssueNode(ctx, r, id)
	case strings.HasPrefix(id, "WorkflowRun:"):
		return resolveWorkflowRunNode(ctx, r, id)
	}
	if isProcessID(id) {
		return resolveProcessNode(ctx, r, id)
	}
	// Two un-prefixed candidates remain: Project ("alpha") and
	// Worktree ("alpha:main"). Worktree ids always contain ":";
	// Project ids never do (they are slugs of the project name).
	if strings.Contains(id, ":") {
		return resolveWorktreeNode(ctx, r, id)
	}
	return resolveProjectNode(ctx, r, id)
}

// isProcessID returns true when id parses as "<host>:<positive int>".
// The host segment can contain its own colons (a machineid is
// formatted like "FE801CDF-..."), so we only inspect the trailing
// token.
func isProcessID(id string) bool {
	idx := strings.LastIndex(id, ":")
	if idx <= 0 || idx == len(id)-1 {
		return false
	}
	n, err := strconv.Atoi(id[idx+1:])
	return err == nil && n > 0
}

func resolveHostNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.HostProvider == nil {
		return graphql1.Host{ID: id}, nil
	}
	machineID := strings.TrimPrefix(id, "Host:")
	h, _, err := r.HostProvider.Get(ctx, hostprovider.HostID(machineID))
	if err != nil {
		// Unknown host ids surface as "not found" per the nullable schema.
		if strings.Contains(err.Error(), "unknown host key") {
			return nil, nil
		}
		return nil, err
	}
	return *h, nil
}

func resolveProjectNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.ProjectsProvider == nil {
		return nil, fmt.Errorf("projects provider not configured")
	}
	rows, err := r.ProjectsProvider.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if string(row.ID) == id {
			return graphql1.Project{
				ID:        string(row.ID),
				Directory: row.Directory,
				Name:      row.Name,
			}, nil
		}
	}
	return nil, nil
}

func resolveWorktreeNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.Git == nil {
		return nil, fmt.Errorf("git provider not configured")
	}
	w, _, err := r.Git.Get(ctx, gitprovider.WorktreeID(id))
	if err != nil {
		// Treat "no such worktree" / malformed as not-found.
		if strings.Contains(err.Error(), "not exist") || strings.Contains(err.Error(), "malformed") {
			return nil, nil
		}
		return nil, err
	}
	return *worktreeNodeValue(w), nil
}

func resolveProcessNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.PS == nil {
		return nil, fmt.Errorf("ps provider not configured")
	}
	pid, err := psprovider.ParseProcessID(id)
	if err != nil {
		return nil, nil
	}
	p, _, getErr := r.PS.Get(ctx, pid)
	if getErr != nil {
		return nil, nil
	}
	return *projectProcessFromCache(&p, pid.Host), nil
}

func resolveTmuxSessionNode(_ context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, name, ok := splitTmuxSessionID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux session id %q", id)
	}
	s, found := findTmuxSession(r.Tmux, host, name)
	if !found {
		return nil, nil
	}
	return *projectTmuxSession(s), nil
}

func resolveTmuxPaneNode(_ context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, paneID, ok := splitTmuxPaneID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux pane id %q", id)
	}
	p, found := findTmuxPane(r.Tmux, host, paneID)
	if !found {
		return nil, nil
	}
	return *projectTmuxPane(p), nil
}

func resolveTmuxServerNode(_ context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, socketPath, ok := splitTmuxServerID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux server id %q", id)
	}
	if string(r.Tmux.Host()) != host {
		return nil, nil
	}
	info := r.Tmux.Server()
	if info.SocketPath != socketPath {
		return nil, nil
	}
	return *projectServer(r.Tmux.Host(), info), nil
}

func resolveTmuxWindowNode(_ context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, session, idx, ok := splitTmuxWindowID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux window id %q", id)
	}
	if string(r.Tmux.Host()) != host {
		return nil, nil
	}
	w, found := findTmuxWindow(r.Tmux, host, session, idx)
	if !found {
		return nil, nil
	}
	return *projectWindowAtomic(w), nil
}

func resolveTmuxClientNode(_ context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, name, ok := splitTmuxClientID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux client id %q", id)
	}
	if string(r.Tmux.Host()) != host {
		return nil, nil
	}
	c, found := findTmuxClient(r.Tmux, host, name)
	if !found {
		return nil, nil
	}
	return *projectClientAtomic(c), nil
}

func resolveConversationNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.ClaudeProjects == nil {
		return nil, fmt.Errorf("claudeprojects provider not configured")
	}
	uuid := strings.TrimPrefix(id, "Conversation:")
	if uuid == "" {
		return nil, nil
	}
	keys, err := r.ClaudeProjects.Keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if k.SessionUUID != uuid {
			continue
		}
		c, _, gerr := r.ClaudeProjects.Get(ctx, k)
		if gerr != nil {
			return nil, nil
		}
		return *r.ClaudeProjects.ToGraphQL(c), nil
	}
	return nil, nil
}

func resolveClaudeAccountNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.ClaudeAccount == nil {
		return nil, fmt.Errorf("claudeaccount provider not configured")
	}
	host, email, ok := splitClaudeAccountID(id)
	if !ok {
		return nil, fmt.Errorf("malformed claude account id %q", id)
	}
	a, _, err := r.ClaudeAccount.Get(ctx, claudeaccount.AccountID{HostID: host, Email: email})
	if err != nil {
		return nil, nil
	}
	return *r.ClaudeAccount.ToGraphQL(a), nil
}

func resolveClaudeInstanceNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.ClaudeInstance == nil {
		return nil, fmt.Errorf("claudeinstance provider not configured")
	}
	host, pid, ok := parseClaudeInstanceID(id)
	if !ok {
		// Session-keyed id — fall through to a List+match.
		return findClaudeInstanceByID(ctx, r.ClaudeInstance, id)
	}
	inst, _, err := r.ClaudeInstance.Get(ctx, claudeinstanceprovider.InstanceID{HostID: host, ClaudePid: pid})
	if err != nil || inst == nil {
		return nil, nil
	}
	return *inst, nil
}

func resolveHostServiceNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.HostServiceProvider == nil {
		return nil, fmt.Errorf("hostservice provider not configured")
	}
	hostID, name, ok := splitHostServiceID(id)
	if !ok {
		return nil, fmt.Errorf("malformed host service id %q", id)
	}
	snap, _, err := r.HostServiceProvider.Get(ctx, hostservice.MakeID(hostID, name))
	if err != nil {
		return nil, nil
	}
	return *hostServiceFromSnapshot(snap), nil
}

func resolveContractNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.ContractsProvider == nil {
		return nil, fmt.Errorf("contracts provider not configured")
	}
	key := contracts.ContractID(strings.TrimPrefix(id, "Contract:"))
	c, _, err := r.ContractsProvider.Get(ctx, key)
	if err != nil || c == nil {
		return nil, nil
	}
	return *c, nil
}

func resolvePullRequestNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	owner, name, number, ok := splitGHNodeID(id, "PullRequest:")
	if !ok {
		return nil, fmt.Errorf("malformed pull request id %q", id)
	}
	pr, err := r.GH.GetPullRequest(ctx, ghprovider.PullRequestKey{Owner: owner, Name: name, Number: number})
	if err != nil {
		return nil, nil
	}
	return *toGraphQLPullRequest(pr), nil
}

func resolveIssueNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	owner, name, number, ok := splitGHNodeID(id, "Issue:")
	if !ok {
		return nil, fmt.Errorf("malformed issue id %q", id)
	}
	iss, err := r.GH.GetIssue(ctx, ghprovider.IssueKey{Owner: owner, Name: name, Number: number})
	if err != nil {
		return nil, nil
	}
	return *toGraphQLIssue(iss), nil
}

func resolveWorkflowRunNode(ctx context.Context, r *Resolver, id string) (graphql1.Node, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	owner, name, runID, ok := splitGHNodeID(id, "WorkflowRun:")
	if !ok {
		return nil, fmt.Errorf("malformed workflow run id %q", id)
	}
	run, err := r.GH.GetWorkflowRun(ctx, ghprovider.WorkflowRunKey{Owner: owner, Name: name, RunID: int64(runID)})
	if err != nil {
		return nil, nil
	}
	return *toGraphQLWorkflowRun(run), nil
}

// splitTmuxSessionID parses "TmuxSession:<host>:<name>". Returns
// ok=false on malformed input.
func splitTmuxSessionID(id string) (host, name string, ok bool) {
	const prefix = "TmuxSession:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", false
	}
	rest := id[len(prefix):]
	idx := strings.Index(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// splitTmuxPaneID parses "TmuxPane:<host>:<paneId>".
func splitTmuxPaneID(id string) (host, paneID string, ok bool) {
	const prefix = "TmuxPane:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", false
	}
	rest := id[len(prefix):]
	idx := strings.Index(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// splitTmuxServerID parses "TmuxServer:<host>:<socketPath>". The socket
// path can contain colons (it's an absolute path) so we only split on
// the first separator after the prefix.
func splitTmuxServerID(id string) (host, socketPath string, ok bool) {
	const prefix = "TmuxServer:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", false
	}
	rest := id[len(prefix):]
	idx := strings.Index(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// splitTmuxWindowID parses "TmuxWindow:<host>:<sessionName>:<index>".
func splitTmuxWindowID(id string) (host, session string, index int, ok bool) {
	const prefix = "TmuxWindow:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", 0, false
	}
	rest := id[len(prefix):]
	hostIdx := strings.Index(rest, ":")
	if hostIdx <= 0 {
		return "", "", 0, false
	}
	host = rest[:hostIdx]
	tail := rest[hostIdx+1:]
	idxIdx := strings.LastIndex(tail, ":")
	if idxIdx <= 0 || idxIdx == len(tail)-1 {
		return "", "", 0, false
	}
	session = tail[:idxIdx]
	n, err := strconv.Atoi(tail[idxIdx+1:])
	if err != nil {
		return "", "", 0, false
	}
	return host, session, n, true
}

// splitTmuxClientID parses "TmuxClient:<host>:<clientName>".
func splitTmuxClientID(id string) (host, name string, ok bool) {
	const prefix = "TmuxClient:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", false
	}
	rest := id[len(prefix):]
	idx := strings.Index(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// splitClaudeAccountID parses "ClaudeAccount:<host>:<email>".
func splitClaudeAccountID(id string) (host, email string, ok bool) {
	const prefix = "ClaudeAccount:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", false
	}
	rest := id[len(prefix):]
	idx := strings.Index(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// splitHostServiceID parses "HostService:<host>:<name>".
func splitHostServiceID(id string) (host, name string, ok bool) {
	const prefix = "HostService:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", false
	}
	rest := id[len(prefix):]
	idx := strings.Index(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// parseClaudeInstanceID parses "ClaudeInstance:<host>:<pid>" when the
// trailing segment is a positive integer. Session-keyed ids
// ("ClaudeInstance:<host>:session-<name>") return ok=false so callers
// can fall back to a List scan.
func parseClaudeInstanceID(id string) (host string, pid int, ok bool) {
	const prefix = "ClaudeInstance:"
	if !strings.HasPrefix(id, prefix) {
		return "", 0, false
	}
	rest := id[len(prefix):]
	idx := strings.LastIndex(rest, ":")
	if idx <= 0 || idx == len(rest)-1 {
		return "", 0, false
	}
	tail := rest[idx+1:]
	n, err := strconv.Atoi(tail)
	if err != nil || n <= 0 {
		return rest[:idx], 0, false
	}
	return rest[:idx], n, true
}

// findClaudeInstanceByID is the fallback for session-keyed claude
// instance ids — it scans the provider's cache for an exact id match.
func findClaudeInstanceByID(ctx context.Context, p *claudeinstanceprovider.Provider, id string) (graphql1.Node, error) {
	rows, err := p.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, inst := range rows {
		if inst != nil && inst.ID == id {
			return *inst, nil
		}
	}
	return nil, nil
}

// splitGHNodeID parses gh-style ids like "PullRequest:owner/repo#42".
func splitGHNodeID(id, prefix string) (owner, name string, number int, ok bool) {
	if !strings.HasPrefix(id, prefix) {
		return "", "", 0, false
	}
	rest := id[len(prefix):]
	hashIdx := strings.LastIndex(rest, "#")
	if hashIdx <= 0 || hashIdx == len(rest)-1 {
		return "", "", 0, false
	}
	repo := rest[:hashIdx]
	slashIdx := strings.Index(repo, "/")
	if slashIdx <= 0 || slashIdx == len(repo)-1 {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(rest[hashIdx+1:])
	if err != nil || n <= 0 {
		return "", "", 0, false
	}
	return repo[:slashIdx], repo[slashIdx+1:], n, true
}

// worktreeNodeValue delegates to `toGraphQLWorktree` so all Worktree
// projection happens in one place. Federation work (Workstream F) only
// needs to update toGraphQLWorktree to populate Host from a real source.
func worktreeNodeValue(w gitprovider.Worktree) *graphql1.Worktree {
	return toGraphQLWorktree(w)
}

// _ keeps imports stable when not all helpers are referenced (build-time
// hygiene; the package's tests reference every helper).
var _ = claudeprojects.ConversationID{}
