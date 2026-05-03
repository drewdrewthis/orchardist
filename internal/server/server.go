// Package server hosts the orchard daemon's HTTP surface: the GraphQL
// endpoint and the /health probe. It is the deployment shell that runs
// the gqlgen-generated executable schema.
//
// Workstream A scope: GraphQL with the stub Health resolver, a /health
// JSON endpoint for cheap liveness checks, graceful shutdown on context
// cancellation. Subscriptions are not wired yet — Workstream C lights
// them up alongside the rest of the schema.
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
}

// New constructs a Server bound to addr. The Resolver captures the start
// time so /health and the GraphQL `health` field report the same uptime.
func New(addr string, logger *slog.Logger) *Server {
	if addr == "" {
		addr = DefaultAddr
	}
	if logger == nil {
		logger = slog.Default()
	}
	startedAt := time.Now()

	mux := http.NewServeMux()
	mux.Handle("/health", healthHandler(startedAt))
	mux.Handle("/graphql", graphqlHandler(startedAt))

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return &Server{
		addr:      addr,
		startedAt: startedAt,
		logger:    logger,
		httpSrv:   httpSrv,
	}
}

// Run starts the HTTP server and blocks until ctx is cancelled, then
// drains in-flight requests with a 5-second deadline. Returns the
// underlying ListenAndServe error unless it is the expected
// http.ErrServerClosed.
func (s *Server) Run(ctx context.Context) error {
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
func graphqlHandler(startedAt time.Time) http.Handler {
	cfg := gql.Config{Resolvers: resolvers.New(startedAt)}
	srv := handler.New(gql.NewExecutableSchema(cfg))
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	return srv
}
