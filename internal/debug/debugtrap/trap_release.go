//go:build !debug

// Package debugtrap provides stable native-debugger breakpoints for semantic execution.
package debugtrap

import "github.com/sebishogun/verifoxx/internal/schema"

// Reached is removed by inlining in release builds.
func Reached(schema.NodeID, schema.InstructionID) {}
