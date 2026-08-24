package postgres

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sebishogun/verifoxx/internal/persistence"
)

func TestJournalRequiredFailureStormReturnsEverySlot(t *testing.T) {
	const submissions = 32
	storeFailure := errors.New("forced audit failure")
	var fail atomic.Bool
	fail.Store(true)
	store := &fakeAuditStore{append: func(context.Context, *persistence.AuditBatch) error {
		if fail.Load() {
			return storeFailure
		}
		return nil
	}}
	config := testJournalConfig(persistence.AuditRequired)
	config.Writers = 2
	config.QueueDepth = 4
	journal, err := NewJournal(store, config)
	if err != nil {
		t.Fatalf("NewJournal() error = %v", err)
	}
	defer func() {
		if closeErr := journal.Close(context.Background()); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	}()

	results := make(chan error, submissions)
	var done sync.WaitGroup
	done.Add(submissions)
	for range submissions {
		go func() {
			defer done.Done()
			batch := testWriterBatch()
			results <- journal.Submit(context.Background(), &batch)
		}()
	}
	done.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, storeFailure) {
			t.Fatalf("failed Submit() error = %v, want %v", err, storeFailure)
		}
	}
	if available := len(journal.available); available != config.QueueDepth {
		t.Fatalf("available slots after failures = %d, want %d", available, config.QueueDepth)
	}

	fail.Store(false)
	for attempt := range config.QueueDepth {
		batch := testWriterBatch()
		if err := journal.Submit(context.Background(), &batch); err != nil {
			t.Fatalf("recovery Submit(%d) error = %v", attempt, err)
		}
	}
	stats := journal.Stats()
	if stats.Accepted != submissions+uint64(config.QueueDepth) ||
		stats.Failed != submissions || stats.Succeeded != uint64(config.QueueDepth) ||
		stats.Dropped != 0 || stats.InFlight != 0 {
		t.Fatalf("Stats() after failure recovery = %+v", stats)
	}
}
