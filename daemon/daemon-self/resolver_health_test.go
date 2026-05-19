// Tests for the daemon-self domain resolvers.
//
// T1: every typed field tested against a stubbed service.
// T3: assertions use concrete expected values.
package daemonself

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- stub implementations ---

// stubService satisfies DaemonSelfReader for tests.
type stubService struct {
	health    HealthSnapshot
	version   string
	schemaSDL string
	registry  NodeRegistry
}

func (s *stubService) Health() HealthSnapshot   { return s.health }
func (s *stubService) Version() string          { return s.version }
func (s *stubService) SchemaSDL() string        { return s.schemaSDL }
func (s *stubService) Registry() NodeRegistry   { return s.registry }

// stubNode is a minimal Node for registry tests.
type stubNode struct{ id string }

func (n stubNode) IsNode() {}

// stubRegistry satisfies NodeRegistry.
type stubRegistry struct {
	nodes map[string]Node
	err   error
}

func (r *stubRegistry) Resolve(_ context.Context, id string) (Node, error) {
	if r.err != nil {
		return nil, r.err
	}
	n, ok := r.nodes[id]
	if !ok {
		return nil, nil
	}
	return n, nil
}

// --- DaemonSelfService unit tests ---

func TestNew_DefaultsVersionToDev(t *testing.T) {
	svc := New(time.Now(), "", "sdl", nil)
	if svc.Version() != "dev" {
		t.Errorf("expected version 'dev', got %q", svc.Version())
	}
}

func TestHealth_StatusIsOk(t *testing.T) {
	svc := New(time.Now(), "1.0.0", "", nil)
	snap := svc.Health()
	if snap.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", snap.Status)
	}
}

func TestHealth_UptimeSIsNonNegative(t *testing.T) {
	// Started 2 seconds ago — uptime must be >= 2.
	startedAt := time.Now().Add(-2 * time.Second)
	svc := New(startedAt, "1.0.0", "", nil)
	snap := svc.Health()
	if snap.UptimeS < 2 {
		t.Errorf("expected uptimeS >= 2, got %d", snap.UptimeS)
	}
}

func TestSchemaSDL_ReturnsBakedContent(t *testing.T) {
	const expected = "type Query { health: Health! }"
	svc := New(time.Now(), "1.0.0", expected, nil)
	if got := svc.SchemaSDL(); got != expected {
		t.Errorf("expected schemaSDL %q, got %q", expected, got)
	}
}

// --- QueryResolver tests (T1) ---

func TestQueryResolver_Health_StatusField(t *testing.T) {
	svc := &stubService{health: HealthSnapshot{Status: "ok", UptimeS: 42}}
	r := NewResolver(svc)
	q := &QueryResolver{r: r}

	snap, err := q.Health(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Status != "ok" {
		t.Errorf("Health.status: expected 'ok', got %q", snap.Status)
	}
}

func TestQueryResolver_Health_UptimeSField(t *testing.T) {
	svc := &stubService{health: HealthSnapshot{Status: "ok", UptimeS: 99}}
	r := NewResolver(svc)
	q := &QueryResolver{r: r}

	snap, err := q.Health(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.UptimeS != 99 {
		t.Errorf("Health.uptimeS: expected 99, got %d", snap.UptimeS)
	}
}

func TestQueryResolver_Version_ReturnsBakedVersion(t *testing.T) {
	svc := &stubService{version: "2.3.1"}
	r := NewResolver(svc)
	q := &QueryResolver{r: r}

	v, err := q.Version(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "2.3.1" {
		t.Errorf("version: expected '2.3.1', got %q", v)
	}
}

func TestQueryResolver_SchemaSDL_ReturnsBakedSDL(t *testing.T) {
	const sdl = "extend type Query { health: Health! }"
	svc := &stubService{schemaSDL: sdl}
	r := NewResolver(svc)
	q := &QueryResolver{r: r}

	got, err := q.SchemaSDL(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sdl {
		t.Errorf("schemaSDL: expected %q, got %q", sdl, got)
	}
}

// --- QueryResolver.Node tests (T1) ---

func TestQueryResolver_Node_ReturnsNilWhenRegistryIsNil(t *testing.T) {
	svc := &stubService{registry: nil}
	r := NewResolver(svc)
	q := &QueryResolver{r: r}

	node, err := q.Node(context.Background(), "Host:some-machine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node != nil {
		t.Errorf("expected nil node when registry is nil, got %v", node)
	}
}

func TestQueryResolver_Node_ReturnsNilForUnknownID(t *testing.T) {
	reg := &stubRegistry{nodes: map[string]Node{}}
	svc := &stubService{registry: reg}
	r := NewResolver(svc)
	q := &QueryResolver{r: r}

	node, err := q.Node(context.Background(), "Host:unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node != nil {
		t.Errorf("expected nil for unknown id, got %v", node)
	}
}

func TestQueryResolver_Node_ReturnsMatchingNode(t *testing.T) {
	want := stubNode{id: "Host:abc123"}
	reg := &stubRegistry{nodes: map[string]Node{"Host:abc123": want}}
	svc := &stubService{registry: reg}
	r := NewResolver(svc)
	q := &QueryResolver{r: r}

	node, err := q.Node(context.Background(), "Host:abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := node.(stubNode)
	if !ok || got.id != want.id {
		t.Errorf("node: expected %v, got %v", want, node)
	}
}

func TestQueryResolver_Node_PropagatesRegistryError(t *testing.T) {
	sentinel := errors.New("registry failure")
	reg := &stubRegistry{err: sentinel}
	svc := &stubService{registry: reg}
	r := NewResolver(svc)
	q := &QueryResolver{r: r}

	_, err := q.Node(context.Background(), "Host:abc123")
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// --- HealthResolver field tests (T1) ---

func TestHealthResolver_Status(t *testing.T) {
	r := &Resolver{svc: &stubService{}}
	hr := &HealthResolver{r: r}
	snap := &HealthSnapshot{Status: "ok", UptimeS: 5}

	status, err := hr.Status(context.Background(), snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "ok" {
		t.Errorf("expected 'ok', got %q", status)
	}
}

func TestHealthResolver_UptimeS(t *testing.T) {
	r := &Resolver{svc: &stubService{}}
	hr := &HealthResolver{r: r}
	snap := &HealthSnapshot{Status: "ok", UptimeS: 77}

	uptimeS, err := hr.UptimeS(context.Background(), snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uptimeS != 77 {
		t.Errorf("expected 77, got %d", uptimeS)
	}
}
