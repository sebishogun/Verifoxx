package grpcapi

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	verifoxxv1 "github.com/sebishogun/verifoxx/api/gen/verifoxx/v1"
	"github.com/sebishogun/verifoxx/internal/security"
	coreservice "github.com/sebishogun/verifoxx/internal/service"
	"google.golang.org/grpc/codes"
)

func TestSecurityRejectsOversizedPolicyBeforeService(t *testing.T) {
	t.Parallel()

	var calls atomic.Uint64
	api := &fakePolicyAPI{validate: func(context.Context, []byte) (coreservice.Validation, error) {
		calls.Add(1)
		return coreservice.Validation{}, nil
	}}
	harness := newGRPCTestHarness(t, api, nil, Config{
		MaxMessageBytes: security.MaximumPolicyBytes + (1 << 20), RequestTimeout: time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := harness.client.ValidatePolicy(ctx, &verifoxxv1.ValidatePolicyRequest{
		SourceJson: make([]byte, security.MaximumPolicyBytes+1),
	})
	assertGRPCCode(t, err, codes.ResourceExhausted)
	if calls.Load() != 0 {
		t.Fatalf("ValidatePolicy calls = %d, oversized policy reached service", calls.Load())
	}
}
