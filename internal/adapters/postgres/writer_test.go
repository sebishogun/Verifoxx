package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/persistence"
)

func benchmarkWriterBatch(rows int) persistence.AuditBatch {
	batch := testWriterBatch()
	findings := batch.Findings
	batch.Findings = persistence.AuditFindings{
		Rationales:           make([]persistence.ByteRange, 0, rows),
		DriverRequirementIDs: make([]persistence.ByteRange, 0, rows),
		DriverClauseIDs:      make([]persistence.ByteRange, 0, rows),
		DriverReasons:        make([]persistence.ByteRange, 0, rows),
		AppliedRequirements:  make([]persistence.ByteRange, 0, rows),
		MissingEvidence:      make([]persistence.ByteRange, 0, rows),
		Assumptions:          make([]persistence.ByteRange, 0, rows),
		Uncertainty:          make([]persistence.ByteRange, 0, rows),
		Remediation:          make([]persistence.ByteRange, 0, rows),
		RequestRows:          make([]uint32, 0, rows),
		EvidenceOffsets:      make([]uint32, 1, rows+1),
		EvidenceRows:         make([]uint32, 0, rows),
		Decisions:            make([]persistence.Decision, 0, rows),
	}
	for row := range rows {
		batch.Findings.Rationales = append(batch.Findings.Rationales, findings.Rationales[0])
		batch.Findings.DriverRequirementIDs = append(batch.Findings.DriverRequirementIDs, findings.DriverRequirementIDs[0])
		batch.Findings.DriverClauseIDs = append(batch.Findings.DriverClauseIDs, findings.DriverClauseIDs[0])
		batch.Findings.DriverReasons = append(batch.Findings.DriverReasons, findings.DriverReasons[0])
		batch.Findings.AppliedRequirements = append(batch.Findings.AppliedRequirements, findings.AppliedRequirements[0])
		batch.Findings.MissingEvidence = append(batch.Findings.MissingEvidence, findings.MissingEvidence[0])
		batch.Findings.Assumptions = append(batch.Findings.Assumptions, findings.Assumptions[0])
		batch.Findings.Uncertainty = append(batch.Findings.Uncertainty, findings.Uncertainty[0])
		batch.Findings.Remediation = append(batch.Findings.Remediation, findings.Remediation[0])
		batch.Findings.RequestRows = append(batch.Findings.RequestRows, 0)
		batch.Findings.EvidenceRows = append(batch.Findings.EvidenceRows, 0)
		batch.Findings.EvidenceOffsets = append(batch.Findings.EvidenceOffsets, uint32(row+1))
		batch.Findings.Decisions = append(batch.Findings.Decisions, findings.Decisions[0])
	}
	batch.Rows = uint32(rows)
	return batch
}

type fakeAuditStore struct {
	append func(context.Context, *persistence.AuditBatch) error
}

func (store *fakeAuditStore) Append(ctx context.Context, batch *persistence.AuditBatch) error {
	return store.append(ctx, batch)
}

func appendWriterText(batch *persistence.AuditBatch, value string) persistence.ByteRange {
	start := len(batch.Bytes)
	batch.Bytes = append(batch.Bytes, value...)
	return persistence.ByteRange{Start: uint32(start), End: uint32(len(batch.Bytes))}
}

