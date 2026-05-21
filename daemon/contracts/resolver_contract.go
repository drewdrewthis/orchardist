package contracts

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ContractResolver provides the GraphQL field resolution logic for the Contract
// type and the Query.{contract, contracts} entry points. It is the R6
// single-type resolver for Contract.
//
// Callers (gqlgen-generated resolver stubs in daemon/resolvers/) embed or call
// this struct. Each resolver method goes through a DataLoader (R3) — never
// calls Snapshot() or GetMany directly in a field path.
//
// The projection functions (contractToGQL etc.) convert domain [Contract]
// values to the wire-format shape the GraphQL response expects. They are pure
// functions so callers can test them without a running provider.
type ContractResolver struct {
	// Service is the ContractsService this resolver reads from.
	// Set at daemon startup.
	Service ContractsService

	// Loader is a per-request ContractByIDLoader. Created fresh per request
	// by the resolver middleware / context factory.
	Loader *ContractByIDLoader
}

// QueryContract resolves `Query.contract(id: ID!)`. Returns nil without error
// when the id is unknown — the schema field is nullable.
func (r *ContractResolver) QueryContract(ctx context.Context, id string) (*GQLContract, error) {
	if r.Service == nil {
		return nil, fmt.Errorf("contracts service not initialised")
	}
	key := ContractID(stripPrefix(id))
	var c *Contract
	var err error
	if r.Loader != nil {
		c, err = r.Loader.Load(ctx, key)
	} else {
		c, err = r.Service.Get(ctx, key)
	}
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	return contractToGQL(c), nil
}

// QueryContracts resolves `Query.contracts(filter: ContractFilter)`.
func (r *ContractResolver) QueryContracts(ctx context.Context, filter *GQLContractFilter) ([]*GQLContract, error) {
	if r.Service == nil {
		return nil, fmt.Errorf("contracts service not initialised")
	}
	var domainFilter *ContractFilter
	if filter != nil {
		domainFilter = gqlFilterToDomain(filter)
	}
	list, err := r.Service.List(ctx, domainFilter)
	if err != nil {
		return nil, err
	}
	out := make([]*GQLContract, 0, len(list))
	for _, c := range list {
		out = append(out, contractToGQL(c))
	}
	return out, nil
}

// stripPrefix removes the "Contract:" node-id prefix. Callers that pass either
// the raw plugin id or the prefixed orchard id get the same result.
func stripPrefix(id string) string {
	return strings.TrimPrefix(id, "Contract:")
}

// GQLContract is the wire-format shape of a Contract returned by resolvers.
// This mirrors the graphql.Contract generated type but is defined here so
// daemon/contracts/ compiles independently before make generate runs against
// the new schema partials.
//
// After make generate produces daemon/graphql/models_gen.go with the renamed
// PENDING_USER_APPROVAL enum, the daemon/resolvers/ stubs will import
// daemon/graphql and the projection layer bridges the two shapes.
type GQLContract struct {
	ID               string
	ContractID       string
	Statement        string
	OwnerSessionID   string
	OwnerAgentName   string
	ReportsTo        *string
	ParentContractID *string
	Status           GQLContractStatus
	CreatedAt        string
	UpdatedAt        string
	Criteria         []string
	OpenQuestions    []*GQLContractQuestion
	LastEventAt      string
}

// GQLContractStatus is the wire-format enum value.
type GQLContractStatus string

const (
	GQLStatusOpen                           GQLContractStatus = "OPEN"
	GQLStatusDeliveredPendingValidation     GQLContractStatus = "DELIVERED_PENDING_VALIDATION"
	GQLStatusDeliveredPendingParentValidation GQLContractStatus = "DELIVERED_PENDING_PARENT_VALIDATION"
	GQLStatusPendingUserApproval            GQLContractStatus = "PENDING_USER_APPROVAL"
	GQLStatusAwaitingCancelAck              GQLContractStatus = "AWAITING_CANCEL_ACK"
	GQLStatusWaitingExternal                GQLContractStatus = "WAITING_EXTERNAL"
	GQLStatusSatisfied                      GQLContractStatus = "SATISFIED"
	GQLStatusCancelled                      GQLContractStatus = "CANCELLED"
	GQLStatusJudgeRejectedTerminal          GQLContractStatus = "JUDGE_REJECTED_TERMINAL"
)

// GQLContractQuestion is the wire-format shape of a ContractQuestion.
type GQLContractQuestion struct {
	QuestionID  string
	Text        string
	AskedBy     string
	AskedAt     string
	Deadline    *string
	BlocksClose bool
}

