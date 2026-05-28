// Package resolvers — subscription dispatch.
//
// Subscription.nodeChanged opens the owning provider's invalidation
// channel and re-emits the freshly-loaded node value on each fire.
// Dispatch is by id-prefix; one channel per subscription; the channel
// closes when ctx is cancelled or the upstream channel closes.
package resolvers

import (
	"context"
	"fmt"
	"strings"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	hostprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/hostservice"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	tmuxprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// subscribeNodeChanged opens the owning provider's invalidation
// channel and re-emits the freshly-loaded node value on each fire.
// Dispatch is by id-prefix; the briefing's id format is
// `<NodeType>:<host-or-scope>:<...>`. Unknown prefixes raise a
// GraphQL error and complete without hanging.
func subscribeNodeChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	switch {
	case strings.HasPrefix(id, "Host:"):
		return subscribeHostChanged(ctx, r, id)
	case strings.HasPrefix(id, "HostService:"):
		return subscribeHostServiceChanged(ctx, r, id)
	case strings.HasPrefix(id, "Conversation:"):
		return subscribeConversationChanged(ctx, r, id)
	case strings.HasPrefix(id, "ClaudeAccount:"):
		return subscribeClaudeAccountChanged(ctx, r, id)
	case strings.HasPrefix(id, "ClaudeInstance:"):
		return subscribeClaudeInstanceChanged(ctx, r, id)
	case strings.HasPrefix(id, "TmuxServer:"):
		return subscribeTmuxServerChanged(ctx, r, id)
	case strings.HasPrefix(id, "TmuxSession:"):
		return subscribeTmuxSessionChanged(ctx, r, id)
	case strings.HasPrefix(id, "TmuxWindow:"):
		return subscribeTmuxWindowChanged(ctx, r, id)
	case strings.HasPrefix(id, "TmuxPane:"):
		return subscribePaneChanged(ctx, r, id)
	case strings.HasPrefix(id, "TmuxClient:"):
		return subscribeTmuxClientChanged(ctx, r, id)
	case strings.HasPrefix(id, "PullRequest:"),
		strings.HasPrefix(id, "Issue:"),
		strings.HasPrefix(id, "WorkflowRun:"):
		return subscribeGHChanged(ctx, r, id)
	case strings.HasPrefix(id, "Worktree:"):
		// Briefing aliases — the Worktree id is unprefixed in the
		// schema (`<project>:<name>`) but accept the prefixed form so
		// callers using the briefing's mapping still work.
		return subscribeWorktreeChanged(ctx, r, strings.TrimPrefix(id, "Worktree:"))
	case strings.HasPrefix(id, "Project:"):
		return subscribeProjectChanged(ctx, r, strings.TrimPrefix(id, "Project:"))
	}
	if isProcessID(id) {
		return subscribeProcessChanged(ctx, r, id)
	}
	if isWorktreeID(id) {
		return subscribeWorktreeChanged(ctx, r, id)
	}
	if id != "" && !strings.Contains(id, ":") {
		return subscribeProjectChanged(ctx, r, id)
	}
	prefix := id
	if i := strings.Index(id, ":"); i > 0 {
		prefix = id[:i]
	}
	return nil, fmt.Errorf("nodeChanged: unknown id prefix %q", prefix)
}

// isWorktreeID returns true for the schema's un-prefixed Worktree id
// shape (`<projectSlug>:<worktreeName>`). The slug never contains
// colons (Project ids forbid them), so a single-colon id with simple
// segments is the worktree shape.
func isWorktreeID(id string) bool {
	idx := strings.Index(id, ":")
	if idx <= 0 || idx == len(id)-1 {
		return false
	}
	// Must be exactly one ":" — anything richer (Foo:bar:baz) is a
	// typed prefix the caller spelled wrong.
	return strings.Count(id, ":") == 1
}

func subscribeHostChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.HostProvider == nil {
		return nil, fmt.Errorf("host provider not configured")
	}
	machineID := strings.TrimPrefix(id, "Host:")
	src := r.HostProvider.Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[hostprovider.HostID]) bool {
		return string(ev.Key) == machineID
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveHostNode(c, r, id)
	}), nil
}

func subscribeHostServiceChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.HostServiceProvider == nil {
		return nil, fmt.Errorf("hostservice provider not configured")
	}
	hostID, name, ok := splitHostServiceID(id)
	if !ok {
		return nil, fmt.Errorf("malformed host service id %q", id)
	}
	wantKey := hostservice.MakeID(hostID, name)
	src := r.HostServiceProvider.Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[hostservice.HostServiceID]) bool {
		return ev.Key == wantKey
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveHostServiceNode(c, r, id)
	}), nil
}

func subscribeConversationChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.ClaudeProjects == nil {
		return nil, fmt.Errorf("claudeprojects provider not configured")
	}
	uuid := strings.TrimPrefix(id, "Conversation:")
	src := r.ClaudeProjects.Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[claudeprojects.ConversationID]) bool {
		return ev.Key.SessionUUID == uuid
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveConversationNode(c, r, id)
	}), nil
}

func subscribeClaudeAccountChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.ClaudeAccount == nil {
		return nil, fmt.Errorf("claudeaccount provider not configured")
	}
	host, email, ok := splitClaudeAccountID(id)
	if !ok {
		return nil, fmt.Errorf("malformed claude account id %q", id)
	}
	wantKey := claudeaccount.AccountID{HostID: host, Email: email}
	src := r.ClaudeAccount.Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[claudeaccount.AccountID]) bool {
		return ev.Key == wantKey
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveClaudeAccountNode(c, r, id)
	}), nil
}

func subscribeClaudeInstanceChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("claudeinstance: tmux provider not configured")
	}
	// ADR-022 Phase 5: ClaudeInstance is a view over panes. Subscribe to the
	// Tmux panes invalidation channel and re-resolve on every pane change.
	src := r.Tmux.Panes().Subscribe(ctx)
	return relayInvalidations(ctx, src, func(_ adapter.InvalidationEvent[tmuxprovider.PaneKey]) bool {
		return true
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveClaudeInstanceNode(c, r, id)
	}), nil
}

func subscribeTmuxServerChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	// Tmux server changes ride the sessions firehose — every poll tick
	// touches the same daemon, so a new session event is a sufficient
	// signal that the server's alive/pid/socket may have moved.
	src := r.Tmux.Sessions().Subscribe(ctx)
	return relayInvalidations(ctx, src, func(_ adapter.InvalidationEvent[tmuxprovider.SessionKey]) bool {
		return true
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveTmuxServerNode(c, r, id)
	}), nil
}

func subscribeTmuxSessionChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, name, ok := splitTmuxSessionID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux session id %q", id)
	}
	src := r.Tmux.Sessions().Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[tmuxprovider.SessionKey]) bool {
		return string(ev.Key.Host) == host && ev.Key.Name == name
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveTmuxSessionNode(c, r, id)
	}), nil
}

func subscribeTmuxWindowChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, session, idx, ok := splitTmuxWindowID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux window id %q", id)
	}
	src := r.Tmux.Windows().Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[tmuxprovider.WindowKey]) bool {
		return string(ev.Key.Host) == host && ev.Key.Session == session && ev.Key.Index == idx
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveTmuxWindowNode(c, r, id)
	}), nil
}

func subscribePaneChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, paneID, ok := splitTmuxPaneID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux pane id %q", id)
	}
	src := r.Tmux.Panes().Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[tmuxprovider.PaneKey]) bool {
		return string(ev.Key.Host) == host && ev.Key.PaneID == paneID
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveTmuxPaneNode(c, r, id)
	}), nil
}

func subscribeTmuxClientChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	host, name, ok := splitTmuxClientID(id)
	if !ok {
		return nil, fmt.Errorf("malformed tmux client id %q", id)
	}
	src := r.Tmux.Clients().Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[tmuxprovider.ClientKey]) bool {
		return string(ev.Key.Host) == host && ev.Key.ClientName == name
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveTmuxClientNode(c, r, id)
	}), nil
}

func subscribeGHChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.GH == nil {
		return nil, errGHNotConfigured
	}
	src := r.GH.Subscribe(ctx)
	resolve := func(c context.Context) (graphql1.Node, error) {
		switch {
		case strings.HasPrefix(id, "PullRequest:"):
			return resolvePullRequestNode(c, r, id)
		case strings.HasPrefix(id, "Issue:"):
			return resolveIssueNode(c, r, id)
		case strings.HasPrefix(id, "WorkflowRun:"):
			return resolveWorkflowRunNode(c, r, id)
		}
		return nil, nil
	}
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[string]) bool {
		return ev.Key == id
	}, resolve), nil
}

func subscribeWorktreeChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Git == nil {
		return nil, fmt.Errorf("git provider not configured")
	}
	src := r.Git.Subscribe(ctx)
	wantKey := gitprovider.WorktreeID(id)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[gitprovider.WorktreeID]) bool {
		return ev.Key == wantKey
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveWorktreeNode(c, r, id)
	}), nil
}

func subscribeProjectChanged(ctx context.Context, _ *Resolver, _ string) (<-chan graphql1.Node, error) {
	// Projects are a config-driven static set; the config provider does
	// not currently expose an invalidation channel through Resolver.
	// Returning a closed channel keeps subscribers from hanging while
	// signalling "no events". When the config provider is wired in, this
	// is the integration point.
	out := make(chan graphql1.Node)
	close(out)
	go func() {
		<-ctx.Done()
	}()
	return out, nil
}

func subscribeProcessChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.PS == nil {
		return nil, fmt.Errorf("ps provider not configured")
	}
	pid, err := psprovider.ParseProcessID(id)
	if err != nil {
		return nil, err
	}
	src := r.PS.Subscribe(ctx)
	return relayInvalidations(ctx, src, func(ev adapter.InvalidationEvent[psprovider.ProcessID]) bool {
		return ev.Key == pid
	}, func(c context.Context) (graphql1.Node, error) {
		return resolveProcessNode(c, r, id)
	}), nil
}

// relayInvalidations is the generic channel pump shared by every
// subscribe* handler: it filters incoming InvalidationEvents and, on
// each match, resolves a fresh Node value through the supplied resolver.
// One goroutine per subscription; closes when ctx is cancelled or the
// upstream channel closes.
func relayInvalidations[K comparable](
	ctx context.Context,
	src <-chan adapter.InvalidationEvent[K],
	match func(adapter.InvalidationEvent[K]) bool,
	resolve func(context.Context) (graphql1.Node, error),
) <-chan graphql1.Node {
	out := make(chan graphql1.Node, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-src:
				if !ok {
					return
				}
				if !match(ev) {
					continue
				}
				node, err := resolve(ctx)
				if err != nil || node == nil {
					continue
				}
				select {
				case out <- node:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// subscribeProcesses pushes a snapshot of every cached process whenever
// the ps provider invalidates.
func subscribeProcesses(ctx context.Context, r *Resolver) (<-chan []*graphql1.Process, error) {
	if r.PS == nil {
		return nil, fmt.Errorf("ps provider not configured")
	}
	src := r.PS.Subscribe(ctx)
	hostID := r.PS.HostID()
	out := make(chan []*graphql1.Process, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-src:
				if !ok {
					return
				}
				snap := r.PS.List()
				procs := make([]*graphql1.Process, 0, len(snap))
				for i := range snap {
					procs = append(procs, projectProcessFromCache(&snap[i], hostID))
				}
				select {
				case out <- procs:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}
