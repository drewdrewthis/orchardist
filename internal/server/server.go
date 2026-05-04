// Package server hosts the orchard daemon's HTTP surface: the GraphQL
// endpoint and the /health probe. It is the deployment shell that runs
// the gqlgen-generated executable schema.
//
// Workstream A scope: GraphQL with the stub Health resolver, a /health
// JSON endpoint for cheap liveness checks, graceful shutdown on context
// cancellation.
//
// Workstream B-ps adds: optional ps provider attachment so resolvers
// can serve `host.processes` and the Process node fields. Subscriptions
// are not wired yet — Workstream C lights them up alongside the rest of
// the schema.
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
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// DefaultAddr is where the daemon listens. Hard-coded for v1; promote to
// config if multi-binding becomes a real need.
const DefaultAddr = "localhost:7777"

// Server wraps the http.Server plus the resolver root. One instance per
// daemon process.
type Server struct {
	addr      string
	startedAt time.Time
	logger    *slog.Logger
	httpSrv   *http.Server
	resolver  *resolvers.Resolver
	psProv    *ps.Provider
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithPSProvider attaches a ps provider to the server's resolver root.
// The server takes responsibility for the provider's lifecycle: Run()
// calls Start() before opening the listener and (implicitly) tears it
// down by cancelling its context on shutdown.
func WithPSProvider(p *ps.Provider) Option {
	return func(s *Server) { s.psProv = p }
}

// New constructs a Server bound to addr. The Resolver captures the start
// time so /health and the GraphQL `health` field report the same uptime.
func New(addr string, logger *slog.Logger, opts ...Option) *Server {
	if addr == "" {
		addr = DefaultAddr
	}
	if logger == nil {
		logger = slog.Default()
	}
	startedAt := time.Now()

	s := &Server{
		addr:      addr,
		startedAt: startedAt,
		logger:    logger,
		resolver:  resolvers.New(startedAt),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.psProv != nil {
		s.resolver.WithPS(s.psProv)
	}

	mux := http.NewServeMux()
	mux.Handle("/health", healthHandler(startedAt))
	mux.Handle("/graphql", graphqlHandler(s.resolver))
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Run starts the HTTP server and blocks until ctx is cancelled, then
// drains in-flight requests with a 5-second deadline. If a ps provider
// is attached, it is started (synchronous warm-up) before the listener
// opens so the first request is served from a populated cache.
func (s *Server) Run(ctx context.Context) error {
	if s.psProv != nil {
		if err := s.psProv.Start(ctx); err != nil {
			return fmt.Errorf("ps provider start: %w", err)
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

// graphqlHandler wires the gqlgen executable schema. POST + GET (for
// query strings) are both accepted; multipart and websocket transports
// stay deferred to Workstream C.
func graphqlHandler(r *resolvers.Resolver) http.Handler {
	cfg := gql.Config{Resolvers: r}
	srv := handler.New(gql.NewExecutableSchema(cfg))
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	return srv
}
