package resolvers

import (
	"context"
	"fmt"
	"strings"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

// subscribeNodeChanged opens the relevant provider's invalidation
// channel and re-emits the freshly-loaded node value on each fire.
// One channel per subscription; closes when ctx is cancelled or the
// upstream channel closes.
func subscribeNodeChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	switch {
	case strings.HasPrefix(id, "Host:"):
		return subscribeHostChanged(ctx, r, id)
	case strings.HasPrefix(id, "TmuxPane:"):
		return subscribePaneChanged(ctx, r, id)
	}
	if isProcessID(id) {
		return subscribeProcessChanged(ctx, r, id)
	}
	if strings.Contains(id, ":") {
		return subscribeWorktreeChanged(ctx, r, id)
	}
	return nil, fmt.Errorf("subscriptions not supported for node id %q", id)
}

func subscribeHostChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.HostProvider == nil {
		return nil, fmt.Errorf("host provider not configured")
	}
	machineID := strings.TrimPrefix(id, "Host:")
	src := r.HostProvider.Subscribe(ctx)
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
				if string(ev.Key) != machineID {
					continue
				}
				node, err := resolveHostNode(ctx, r, id)
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
	return out, nil
}

func subscribeWorktreeChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Git == nil {
		return nil, fmt.Errorf("git provider not configured")
	}
	src := r.Git.Subscribe(ctx)
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
				if string(ev.Key) != id {
					continue
				}
				node, err := resolveWorktreeNode(ctx, r, id)
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
				if ev.Key != pid {
					continue
				}
				node, err := resolveProcessNode(ctx, r, id)
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
	return out, nil
}

func subscribePaneChanged(ctx context.Context, r *Resolver, id string) (<-chan graphql1.Node, error) {
	if r.Tmux == nil {
		return nil, fmt.Errorf("tmux provider not configured")
	}
	src := r.Tmux.Panes().Subscribe(ctx)
	host, paneID, _ := splitTmuxPaneID(id)
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
				if string(ev.Key.Host) != host || ev.Key.PaneID != paneID {
					continue
				}
				node, err := resolveTmuxPaneNode(ctx, r, id)
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
	return out, nil
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
