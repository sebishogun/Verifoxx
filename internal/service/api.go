package service

import (
	"context"
	"errors"

	"github.com/sebishogun/verifoxx/internal/compile"
)

var (
	ErrInvalidRequest   = errors.New("service: invalid request")
	ErrInvalidPolicy    = errors.New("service: invalid policy")
	ErrPolicyNotFound   = errors.New("service: policy not found")
	ErrAuditUnavailable = errors.New("service: required audit unavailable")
	ErrUnavailable      = errors.New("service: unavailable")
)

// Validation carries stable compiler diagnostics without transport types.
type Validation struct {
	Diagnostics []compile.Diagnostic
}

// PolicyMetadata is the immutable public identity and shape of one Program.
// Name and Version borrow immutable Program symbol bytes for the duration of a
// service call.
type PolicyMetadata struct {
	Name         []byte
	Version      []byte
	ContentHash  [32]byte
	Instructions uint32
	Requirements uint32
	Clauses      uint32
}

// EvaluationRequest carries canonical request/evidence documents and an
// optional explicit policy selection independently of HTTP or gRPC types.
type EvaluationRequest struct {
	Requests       []byte
	Evidence       []byte
	PolicyHash     [32]byte
	ExplicitPolicy bool
}

// PolicyAPI is the transport-independent service port shared by network
// adapters. EvaluateBatch appends one complete canonical JSON result document
// to caller-owned dst and must not return service-owned reusable storage.
type PolicyAPI interface {
	ValidatePolicy(context.Context, []byte) (Validation, error)
	CompilePolicy(context.Context, []byte) (PolicyMetadata, error)
	EvaluateBatch(context.Context, EvaluationRequest, []byte) ([]byte, error)
	LookupPolicy(context.Context, [32]byte) (PolicyMetadata, error)
	Health(context.Context) error
}