func testWriterBatch() persistence.AuditBatch {
	started := time.Unix(1_777_777_700, 0).UTC()
	batch := persistence.AuditBatch{
		PolicyVersionID: 1,
		StartedAt:       started,
		CompletedAt:     started.Add(time.Millisecond),
		Rows:            1,
	}
	batch.IdempotencyKey = appendWriterText(&batch, "audit-1")
	batch.EngineVersion = appendWriterText(&batch, "engine-1")
	batch.ExecutionMetadata = appendWriterText(&batch, `{}`)
	requestPayload := appendWriterText(&batch, `{"request":"R1"}`)
	batch.Requests.Keys = append(batch.Requests.Keys, appendWriterText(&batch, "R1"))
	batch.Requests.Payloads = append(batch.Requests.Payloads, requestPayload)
	batch.Requests.Hashes = append(batch.Requests.Hashes, sha256.Sum256(requestPayload.Bytes(batch.Bytes)))
	batch.Requests.CapturedAt = append(batch.Requests.CapturedAt, started)
	evidencePayload := appendWriterText(&batch, `{"evidence":"E1"}`)
	batch.Evidence.Keys = append(batch.Evidence.Keys, appendWriterText(&batch, "E1"))
	batch.Evidence.Payloads = append(batch.Evidence.Payloads, evidencePayload)
	batch.Evidence.Hashes = append(batch.Evidence.Hashes, sha256.Sum256(evidencePayload.Bytes(batch.Bytes)))
	batch.Evidence.CapturedAt = append(batch.Evidence.CapturedAt, started)
	batch.Evidence.ExpiresAt = append(batch.Evidence.ExpiresAt, time.Time{})
	batch.Findings.RequestRows = append(batch.Findings.RequestRows, 0)
	batch.Findings.Decisions = append(batch.Findings.Decisions, persistence.DecisionApprove)
	batch.Findings.Rationales = append(batch.Findings.Rationales, appendWriterText(&batch, "approved"))
	batch.Findings.DriverRequirementIDs = append(batch.Findings.DriverRequirementIDs, appendWriterText(&batch, "R1"))
	batch.Findings.DriverClauseIDs = append(batch.Findings.DriverClauseIDs, persistence.ByteRange{})
	batch.Findings.DriverReasons = append(batch.Findings.DriverReasons, persistence.ByteRange{})
	batch.Findings.AppliedRequirements = append(batch.Findings.AppliedRequirements, appendWriterText(&batch, `["R1"]`))
	batch.Findings.MissingEvidence = append(batch.Findings.MissingEvidence, appendWriterText(&batch, `[]`))
	batch.Findings.Assumptions = append(batch.Findings.Assumptions, appendWriterText(&batch, `[]`))
	batch.Findings.Uncertainty = append(batch.Findings.Uncertainty, appendWriterText(&batch, `[]`))
	batch.Findings.Remediation = append(batch.Findings.Remediation, appendWriterText(&batch, `[]`))
	batch.Findings.EvidenceOffsets = append(batch.Findings.EvidenceOffsets, 0, 1)
	batch.Findings.EvidenceRows = append(batch.Findings.EvidenceRows, 0)
	return batch
}

func testJournalConfig(mode persistence.AuditMode) JournalConfig {
	return JournalConfig{
		Capacity: persistence.AuditCapacity{
			Bytes:         1024,
			Requests:      2,
			Evidence:      2,
			Rows:          2,
			EvidenceLinks: 4,
		},
		WriteTimeout: time.Second,
		Mode:         mode,
		Writers:      1,
		QueueDepth:   1,
	}
}

func waitWriterSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func receiveWriterError(t *testing.T, result <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}

func TestJournalOffNeedsNoStoreOrBatch(t *testing.T) {
	journal, err := NewJournal(nil, JournalConfig{Mode: persistence.AuditOff})
	if err != nil {
		t.Fatalf("NewJournal(off) error = %v", err)
	}
	if err := journal.Submit(nil, nil); err != nil {
		t.Fatalf("Submit(off) error = %v", err)
	}
	if stats := journal.Stats(); stats != (JournalStats{}) {
		t.Fatalf("Stats() = %+v, want zero", stats)
	}
	if err := journal.Close(context.Background()); err != nil {
		t.Fatalf("Close(off) error = %v", err)
	}
}

func TestJournalRequiredOwnsInputAndReturnsStoreFailure(t *testing.T) {
	storeErr := errors.New("database unavailable")
	seen := make(chan []byte, 1)
	store := &fakeAuditStore{append: func(_ context.Context, batch *persistence.AuditBatch) error {
		seen <- append([]byte(nil), batch.Bytes...)
		return storeErr
	}}
	journal, err := NewJournal(store, testJournalConfig(persistence.AuditRequired))
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}
	t.Cleanup(func() { _ = journal.Close(context.Background()) })

	batch := testWriterBatch()
	want := append([]byte(nil), batch.Bytes...)
	if err := journal.Submit(context.Background(), &batch); !errors.Is(err, storeErr) {
		t.Fatalf("Submit() error = %v, want %v", err, storeErr)
	}
	batch.Bytes[0] ^= 0xff
	if got := <-seen; !bytes.Equal(got, want) {
		t.Fatalf("store bytes changed: got %q want %q", got, want)
	}
	stats := journal.Stats()
	if stats.Accepted != 1 || stats.Failed != 1 || stats.Succeeded != 0 || stats.InFlight != 0 {
		t.Fatalf("Stats() = %+v, want one failed terminal batch", stats)
	}
}

