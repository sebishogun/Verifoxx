package sql

import public "github.com/sebishogun/nornrune/frontend"

// Diagnostic is one bounded pointerless SQL frontend failure.
type Diagnostic struct {
	Span    public.Span
	Row     uint32
	Field   public.FieldID
	Dialect Dialect
	Command Command
	Code    public.DiagnosticCode
}
