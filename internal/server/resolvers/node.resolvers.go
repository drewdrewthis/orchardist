package resolvers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	hostprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
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
	case strings.HasPrefix(id, "TmuxSession:"):
		return resolveTmuxSessionNode(ctx, r, id)
	case strings.HasPrefix(id, "TmuxPane:"):
		return resolveTmuxPaneNode(ctx, r, id)
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

// worktreeNodeValue mirrors `toGraphQLWorktree` in schema.resolvers.go.
// Kept here so node.resolvers.go is self-contained.
func worktreeNodeValue(w gitprovider.Worktree) *graphql1.Worktree {
	return &graphql1.Worktree{
		ID:     string(w.ID),
		Path:   w.Path,
		Branch: w.Branch,
		Head:   w.Head,
		Bare:   w.Bare,
	}
}
