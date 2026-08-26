//go:build debug

// Package debugtrap provides stable native-debugger breakpoints for semantic execution.
package debugtrap

import "github.com/sebishogun/nornrune/internal/schema"

// Reached marks one semantic instruction boundary in debug builds.
//
//go:noinline
func Reached(schema.NodeID, schema.InstructionID) {}
