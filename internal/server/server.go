// Package server hosts the orchard daemon's HTTP surface: the GraphQL
// endpoint and the /health probe. It is the deployment shell that runs
// the gqlgen-generated executable schema.
//
// Workstream A: GraphQL with the stub Health resolver, /health, graceful shutdown.
// Workstream B-host: host Provider constructed and started in Run.
// Workstream B-config: Option pattern (WithFoo) wires optional providers.
// Workstream B-git: WithGit wires the git provider.
// Workstream B-ps: WithPS attaches a ps provider; Run() starts it.
// Workstream B-tmux: WithTmux attaches a tmux provider; Run() starts it
// + the fsnotify-backed watcher.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// DefaultAddr is where the daemon listens.
const DefaultAddr = "localhost:7777"

// Server wraps the http.Server plus the resolver root and provider set.
type Server struct {
	addr      string
	startedAt time.Time
	logger    *slog.Logger
	httpSrv   *http.Server
	host      *host.Provider
	resolver  *resolvers.Resolver
	psProv    *ps.Provider
	tmuxProv  *tmux.Provider
}

// Option mutates a Server during construction.
type Option func(*Server, *resolvers.Resolver)

// WithProjects wires a projects provider into the resolver.
func WithProjects(p resolvers.ProjectsLister) Option {
	return func(_ *Server, r *resolvers.Resolver) { r.WithProjects(p) }
}

// WithGit wires a git provider into the resolver.
func WithGit(g *gitprovider.Provider) Option {
	return func(_ *Server, r *resolvers.Resolver) { r.WithGit(g) }
}

// WithPS attaches a ps provider; Run() calls Start() before the listener opens.
func WithPS(p *ps.Provider) Option {
	return func(s *Server, r *resolvers.Resolver) {
		s.psProv = p
		r.WithPS(p)
	}
}

// WithTmux attaches a tmux provider; Run() calls Start()+StartWatcher.
func WithTmux(p *tmux.Provider) Option {
	return func(s *Server, r *resolvers.Resolver) {
		s.tmuxProv = p
		r.WithTmux(p)
	}
}

// New constructs a Server bound to addr.
func New(addr string, logger *slog.Logger, opts ...Option) *Server {
	if addr == "" {
		addr = DefaultAddr
	}
	if logger == nil {
		logger = slog.Default()
	}
	startedAt := time.Now()

	hostProvider := host.New()
	res := resolvers.New(startedAt).WithHost(hostProvider)

	srv := &Server{
		addr:      addr,
		startedAt: startedAt,
		logger:    logger,
		host:      hostProvider,
		resolver:  res,
	}
	for _, opt := range opts {
		opt(srv, res)
	}

	mux := http.NewServeMux()
	mux.Handle("/health", healthHandler(startedAt))
	mux.Handle("/graphql", graphqlHandlerFor(res))

	srv.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return srv
}

// Addr returns the configured listen address.
func (s *Server) Addr() string { return s.addr }

// HTTPHandler returns the underlying HTTP mux for tests.
func (s *Server) HTTPHandler() http.Handler { return s.httpSrv.Handler }

// Resolver returns the resolver root the server was built with.
func (s *Server) Resolver() *resolvers.Resolver { return s.resolver }

// GraphQLHandler returns a fresh handler bound to the server's resolver.
func (s *Server) GraphQLHandler() http.Handler {
	return graphqlHandlerFor(s.resolver)
}

// Run starts providers, the HTTP server, and blocks until ctx is
// cancelled, then drains in-flight requests with a 5-second deadline.
func (s *Server) Run(ctx context.Context) error {
	if err := s.host.Start(ctx); err != nil {
		return fmt.Errorf("start host provider: %w", err)
	}
	if s.psProv != nil {
		if err := s.psProv.Start(ctx); err != nil {
			return fmt.Errorf("ps provider start: %w", err)
		}
	}
	if s.tmuxProv != nil {
		if err := s.tmuxProv.Start(ctx); err != nil {
			return fmt.Errorf("tmux provider start: %w", err)
		}
		if err := tmux.StartWatcher(ctx, s.tmuxProv, s.logger); err != nil {
			s.logger.Warn("tmux watcher start failed; continuing poll-only", "err", err)
		}
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("orchard daemon listening", "addr", s.addr)
		errCh <- s.httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		s.logger.Info("orchard daemon stopped")
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// healthHandler reflects the server's uptime as JSON.
func healthHandler(startedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uptime := int64(time.Since(startedAt).Round(time.Second).Seconds())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"uptimeS": uptime,
		})
	}
}

// graphqlHandlerFor wires the gqlgen executable schema with an already-constructed Resolver root.
func graphqlHandlerFor(res *resolvers.Resolver) http.Handler {
	cfg := gql.Config{Resolvers: res}
	srv := handler.New(gql.NewExecutableSchema(cfg))
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	return srv
}
