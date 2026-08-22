package eval

import "github.com/sebishogun/verifoxx/internal/schema"

// EvidenceBatch stores one evidence record per row in parallel typed columns.
type EvidenceBatch struct {
	IDs        []schema.EvidenceID
	Kinds      []schema.EvidenceKindID
	States     []schema.EvidenceStateID
	Subjects   []schema.SymbolID
	Scopes     []schema.SymbolID
	Reviewers  []schema.SymbolID
	Timings    []schema.SymbolID
	Timestamps []int64
}

// Len returns the number of evidence rows.
func (e EvidenceBatch) Len() int { return len(e.IDs) }

func (e *EvidenceBatch) resize(rows int) {
	e.IDs = resizeClear(e.IDs, rows)
	e.Kinds = resizeClear(e.Kinds, rows)
	e.States = resizeClear(e.States, rows)
	e.Subjects = resizeClear(e.Subjects, rows)
	e.Scopes = resizeClear(e.Scopes, rows)
	e.Reviewers = resizeClear(e.Reviewers, rows)
	e.Timings = resizeClear(e.Timings, rows)
	e.Timestamps = resizeClear(e.Timestamps, rows)
}