func TestJournalBestEffortDropsWhenFixedSlotsAreBusy(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	completed := make(chan struct{}, 1)
	var stored []byte
	store := &fakeAuditStore{append: func(_ context.Context, batch *persistence.AuditBatch) error {
		stored = append(stored[:0], batch.Bytes...)
		entered <- struct{}{}
		<-release
		completed <- struct{}{}
		return nil
	}}
	journal, err := NewJournal(store, testJournalConfig(persistence.AuditBestEffort))
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}

	batch := testWriterBatch()
	want := append([]byte(nil), batch.Bytes...)
	if err := journal.Submit(context.Background(), &batch); err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	waitWriterSignal(t, entered, "first store call")
	batch.Bytes[0] ^= 0xff
	if err := journal.Submit(context.Background(), &batch); !errors.Is(err, persistence.ErrJournalQueueFull) {
		t.Fatalf("second Submit() error = %v, want %v", err, persistence.ErrJournalQueueFull)
	}
	close(release)
	waitWriterSignal(t, completed, "best-effort completion")
	if err := journal.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !bytes.Equal(stored, want) {
		t.Fatalf("stored bytes = %q, want owned %q", stored, want)
	}
	stats := journal.Stats()
	if stats.Accepted != 1 || stats.Succeeded != 1 || stats.Dropped != 1 || stats.InFlight != 0 {
		t.Fatalf("Stats() = %+v, want accepted=1 succeeded=1 dropped=1", stats)
	}
}

func TestJournalRequiredWaitsForAcknowledgmentAfterCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	release := make(chan struct{})
	store := &fakeAuditStore{append: func(ctx context.Context, _ *persistence.AuditBatch) error {
		entered <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		<-release
		return ctx.Err()
	}}
	journal, err := NewJournal(store, testJournalConfig(persistence.AuditRequired))
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}
	t.Cleanup(func() { _ = journal.Close(context.Background()) })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	batch := testWriterBatch()
	go func() { result <- journal.Submit(ctx, &batch) }()
	waitWriterSignal(t, entered, "required store call")
	cancel()
	waitWriterSignal(t, canceled, "store cancellation")
	select {
	case err := <-result:
		t.Fatalf("Submit() returned before store acknowledgment: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := receiveWriterError(t, result, "required result"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit() error = %v, want context cancellation", err)
	}
}

func TestJournalCloseDrainsAndRejectsNewSubmits(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	store := &fakeAuditStore{append: func(context.Context, *persistence.AuditBatch) error {
		entered <- struct{}{}
		<-release
		return nil
	}}
	journal, err := NewJournal(store, testJournalConfig(persistence.AuditBestEffort))
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}
	batch := testWriterBatch()
	if err := journal.Submit(context.Background(), &batch); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitWriterSignal(t, entered, "store call")
	closed := make(chan error, 2)
	go func() { closed <- journal.Close(context.Background()) }()
	go func() { closed <- journal.Close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("Close() returned before drain: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := journal.Submit(context.Background(), &batch); !errors.Is(err, persistence.ErrJournalClosed) {
		t.Fatalf("Submit(after close) error = %v, want %v", err, persistence.ErrJournalClosed)
	}
	close(release)
	for range 2 {
		if err := receiveWriterError(t, closed, "close result"); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
}

func TestJournalBestEffortFailureReturnsSlotAndUpdatesStats(t *testing.T) {
	storeErr := errors.New("write failed")
	calls := make(chan struct{}, 2)
	store := &fakeAuditStore{append: func(context.Context, *persistence.AuditBatch) error {
		calls <- struct{}{}
		return storeErr
	}}
	journal, err := NewJournal(store, testJournalConfig(persistence.AuditBestEffort))
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}
	batch := testWriterBatch()
	for attempt := range 2 {
		deadline := time.Now().Add(2 * time.Second)
		for {
			err = journal.Submit(context.Background(), &batch)
			if err == nil {
				break
			}
			if !errors.Is(err, persistence.ErrJournalQueueFull) || time.Now().After(deadline) {
				t.Fatalf("Submit(%d) error = %v", attempt, err)
			}
			time.Sleep(time.Millisecond)
		}
		waitWriterSignal(t, calls, "failed best-effort write")
	}
	if err := journal.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stats := journal.Stats()
	if stats.Accepted != 2 || stats.Failed != 2 || stats.Succeeded != 0 || stats.InFlight != 0 {
		t.Fatalf("Stats() = %+v, want two failed terminal batches", stats)
	}
}

