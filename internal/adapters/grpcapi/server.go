// Package grpcapi exposes the policy service through bounded protobuf RPCs.
package grpcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	verifoxxv1 "github.com/sebishogun/verifoxx/api/gen/verifoxx/v1"
	"github.com/sebishogun/verifoxx/internal/compile"
	coreservice "github.com/sebishogun/verifoxx/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maxMessageBytes   = 64 << 20
	maxRequestTimeout = 30 * time.Minute
	policyHashBytes   = 32
)

// ErrInvalidServer reports an unusable dependency or transport limit.
var ErrInvalidServer = errors.New("grpcapi: invalid server configuration")

// Config fixes transport storage and service-call deadlines.
type Config struct {
	MaxMessageBytes int
	RequestTimeout  time.Duration
}

func (config Config) valid() bool {
	return config.MaxMessageBytes > 0 && config.MaxMessageBytes <= maxMessageBytes &&
		config.RequestTimeout > 0 && config.RequestTimeout <= maxRequestTimeout
}

type policyServer struct {
	verifoxxv1.UnimplementedPolicyServiceServer
	api       coreservice.PolicyAPI
	admission *coreservice.Service
	config    Config
}

// New constructs and registers one bounded gRPC policy server.
func New(api coreservice.PolicyAPI, admission *coreservice.Service, config Config) (*grpc.Server, error) {
	if api == nil || admission == nil || admission.Stats().Limit == 0 || !config.valid() {
		return nil, ErrInvalidServer
	}
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(config.MaxMessageBytes),
		grpc.MaxSendMsgSize(config.MaxMessageBytes),
	)
	verifoxxv1.RegisterPolicyServiceServer(server, &policyServer{
		api: api, admission: admission, config: config,
	})
	return server, nil
}

func (server *policyServer) ValidatePolicy(
	ctx context.Context,
	request *verifoxxv1.ValidatePolicyRequest,
) (*verifoxxv1.ValidatePolicyResponse, error) {
	if request == nil || len(request.GetSourceJson()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "policy source is required")
	}
	if len(request.GetSourceJson()) > server.config.MaxMessageBytes {
		return nil, status.Error(codes.ResourceExhausted, "policy source exceeds limit")
	}
	callCtx, cancel, admission, err := server.admit(ctx)
	if err != nil {
		return nil, serviceStatus(err)
	}
	defer server.release(&admission, cancel)
	validation, err := server.api.ValidatePolicy(callCtx, request.GetSourceJson())
	if err != nil {
		return nil, serviceStatus(err)
	}
	diagnostics := make([]*verifoxxv1.Diagnostic, len(validation.Diagnostics))
	for index := range validation.Diagnostics {
		diagnostics[index] = encodeDiagnostic(&validation.Diagnostics[index])
	}
	return &verifoxxv1.ValidatePolicyResponse{
		Valid: len(diagnostics) == 0, Diagnostics: diagnostics,
	}, nil
}

func (server *policyServer) CompilePolicy(
	ctx context.Context,
	request *verifoxxv1.CompilePolicyRequest,
) (*verifoxxv1.CompilePolicyResponse, error) {
	if request == nil || len(request.GetSourceJson()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "policy source is required")
	}
	if len(request.GetSourceJson()) > server.config.MaxMessageBytes {
		return nil, status.Error(codes.ResourceExhausted, "policy source exceeds limit")
	}
	callCtx, cancel, admission, err := server.admit(ctx)
	if err != nil {
		return nil, serviceStatus(err)
	}
	defer server.release(&admission, cancel)
	metadata, err := server.api.CompilePolicy(callCtx, request.GetSourceJson())
	if err != nil {
		return nil, serviceStatus(err)
	}
	encoded, ok := encodePolicyMetadata(metadata)
	if !ok {
		return nil, status.Error(codes.Internal, "compiled policy metadata is invalid")
	}
	return &verifoxxv1.CompilePolicyResponse{Policy: encoded}, nil
}

func (server *policyServer) EvaluateBatch(
	ctx context.Context,
	request *verifoxxv1.EvaluateBatchRequest,
) (*verifoxxv1.EvaluateBatchResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "evaluation request is required")
	}
	encoded, err := server.evaluate(
		ctx, request.GetRequestsJson(), request.GetEvidenceJson(), request.GetPolicySha256(),
	)
	if err != nil {
		return nil, err
	}
	return &verifoxxv1.EvaluateBatchResponse{ResultJson: encoded}, nil
}

func (server *policyServer) EvaluateStream(
	stream grpc.BidiStreamingServer[verifoxxv1.EvaluateStreamRequest, verifoxxv1.EvaluateStreamResponse],
) error {
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return serviceStatus(err)
		}
		encoded, err := server.evaluate(
			stream.Context(), request.GetRequestsJson(), request.GetEvidenceJson(), request.GetPolicySha256(),
		)
		if err != nil {
			return err
		}
		if err := stream.Send(&verifoxxv1.EvaluateStreamResponse{ResultJson: encoded}); err != nil {
			return serviceStatus(err)
		}
	}
}

