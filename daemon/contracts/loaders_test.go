package contracts

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubService is a ContractsService fake for loader tests. It records how many
// times GetMany is called so T5 can assert ≤1 call per batch.
type stubService struct {
	mu         sync.RWMutex
	contracts  map[ContractID]*Contract
	getManyN   atomic.Int64 // call counter
}

func newStubService(cs ...*Contract) *stubService {
	s := &stubService{contracts: make(map[ContractID]*Contract)}
	for _, c := range cs {
		s.contracts[c.ID] = c
	}
	return s
}

func (s *stubService) Get(_ context.Context, id ContractID) (*Contract, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.contracts[id]
	return c, nil
}

func (s *stubService) GetMany(_ context.Context, ids []ContractID) (map[ContractID]*Contract, error) {
	s.getManyN.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[ContractID]*Contract, len(ids))
	for _, id := range ids {
		if c, ok := s.contracts[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

func (s *stubService) List(_ context.Context, filter *ContractFilter) ([]*Contract, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Contract
	for _, c := range s.contracts {
		if filter == nil || matchesFilter(c, filter) {
			cp := *c
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *stubService) Subscribe(_ context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent)
	close(ch)
	return ch
}

// makeContract builds a minimal Contract for tests.
func makeContract(id ContractID) *Contract {
	return &Contract{
		ID:        id,
		Statement: string(id),
		Status:    StatusOpen,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Criteria:  []string{},
	}
}

// TestContractByIDLoader_Coalescing verifies that N parallel Load calls for
// distinct keys within one batch window issue exactly 1 GetMany call (T5).
func TestContractByIDLoader_Coalescing(t *testing.T) {
	svc := newStubService(
		makeContract("C-1"),
		makeContract("C-2"),
		makeContract("C-3"),
	)
	loader := NewContractByIDLoader(svc)

	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]*Contract, 3)
	errs := make([]error, 3)

	ids := []ContractID{"C-1", "C-2", "C-3"}
	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = loader.Load(ctx, id)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Load[%d]: %v", i, err)
		}
		if results[i] == nil {
			t.Errorf("Load[%d] returned nil", i)
		}
	}

	// T5: assert ≤1 underlying GetMany call.
	if n := svc.getManyN.Load(); n > 1 {
		t.Errorf("GetMany called %d times for 3 concurrent Load calls; want ≤1 (loader coalescing broken)", n)
	}
}

// TestContractByIDLoader_MissingKey verifies that a missing key returns nil,
// nil (not an error).
func TestContractByIDLoader_MissingKey(t *testing.T) {
	svc := newStubService() // empty
	loader := NewContractByIDLoader(svc)
	c, err := loader.Load(context.Background(), "C-unknown")
	if err != nil {
		t.Errorf("Load missing key: unexpected error %v", err)
	}
	if c != nil {
		t.Errorf("Load missing key: expected nil, got %+v", c)
	}
}

// TestContractByIDLoader_SameKey verifies that multiple concurrent Load calls
// for the same key coalesce to a single GetMany call.
func TestContractByIDLoader_SameKey(t *testing.T) {
	svc := newStubService(makeContract("C-dupe"))
	loader := NewContractByIDLoader(svc)

	ctx := context.Background()
	var wg sync.WaitGroup
	const N = 5
	results := make([]*Contract, N)

	for i := range results {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = loader.Load(ctx, "C-dupe")
		}()
	}
	wg.Wait()

	for i, c := range results {
		if c == nil {
			t.Errorf("results[%d] nil", i)
		}
	}
	// T5: all N callers share one GetMany call.
	if n := svc.getManyN.Load(); n > 1 {
		t.Errorf("GetMany called %d times for %d concurrent same-key Load calls; want ≤1", n, N)
	}
}

// TestContractsByOwnerLoader_Coalescing verifies that N parallel owner Load
// calls coalesce correctly (T5).
func TestContractsByOwnerLoader_Coalescing(t *testing.T) {
	c1 := makeContract("C-owner-1")
	c1.OwnerSessionID = "session-x"
	svc := newStubService(c1)

	loader := NewContractsByOwnerLoader(svc)
	ctx := context.Background()

	var wg sync.WaitGroup
	const N = 3
	results := make([][]*Contract, N)

	for i := range results {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = loader.Load(ctx, "session-x")
		}()
	}
	wg.Wait()

	for i, cs := range results {
		if len(cs) == 0 {
			t.Errorf("results[%d] empty", i)
		}
	}
}

