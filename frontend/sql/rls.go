package sql

import public "github.com/sebishogun/nornrune/frontend"

// PolicyMode selects PostgreSQL's permissive OR or restrictive AND policy set.
type PolicyMode uint8

const (
	PolicyModeInvalid PolicyMode = iota
	PolicyModePermissive
	PolicyModeRestrictive
)

// PolicyCommand is one CREATE POLICY command selector.
type PolicyCommand uint8

const (
	PolicyCommandInvalid PolicyCommand = iota
	PolicyCommandAll
	PolicyCommandSelect
	PolicyCommandInsert
	PolicyCommandUpdate
	PolicyCommandDelete
)

// RLS owns one compiled PostgreSQL RLS policy and its source metadata. Row
// columns are parallel; names and role edges use shared slabs and CSR ranges.
type RLS struct {
	Semantic *public.Policy
	Table    []byte

	NameBytes []byte
	RoleBytes []byte

	Modes       []PolicyMode
	Commands    []PolicyCommand
	UsingRoots  []public.NodeID
	CheckRoots  []public.NodeID
	RoleStarts  []uint32
	RoleCounts  []uint16
	PolicySpans []public.Span

	NameStarts      []uint32
	NameLengths     []uint32
	RoleNameStarts  []uint32
	RoleNameLengths []uint32
	RolePublic      []uint8
}

// PolicyNames returns owned policy names in source order.
func (rls *RLS) PolicyNames() []string {
	if rls == nil {
		return nil
	}
	result := make([]string, len(rls.NameStarts))
	for row := range result {
		start := rls.NameStarts[row]
		end := start + rls.NameLengths[row]
		result[row] = string(rls.NameBytes[start:end])
	}
	return result
}

// RoleNames returns owned role-edge names in CSR order.
func (rls *RLS) RoleNames() []string {
	if rls == nil {
		return nil
	}
	result := make([]string, len(rls.RoleNameStarts))
	for row := range result {
		start := rls.RoleNameStarts[row]
		end := start + rls.RoleNameLengths[row]
		result[row] = string(rls.RoleBytes[start:end])
	}
	return result
}