// GQLContractFilter carries optional filter parameters from the GraphQL layer.
type GQLContractFilter struct {
	Statuses         []GQLContractStatus
	OwnerSessionID   *string
	OwnerAgentName   *string
	ParentContractID *string
}

// contractToGQL projects an internal Contract onto the GQL wire shape. Pure.
func contractToGQL(c *Contract) *GQLContract {
	out := &GQLContract{
		ID:             "Contract:" + string(c.ID),
		ContractID:     string(c.ID),
		Statement:      c.Statement,
		OwnerSessionID: c.OwnerSessionID,
		OwnerAgentName: c.OwnerAgentName,
		Status:         domainStatusToGQL(c.Status),
		CreatedAt:      formatRFC3339(c.CreatedAt),
		UpdatedAt:      formatRFC3339(c.UpdatedAt),
		LastEventAt:    formatRFC3339(c.LastEventAt),
		Criteria:       append([]string{}, c.Criteria...),
		OpenQuestions:  questionsToGQL(c.OpenQuestions),
	}
	if c.ReportsTo != "" {
		s := c.ReportsTo
		out.ReportsTo = &s
	}
	if c.ParentContractID != "" {
		s := c.ParentContractID
		out.ParentContractID = &s
	}
	return out
}

// questionsToGQL converts the internal OpenQuestion list onto the GQL shape.
func questionsToGQL(qs []OpenQuestion) []*GQLContractQuestion {
	if len(qs) == 0 {
		return []*GQLContractQuestion{}
	}
	out := make([]*GQLContractQuestion, 0, len(qs))
	for _, q := range qs {
		gq := &GQLContractQuestion{
			QuestionID:  q.QuestionID,
			Text:        q.Text,
			AskedBy:     q.AskedBy,
			AskedAt:     formatRFC3339(q.AskedAt),
			BlocksClose: q.BlocksClose,
		}
		if q.Deadline != nil {
			d := formatRFC3339(*q.Deadline)
			gq.Deadline = &d
		}
		out = append(out, gq)
	}
	return out
}

// domainStatusToGQL maps the domain ContractStatus to the GQL wire enum.
func domainStatusToGQL(s ContractStatus) GQLContractStatus {
	switch s {
	case StatusDeliveredPendingValidation:
		return GQLStatusDeliveredPendingValidation
	case StatusDeliveredPendingParentValidation:
		return GQLStatusDeliveredPendingParentValidation
	case StatusPendingUserApproval:
		return GQLStatusPendingUserApproval
	case StatusAwaitingCancelAck:
		return GQLStatusAwaitingCancelAck
	case StatusWaitingExternal:
		return GQLStatusWaitingExternal
	case StatusSatisfied:
		return GQLStatusSatisfied
	case StatusCancelled:
		return GQLStatusCancelled
	case StatusJudgeRejectedTerminal:
		return GQLStatusJudgeRejectedTerminal
	default:
		return GQLStatusOpen
	}
}

// gqlFilterToDomain converts a GQLContractFilter to the domain ContractFilter.
func gqlFilterToDomain(f *GQLContractFilter) *ContractFilter {
	if f == nil {
		return nil
	}
	df := &ContractFilter{
		OwnerSessionID:   f.OwnerSessionID,
		OwnerAgentName:   f.OwnerAgentName,
		ParentContractID: f.ParentContractID,
	}
	for _, s := range f.Statuses {
		df.Statuses = append(df.Statuses, gqlStatusToDomain(s))
	}
	return df
}

// gqlStatusToDomain converts a GQL wire status enum to the domain constant.
func gqlStatusToDomain(s GQLContractStatus) ContractStatus {
	switch s {
	case GQLStatusDeliveredPendingValidation:
		return StatusDeliveredPendingValidation
	case GQLStatusDeliveredPendingParentValidation:
		return StatusDeliveredPendingParentValidation
	case GQLStatusPendingUserApproval:
		return StatusPendingUserApproval
	case GQLStatusAwaitingCancelAck:
		return StatusAwaitingCancelAck
	case GQLStatusWaitingExternal:
		return StatusWaitingExternal
	case GQLStatusSatisfied:
		return StatusSatisfied
	case GQLStatusCancelled:
		return StatusCancelled
	case GQLStatusJudgeRejectedTerminal:
		return StatusJudgeRejectedTerminal
	default:
		return StatusOpen
	}
}

// formatRFC3339 renders a time as RFC 3339 with nanosecond precision.
func formatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