func TestJournalRequiredSubmitAndCloseCompleteTogether(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	store := &fakeAuditStore{append: func(context.Context, *persistence.AuditBatch) error {
		entered <- struct{}{}
		<-release
		return nil
	}}
	journal, err := NewJournal(store, testJournalConfig(persistence.AuditRequired))
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}
	batch := testWriterBatch()
	submitted := make(chan error, 1)
	go func() { submitted <- journal.Submit(context.Background(), &batch) }()
	waitWriterSignal(t, entered, "required write")
	closed := make(chan error, 1)
	go func() { closed <- journal.Close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("Close() returned before required acknowledgment: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := receiveWriterError(t, submitted, "required audit attempt"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := receiveWriterError(t, closed, "required close"); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestJournalCloseDeadlineDoesNotAbandonDrain(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	store := &fakeAuditStore{append: func(context.Context, *persistence.AuditBatch) error {
		entered <- struct{}{}
		<-release
		return nil
	}}
	journal, err := NewJournal(store, testJournalConfig(persistence.AuditBestEffort))
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}
	batch := testWriterBatch()
	if err := journal.Submit(context.Background(), &batch); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitWriterSignal(t, entered, "best-effort write")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := journal.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close(canceled) error = %v, want context cancellation", err)
	}
	close(release)
	if err := journal.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestJournalConstructorRejectsInvalidPersistentConfiguration(t *testing.T) {
	validStore := &fakeAuditStore{append: func(context.Context, *persistence.AuditBatch) error { return nil }}
	tests := []struct {
		name   string
		store  persistence.AuditStore
		config JournalConfig
	}{
		{name: "unknown_mode", config: JournalConfig{Mode: 99}},
		{name: "nil_store", config: testJournalConfig(persistence.AuditRequired)},
		{name: "zero_writers", store: validStore, config: func() JournalConfig {
			config := testJournalConfig(persistence.AuditRequired)
			config.Writers = 0
			return config
		}()},
		{name: "workers_exceed_depth", store: validStore, config: func() JournalConfig {
			config := testJournalConfig(persistence.AuditRequired)
			config.Writers = 2
			return config
		}()},
		{name: "zero_timeout", store: validStore, config: func() JournalConfig {
			config := testJournalConfig(persistence.AuditRequired)
			config.WriteTimeout = 0
			return config
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal, err := NewJournal(test.store, test.config)
			if journal != nil || !errors.Is(err, persistence.ErrInvalidJournal) {
				t.Fatalf("NewJournal() = (%p, %v), want nil and %v", journal, err, persistence.ErrInvalidJournal)
			}
		})
	}
}

func TestJournalConcurrentRequiredSubmits(t *testing.T) {
	const submits = 32
	store := &fakeAuditStore{append: func(context.Context, *persistence.AuditBatch) error { return nil }}
	config := testJournalConfig(persistence.AuditRequired)
	config.Writers = 2
	config.QueueDepth = 4
	journal, err := NewJournal(store, config)
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}
	defer func() { _ = journal.Close(context.Background()) }()

	var wait sync.WaitGroup
	errorsOut := make(chan error, submits)
	wait.Add(submits)
	for range submits {
		go func() {
			defer wait.Done()
			batch := testWriterBatch()
			errorsOut <- journal.Submit(context.Background(), &batch)
		}()
	}
	wait.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("concurrent Submit() error = %v", err)
		}
	}
	stats := journal.Stats()
	if stats.Accepted != submits || stats.Succeeded != submits || stats.InFlight != 0 {
		t.Fatalf("Stats() = %+v, want %d successful submits", stats, submits)
	}
}

func BenchmarkJournalSubmit(b *testing.B) {
	store := &fakeAuditStore{append: func(context.Context, *persistence.AuditBatch) error { return nil }}
	for _, rows := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			batch := benchmarkWriterBatch(rows)
			config := testJournalConfig(persistence.AuditRequired)
			config.Capacity.Rows = rows
			config.Capacity.EvidenceLinks = rows
			journal, err := NewJournal(store, config)
			if err != nil {
				b.Fatalf("NewJournal() error = %v", err)
			}
			defer func() { _ = journal.Close(context.Background()) }()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := journal.Submit(context.Background(), &batch); err != nil {
					b.Fatalf("Submit() error = %v", err)
				}
			}
		})
	}
}
