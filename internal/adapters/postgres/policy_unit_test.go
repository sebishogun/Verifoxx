package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type commitContextRecorder struct {
	pgx.Tx
	err               error
	contextErr        error
	deadlineRemaining time.Duration
	hasDeadline       bool
}

func (transaction *commitContextRecorder) Commit(ctx context.Context) error {
	transaction.contextErr = ctx.Err()
	deadline, ok := ctx.Deadline()
	transaction.hasDeadline = ok
	if ok {
		transaction.deadlineRemaining = time.Until(deadline)
	}
	return transaction.err
}

func TestCommitPolicyDetachesCanceledParentAndStaysBounded(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	transaction := &commitContextRecorder{}

	if err := commitPolicy(transaction, parent); err != nil {
		t.Fatalf("commitPolicy() error = %v", err)
	}
	if transaction.contextErr != nil {
		t.Fatalf("commit context error = %v, want nil", transaction.contextErr)
	}
	if !transaction.hasDeadline || transaction.deadlineRemaining <= 0 || transaction.deadlineRemaining > policyCommitWindow {
		t.Fatalf("commit deadline = (%v, %v), want (0, %v]", transaction.hasDeadline, transaction.deadlineRemaining, policyCommitWindow)
	}
}