func (server *policyServer) evaluate(ctx context.Context, requests, evidence, encodedHash []byte) ([]byte, error) {
	if len(requests) == 0 || len(evidence) == 0 {
		return nil, status.Error(codes.InvalidArgument, "request and evidence JSON objects are required")
	}
	if len(encodedHash) != 0 && len(encodedHash) != policyHashBytes {
		return nil, status.Error(codes.InvalidArgument, "policy hash must contain 32 bytes")
	}
	total := uint64(len(requests)) + uint64(len(evidence)) + uint64(len(encodedHash))
	if total > uint64(server.config.MaxMessageBytes) {
		return nil, status.Error(codes.ResourceExhausted, "evaluation input exceeds limit")
	}
	callCtx, cancel, admission, err := server.admit(ctx)
	if err != nil {
		return nil, serviceStatus(err)
	}
	defer server.release(&admission, cancel)
	if !jsonObject(requests) || !jsonObject(evidence) {
		return nil, status.Error(codes.InvalidArgument, "request and evidence JSON objects are required")
	}
	var evaluation coreservice.EvaluationRequest
	evaluation.Requests = requests
	evaluation.Evidence = evidence
	if len(encodedHash) != 0 {
		copy(evaluation.PolicyHash[:], encodedHash)
		evaluation.ExplicitPolicy = true
	}
	// The output must not alias still-live protobuf request storage.
	encoded, err := server.api.EvaluateBatch(callCtx, evaluation, nil)
	if err != nil {
		return nil, serviceStatus(err)
	}
	if len(encoded) == 0 {
		return nil, status.Error(codes.Internal, "evaluation output is empty")
	}
	if len(encoded) > server.config.MaxMessageBytes {
		return nil, status.Error(codes.ResourceExhausted, "evaluation output exceeds limit")
	}
	if !json.Valid(encoded) {
		return nil, status.Error(codes.Internal, "evaluation output is invalid")
	}
	return encoded, nil
}

func (server *policyServer) admit(ctx context.Context) (
	context.Context,
	context.CancelFunc,
	coreservice.Admission,
	error,
) {
	callCtx, cancel := context.WithTimeout(ctx, server.config.RequestTimeout)
	admission, err := server.admission.Admit(callCtx)
	if err != nil {
		cancel()
		return nil, nil, coreservice.Admission{}, err
	}
	return callCtx, cancel, admission, nil
}

func (server *policyServer) release(admission *coreservice.Admission, cancel context.CancelFunc) {
	_ = server.admission.Release(admission)
	cancel()
}

func encodeDiagnostic(diagnostic *compile.Diagnostic) *verifoxxv1.Diagnostic {
	return &verifoxxv1.Diagnostic{
		Code:   diagnostic.Code.String(),
		Table:  diagnostic.Table.String(),
		Member: diagnostic.Member.String(),
		Row:    diagnostic.Row,
		Span:   &verifoxxv1.DiagnosticSpan{Start: diagnostic.Span.Start, End: diagnostic.Span.End},
		Ids: &verifoxxv1.DiagnosticIDs{
			Node: uint32(diagnostic.Node), Clause: uint32(diagnostic.Clause),
			Requirement: uint32(diagnostic.Requirement), Field: uint32(diagnostic.Field),
			Value: uint32(diagnostic.Value), Outcome: uint32(diagnostic.Outcome),
			Remediation: uint32(diagnostic.Remediation), EvidenceKind: uint32(diagnostic.EvidenceKind),
			EvidenceState: uint32(diagnostic.EvidenceState),
		},
	}
}

func encodePolicyMetadata(metadata coreservice.PolicyMetadata) (*verifoxxv1.PolicyMetadata, bool) {
	if len(metadata.Name) == 0 || len(metadata.Version) == 0 || metadata.ContentHash == [32]byte{} {
		return nil, false
	}
	return &verifoxxv1.PolicyMetadata{
		Name: string(metadata.Name), Version: string(metadata.Version),
		Sha256: append([]byte(nil), metadata.ContentHash[:]...), Instructions: metadata.Instructions,
		Requirements: metadata.Requirements, Clauses: metadata.Clauses,
	}, true
}

func jsonObject(value []byte) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' && json.Valid(trimmed)
}

func serviceStatus(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	case errors.Is(err, coreservice.ErrInvalidRequest), errors.Is(err, coreservice.ErrInvalidPolicy):
		return status.Error(codes.InvalidArgument, "request is invalid")
	case errors.Is(err, coreservice.ErrPolicyNotFound):
		return status.Error(codes.NotFound, "policy not found")
	case errors.Is(err, coreservice.ErrAuditUnavailable), errors.Is(err, coreservice.ErrServiceBusy),
		errors.Is(err, coreservice.ErrServiceStopping), errors.Is(err, coreservice.ErrUnavailable):
		return status.Error(codes.Unavailable, "service is unavailable")
	default:
		if code := status.Code(err); code != codes.Unknown {
			return err
		}
		return status.Error(codes.Internal, "internal service error")
	}
}
