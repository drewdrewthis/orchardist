// Package server hosts the orchard daemon's HTTP surface: the GraphQL
// endpoint and the /health probe. It is the deployment shell that runs
// the gqlgen-generated executable schema.
//
// Workstream A scope: GraphQL with the stub Health resolver, a /health
// JSON endpoint for cheap liveness checks, graceful shutdown on context
// cancellation. Subscriptions are not wired yet — Workstream C lights
// them up alongside the rest of the schema.
//
// Workstream B-host: the host Provider is constructed here and started
// in Run, so the resolver root has it ready before any GraphQL request
// arrives. Subsequent provider workstreams hang their constructors off
// New the same way.
//
// Workstream B-config: providers introduced by later workstreams are
// wired through the Option pattern (WithFoo(p)), so New keeps a tight
// signature as the provider set grows.
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
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// DefaultAddr is where the daemon listens. Hard-coded for v1; promote to
// config if multi-binding becomes a real need.
const DefaultAddr = "localhost:7777"

// Server wraps the http.Server plus the resolver root and provider set.
// One instance per daemon process.
type Server struct {
	addr      string
	startedAt time.Time
	logger    *slog.Logger
	httpSrv   *http.Server
	host      *host.Provider
}

// Option mutates a Server during construction. Used for wiring optional
// providers without bloating the New signature.
type Option func(*Server, *resolvers.Resolver)

// WithProjects wires a projects provider into the resolver.
func WithProjects(p resolvers.ProjectsLister) Option {
	return func(_ *Server, r *resolvers.Resolver) { r.WithProjects(p) }
}

// New constructs a Server bound to addr. The host provider is constructed
// here but not started — Run owns lifecycle so the poll loops are tied
// to the same ctx as the HTTP listener. Optional providers are wired
// via the With* options.
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

// Addr returns the configured listen address. Useful for tests using
// httptest or for callers that need to print the URL.
func (s *Server) Addr() string { return s.addr }

// HTTPHandler returns the underlying HTTP mux for tests that wrap the
// daemon in httptest.Server without binding a real port.
func (s *Server) HTTPHandler() http.Handler { return s.httpSrv.Handler }

// Run starts providers, the HTTP server, and blocks until ctx is
// cancelled, then drains in-flight requests with a 5-second deadline.
// Returns the underlying ListenAndServe error unless it is the expected
// http.ErrServerClosed.
func (s *Server) Run(ctx context.Context) error {
	if err := s.host.Start(ctx); err != nil {
		// Identity is load-bearing — fail fast so the operator sees it
		// rather than getting half-populated GraphQL responses.
		return fmt.Errorf("start host provider: %w", err)
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

// healthHandler reflects the server's uptime as JSON. Mirrors the
// GraphQL `health` field so callers can pick the cheaper transport for
// liveness probes.
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

// graphqlHandlerFor wires the gqlgen executable schema with an
// already-constructed Resolver root, so callers can inject providers
// before serving any requests. POST + GET (for query strings) are both
// accepted; multipart and websocket transports stay deferred to
// Workstream C.
func graphqlHandlerFor(res *resolvers.Resolver) http.Handler {
	cfg := gql.Config{Resolvers: res}
	srv := handler.New(gql.NewExecutableSchema(cfg))
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	return srv
}