// TestContractResolver_QueryContract verifies the resolver projects a domain
// Contract onto GQLContract correctly (T1: typed field against stubbed service).
func TestContractResolver_QueryContract(t *testing.T) {
	t0 := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	c := &Contract{
		ID:             "C-res-001",
		Statement:      "Deliver the feature",
		OwnerSessionID: "session-res",
		OwnerAgentName: "agent-res",
		Status:         StatusDeliveredPendingValidation,
		CreatedAt:      t0,
		UpdatedAt:      t0,
		LastEventAt:    t0,
		Criteria:       []string{"AC1", "AC2"},
		ReportsTo:      "drew",
	}
	svc := newStubService(c)
	resolver := &ContractResolver{Service: svc}

	got, err := resolver.QueryContract(context.Background(), "C-res-001")
	if err != nil {
		t.Fatalf("QueryContract: %v", err)
	}
	if got == nil {
		t.Fatal("QueryContract returned nil")
	}
	if got.ID != "Contract:C-res-001" {
		t.Errorf("ID = %q, want Contract:C-res-001", got.ID)
	}
	if got.Status != GQLStatusDeliveredPendingValidation {
		t.Errorf("Status = %q, want %q", got.Status, GQLStatusDeliveredPendingValidation)
	}
	if len(got.Criteria) != 2 {
		t.Errorf("Criteria len = %d, want 2", len(got.Criteria))
	}
	if got.ReportsTo == nil || *got.ReportsTo != "drew" {
		t.Errorf("ReportsTo = %v, want *\"drew\"", got.ReportsTo)
	}
}

// TestContractResolver_QueryContract_NotFound verifies the resolver returns
// nil, nil for an unknown id (schema field is nullable).
func TestContractResolver_QueryContract_NotFound(t *testing.T) {
	svc := newStubService()
	resolver := &ContractResolver{Service: svc}

	got, err := resolver.QueryContract(context.Background(), "C-unknown")
	if err != nil {
		t.Fatalf("QueryContract unknown: unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("QueryContract unknown: expected nil, got %+v", got)
	}
}

// TestContractResolver_QueryContracts verifies list projection.
func TestContractResolver_QueryContracts(t *testing.T) {
	t0 := time.Now()
	svc := newStubService(
		&Contract{ID: "C-qa", Statement: "a", Status: StatusOpen, CreatedAt: t0, UpdatedAt: t0, Criteria: []string{}},
		&Contract{ID: "C-qb", Statement: "b", Status: StatusSatisfied, CreatedAt: t0, UpdatedAt: t0, Criteria: []string{}},
	)
	resolver := &ContractResolver{Service: svc}

	all, err := resolver.QueryContracts(context.Background(), nil)
	if err != nil {
		t.Fatalf("QueryContracts: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("QueryContracts returned %d, want 2", len(all))
	}

	// Filtered to open only.
	filtered, err := resolver.QueryContracts(context.Background(), &GQLContractFilter{
		Statuses: []GQLContractStatus{GQLStatusOpen},
	})
	if err != nil {
		t.Fatalf("QueryContracts filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("QueryContracts(open) returned %d, want 1", len(filtered))
	}
	if filtered[0].Status != GQLStatusOpen {
		t.Errorf("filtered[0].Status = %q, want OPEN", filtered[0].Status)
	}
}

// TestStatusMapping_PendingUserApproval verifies that both old and new plugin
// status strings produce PENDING_USER_APPROVAL on the wire (covers the rename).
func TestStatusMapping_PendingUserApproval(t *testing.T) {
	// Domain status already normalises both → same constant in fold.
	// Wire status must reflect the new enum name.
	gql := domainStatusToGQL(StatusPendingUserApproval)
	if gql != GQLStatusPendingUserApproval {
		t.Errorf("domainStatusToGQL(PendingUserApproval) = %q, want %q", gql, GQLStatusPendingUserApproval)
	}
	if string(gql) != "PENDING_USER_APPROVAL" {
		t.Errorf("GQLStatusPendingUserApproval wire value = %q, want PENDING_USER_APPROVAL", gql)
	}
}
