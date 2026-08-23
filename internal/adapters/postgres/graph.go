package postgres

import (
	"bytes"
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/sebishogun/verifoxx/internal/persistence"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

const (
	graphNodePolicyVersion       = "policy_version"
	graphNodeRequirement         = "requirement"
	graphNodeClause              = "clause"
	graphNodeExpression          = "expression"
	graphNodeEvidenceRequirement = "evidence_requirement"
	graphNodeOutcome             = "outcome"
	graphNodeRemediation         = "remediation"

	graphEdgeContains       = "CONTAINS"
	graphEdgeChild          = "CHILD"
	graphEdgeAppliesWhen    = "APPLIES_WHEN"
	graphEdgeRequires       = "REQUIRES"
	graphEdgeResolvesTo     = "RESOLVES_TO"
	graphEdgeRemediatesWith = "REMEDIATES_WITH"
)

var (
	policyNodeTable   = pgx.Identifier{"verifoxx", "policy_nodes"}
	policyEdgeTable   = pgx.Identifier{"verifoxx", "policy_edges"}
	policyNodeColumns = []string{
		"policy_version_id", "node_kind", "local_id", "name", "detail",
		"source_start", "source_end", "precedence", "terminal", "content_hash",
	}
	policyEdgeColumns = []string{
		"policy_version_id", "edge_id", "edge_kind", "source_kind", "source_id",
		"target_kind", "target_id", "ordinal", "branch",
	}
	resolutionBranches = [...]string{
		"satisfied", "false", "missing", "stale", "unclear", "unverifiable", "conflict",
	}
	graphOpcodeNames = [...][]byte{
		nil,
		[]byte("equal"),
		[]byte("not_equal"),
		[]byte("in"),
		[]byte("exists"),
		[]byte("less"),
		[]byte("less_equal"),
		[]byte("greater"),
		[]byte("greater_equal"),
		nil,
		[]byte("all"),
		[]byte("any"),
		[]byte("not"),
	}
	graphRemediationSetField    = []byte("set_field")
	graphRemediationAddEvidence = []byte("add_evidence")
)

func writePolicyGraph(
	ctx context.Context,
	tx pgx.Tx,
	versionID persistence.PolicyVersionID,
	candidate persistence.Candidate,
) error {
	if err := validatePolicyGraphProgram(candidate.Program); err != nil {
		return err
	}
	nodes, err := newPolicyNodeSource(versionID, candidate.Program)
	if err != nil {
		return err
	}
	edges, err := newPolicyEdgeSource(versionID, candidate.Program)
	if err != nil {
		return err
	}

	var claimed int64
	err = tx.QueryRow(ctx, `
		INSERT INTO verifoxx.policy_nodes
		    (policy_version_id, node_kind, local_id, name, detail,
		     source_start, source_end, content_hash,
		     projected_node_count, projected_edge_count, projection_xid)
		VALUES ($1, 'policy_version', 1, $2, $3, 0, $4, $5, $6, $7, pg_current_xact_id())
		ON CONFLICT (policy_version_id, node_kind, local_id) DO NOTHING
		RETURNING local_id
	`, versionID, candidate.Name, candidate.SemanticVersion, len(candidate.Source), candidate.ContentHash[:],
		nodes.total+1, edges.total).Scan(&claimed)
	if err == pgx.ErrNoRows {
		return validateExistingPolicyGraph(ctx, tx, versionID, candidate)
	}
	if err != nil {
		return fmt.Errorf("postgres: claim policy graph projection: %w", err)
	}
	if claimed != 1 {
		return fmt.Errorf("%w: policy graph claim", persistence.ErrStoredPolicyCorrupt)
	}

	nodeCount, err := tx.CopyFrom(ctx, policyNodeTable, policyNodeColumns, nodes)
	if err != nil {
		return fmt.Errorf("postgres: copy policy graph nodes: %w", err)
	}
	if nodeCount != nodes.total {
		return fmt.Errorf("%w: copied %d of %d policy graph nodes", persistence.ErrStoredPolicyCorrupt, nodeCount, nodes.total)
	}

	edgeCount, err := tx.CopyFrom(ctx, policyEdgeTable, policyEdgeColumns, edges)
	if err != nil {
		return fmt.Errorf("postgres: copy policy graph edges: %w", err)
	}
	if edgeCount != edges.total {
		return fmt.Errorf("%w: copied %d of %d policy graph edges", persistence.ErrStoredPolicyCorrupt, edgeCount, edges.total)
	}
	return nil
}

func validateExistingPolicyGraph(
	ctx context.Context,
	tx pgx.Tx,
	versionID persistence.PolicyVersionID,
	candidate persistence.Candidate,
) error {
	var (
		name              string
		detail            string
		contentHash       []byte
		sourceStart       int64
		sourceEnd         int64
		expectedNodeCount int64
		expectedEdgeCount int64
		actualNodeCount   int64
		actualEdgeCount   int64
	)
	err := tx.QueryRow(ctx, `
		SELECT claim.name, claim.detail, claim.source_start, claim.source_end, claim.content_hash,
		       claim.projected_node_count, claim.projected_edge_count,
		       (SELECT count(*) FROM verifoxx.policy_nodes WHERE policy_version_id = $1),
		       (SELECT count(*) FROM verifoxx.policy_edges WHERE policy_version_id = $1)
		FROM verifoxx.policy_nodes AS claim
		WHERE claim.policy_version_id = $1
		  AND claim.node_kind = 'policy_version'
		  AND claim.local_id = 1
	`, versionID).Scan(
		&name, &detail, &sourceStart, &sourceEnd, &contentHash,
		&expectedNodeCount, &expectedEdgeCount, &actualNodeCount, &actualEdgeCount,
	)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("%w: existing policy graph claim disappeared", persistence.ErrStoredPolicyCorrupt)
	}
	if err != nil {
		return fmt.Errorf("postgres: query existing policy graph: %w", err)
	}
	if name != candidate.Name || detail != candidate.SemanticVersion || sourceStart != 0 ||
		sourceEnd != int64(len(candidate.Source)) || !bytes.Equal(contentHash, candidate.ContentHash[:]) ||
		actualNodeCount != expectedNodeCount || actualEdgeCount != expectedEdgeCount {
		return fmt.Errorf("%w: existing policy graph projection", persistence.ErrStoredPolicyCorrupt)
	}
	return nil
}

type policyNodeSource struct {
	values      [10]any
	err         error
	program     *program.Program
	nodeKind    string
	name        []byte
	detail      []byte
	versionID   int64
	row         int64
	total       int64
	localID     int64
	sourceStart int64
	sourceEnd   int64
	precedence  int16
	terminal    bool
}

func newPolicyNodeSource(versionID persistence.PolicyVersionID, compiled *program.Program) (*policyNodeSource, error) {
	total := uint64(len(compiled.RequirementIDs)) + uint64(len(compiled.ClauseAssertionRoots)) +
		uint64(len(compiled.Opcodes)) + uint64(len(compiled.Outcomes.Names)) +
		uint64(len(compiled.Remediations.Kinds))
	if total >= uint64(math.MaxInt) {
		return nil, invalidPolicyGraph("node count")
	}
	source := &policyNodeSource{
		program:   compiled,
		versionID: int64(versionID),
		row:       -1,
		total:     int64(total),
	}
	source.values[0] = &source.versionID
	source.values[1] = &source.nodeKind
	source.values[2] = &source.localID
	source.values[5] = &source.sourceStart
	source.values[6] = &source.sourceEnd
	return source, nil
}

func (source *policyNodeSource) Next() bool {
	if source.err != nil || source.row+1 >= source.total {
		return false
	}
	source.row++
	return true
}

func (source *policyNodeSource) Values() ([]any, error) {
	source.name = nil
	source.detail = nil
	source.values[3] = nil
	source.values[4] = nil
	source.values[7] = nil
	source.values[8] = nil
	source.values[9] = nil

	compiled := source.program
	row := source.row
	requirements := int64(len(compiled.RequirementIDs))
	clauses := int64(len(compiled.ClauseAssertionRoots))
	instructions := int64(len(compiled.Opcodes))
	outcomes := int64(len(compiled.Outcomes.Names))

	switch {
	case row < requirements:
		index := int(row)
		source.nodeKind = graphNodeRequirement
		source.localID = int64(compiled.RequirementIDs[index])
		source.setSpan(compiled.RequirementSourceStarts[index], compiled.RequirementSourceEnds[index])
	case row < requirements+clauses:
		index := int(row - requirements)
		source.nodeKind = graphNodeClause
		source.localID = int64(index + 1)
		source.setSpan(compiled.ClauseSourceStarts[index], compiled.ClauseSourceEnds[index])
	case row < requirements+clauses+instructions:
		index := int(row - requirements - clauses)
		opcode := compiled.Opcodes[index]
		source.localID = int64(index + 1)
		source.setSpan(compiled.InstructionSourceStarts[index], compiled.InstructionSourceEnds[index])
		if opcode == program.OpcodeEvidence {
			source.nodeKind = graphNodeEvidenceRequirement
			source.setName(graphEvidenceKindName(compiled, compiled.EvidenceKinds[index]))
			source.setDetail(graphEvidenceStateName(compiled, compiled.EvidenceStates[index]))
		} else {
			source.nodeKind = graphNodeExpression
			source.setName(graphOpcodeName(opcode))
			if compiled.Fields[index] != 0 {
				source.setDetail(graphFieldName(compiled, compiled.Fields[index]))
			}
		}
	case row < requirements+clauses+instructions+outcomes:
		index := int(row - requirements - clauses - instructions)
		source.nodeKind = graphNodeOutcome
		source.localID = int64(index + 1)
		source.setName(graphSymbol(compiled, compiled.Outcomes.Names[index]))
		source.setSpan(compiled.OutcomeSourceStarts[index], compiled.OutcomeSourceEnds[index])
		source.precedence = int16(compiled.Outcomes.Precedence[index])
		source.terminal = compiled.Outcomes.Terminal[index]
		source.values[7] = &source.precedence
		source.values[8] = &source.terminal
	default:
		index := int(row - requirements - clauses - instructions - outcomes)
		remediation := schema.RemediationID(index + 1)
		record, ok := compiled.Remediations.Lookup(remediation)
		if !ok {
			source.err = invalidPolicyGraph("remediation row")
			return nil, source.err
		}
		source.nodeKind = graphNodeRemediation
		source.localID = int64(remediation)
		source.setSpan(compiled.RemediationSourceStarts[index], compiled.RemediationSourceEnds[index])
		switch record.Kind {
		case result.RemediationSetField:
			source.setName(graphRemediationSetField)
			source.setDetail(graphFieldName(compiled, record.Field))
		case result.RemediationAddEvidence:
			source.setName(graphRemediationAddEvidence)
			source.setDetail(graphEvidenceKindName(compiled, record.EvidenceKind))
		default:
			source.err = invalidPolicyGraph("remediation kind")
			return nil, source.err
		}
	}
	return source.values[:], nil
}

func (source *policyNodeSource) setSpan(start, end uint32) {
	source.sourceStart = int64(start)
	source.sourceEnd = int64(end)
}

func (source *policyNodeSource) setName(value []byte) {
	source.name = value
	source.values[3] = &source.name
}

func (source *policyNodeSource) setDetail(value []byte) {
	source.detail = value
	source.values[4] = &source.detail
}

func (source *policyNodeSource) Err() error { return source.err }

const (
	edgePhasePolicyRequirements uint8 = iota
	edgePhaseRequirementClauses
	edgePhaseRequirementApplicability
	edgePhaseClauseAssertions
	edgePhaseClauseEvidence
	edgePhaseInstructionChildren
	edgePhaseClauseOutcomes
	edgePhaseClauseRemediations
	edgePhaseDone
)

type policyEdgeSource struct {
	values     [9]any
	err        error
	program    *program.Program
	edgeKind   string
	sourceKind string
	targetKind string
	branch     string
	versionID  int64
	total      int64
	edgeID     int64
	sourceID   int64
	targetID   int64
	ordinalDB  int64
	cursor     uint32
	owner      uint32
	phase      uint8
}

func newPolicyEdgeSource(versionID persistence.PolicyVersionID, compiled *program.Program) (*policyEdgeSource, error) {
	total := uint64(len(compiled.RequirementIDs))*2 + graphEdgeCount(compiled.RequirementClauseCounts) +
		uint64(len(compiled.ClauseAssertionRoots)) + graphEdgeCount(compiled.ClauseEvidenceCounts) +
		graphEdgeCount(compiled.OperandCounts) +
		uint64(len(compiled.ClauseAssertionRoots))*uint64(len(resolutionBranches)) +
		graphEdgeCount(compiled.ClauseRemediationCounts)
	if total > uint64(math.MaxInt) {
		return nil, invalidPolicyGraph("edge count")
	}
	source := &policyEdgeSource{
		program:   compiled,
		versionID: int64(versionID),
		total:     int64(total),
	}
	source.values[0] = &source.versionID
	source.values[1] = &source.edgeID
	source.values[2] = &source.edgeKind
	source.values[3] = &source.sourceKind
	source.values[4] = &source.sourceID
	source.values[5] = &source.targetKind
	source.values[6] = &source.targetID
	source.values[7] = &source.ordinalDB
	return source, nil
}

func graphEdgeCount(counts []uint16) uint64 {
	var total uint64
	for _, count := range counts {
		total += uint64(count)
	}
	return total
}

func (source *policyEdgeSource) Next() bool {
	if source.err != nil || source.edgeID >= source.total {
		return false
	}
	compiled := source.program
	for {
		switch source.phase {
		case edgePhasePolicyRequirements:
			if int(source.owner) < len(compiled.RequirementIDs) {
				row := source.owner
				source.owner++
				return source.emit(
					graphEdgeContains, graphNodePolicyVersion, 1,
					graphNodeRequirement, int64(compiled.RequirementIDs[row]), row, "",
				)
			}
			source.nextPhase()
		case edgePhaseRequirementClauses:
			if int(source.owner) >= len(compiled.RequirementIDs) {
				source.nextPhase()
				continue
			}
			owner := int(source.owner)
			count := uint32(compiled.RequirementClauseCounts[owner])
			if source.cursor >= count {
				source.owner++
				source.cursor = 0
				continue
			}
			ordinal := source.cursor
			edge := compiled.RequirementClauseStarts[owner] + ordinal
			source.cursor++
			return source.emit(
				graphEdgeContains, graphNodeRequirement, int64(compiled.RequirementIDs[owner]),
				graphNodeClause, int64(compiled.RequirementClauseIDs[edge]), ordinal, "",
			)
		case edgePhaseRequirementApplicability:
			if int(source.owner) < len(compiled.RequirementIDs) {
				row := source.owner
				source.owner++
				return source.emit(
					graphEdgeAppliesWhen, graphNodeRequirement, int64(compiled.RequirementIDs[row]),
					graphNodeExpression, int64(compiled.RequirementRoots[row]), 0, "",
				)
			}
			source.nextPhase()
		case edgePhaseClauseAssertions:
			if int(source.owner) < len(compiled.ClauseAssertionRoots) {
				row := source.owner
				source.owner++
				return source.emit(
					graphEdgeContains, graphNodeClause, int64(row+1),
					graphNodeExpression, int64(compiled.ClauseAssertionRoots[row]), 0, "",
				)
			}
			source.nextPhase()
		case edgePhaseClauseEvidence:
			if int(source.owner) >= len(compiled.ClauseEvidenceStarts) {
				source.nextPhase()
				continue
			}
			owner := int(source.owner)
			count := uint32(compiled.ClauseEvidenceCounts[owner])
			if source.cursor >= count {
				source.owner++
				source.cursor = 0
				continue
			}
			ordinal := source.cursor
			edge := compiled.ClauseEvidenceStarts[owner] + ordinal
			source.cursor++
			return source.emit(
				graphEdgeRequires, graphNodeClause, int64(owner+1),
				graphNodeEvidenceRequirement, int64(compiled.ClauseEvidenceIDs[edge]), ordinal, "",
			)
		case edgePhaseInstructionChildren:
			if int(source.owner) >= len(compiled.Opcodes) {
				source.nextPhase()
				continue
			}
			owner := int(source.owner)
			count := uint32(compiled.OperandCounts[owner])
			if source.cursor >= count {
				source.owner++
				source.cursor = 0
				continue
			}
			ordinal := source.cursor
			edge := compiled.OperandStarts[owner] + ordinal
			source.cursor++
			return source.emit(
				graphEdgeChild, graphNodeExpression, int64(owner+1),
				graphNodeExpression, int64(compiled.Operands[edge]), ordinal, "",
			)
		case edgePhaseClauseOutcomes:
			if int(source.owner) >= len(compiled.ClauseAssertionRoots) {
				source.nextPhase()
				continue
			}
			if int(source.cursor) >= len(resolutionBranches) {
				source.owner++
				source.cursor = 0
				continue
			}
			owner := int(source.owner)
			ordinal := source.cursor
			outcome := graphClauseOutcome(compiled, owner, ordinal)
			source.cursor++
			return source.emit(
				graphEdgeResolvesTo, graphNodeClause, int64(owner+1),
				graphNodeOutcome, int64(outcome), ordinal, resolutionBranches[ordinal],
			)
		case edgePhaseClauseRemediations:
			if int(source.owner) >= len(compiled.ClauseRemediationStarts) {
				source.nextPhase()
				continue
			}
			owner := int(source.owner)
			count := uint32(compiled.ClauseRemediationCounts[owner])
			if source.cursor >= count {
				source.owner++
				source.cursor = 0
				continue
			}
			ordinal := source.cursor
			edge := compiled.ClauseRemediationStarts[owner] + ordinal
			source.cursor++
			return source.emit(
				graphEdgeRemediatesWith, graphNodeClause, int64(owner+1),
				graphNodeRemediation, int64(compiled.ClauseRemediationIDs[edge]), ordinal, "",
			)
		case edgePhaseDone:
			if source.edgeID != source.total {
				source.err = invalidPolicyGraph("edge traversal count")
			}
			return false
		default:
			source.err = invalidPolicyGraph("edge phase")
			return false
		}
	}
}

func (source *policyEdgeSource) nextPhase() {
	source.phase++
	source.owner = 0
	source.cursor = 0
}

func (source *policyEdgeSource) emit(
	edgeKind, sourceKind string,
	sourceID int64,
	targetKind string,
	targetID int64,
	ordinal uint32,
	branch string,
) bool {
	source.edgeID++
	source.edgeKind = edgeKind
	source.sourceKind = sourceKind
	source.sourceID = sourceID
	source.targetKind = targetKind
	source.targetID = targetID
	source.ordinalDB = int64(ordinal)
	source.branch = branch
	return true
}

func (source *policyEdgeSource) Values() ([]any, error) {
	if source.branch == "" {
		source.values[8] = nil
	} else {
		source.values[8] = &source.branch
	}
	return source.values[:], nil
}

func (source *policyEdgeSource) Err() error { return source.err }

func validatePolicyGraphProgram(compiled *program.Program) error {
	if compiled == nil || len(compiled.InputBytes) == 0 {
		return invalidPolicyGraph("nil or empty Program")
	}
	instructions := len(compiled.Opcodes)
	if instructions == 0 || len(compiled.Fields) != instructions || len(compiled.Values) != instructions ||
		len(compiled.ListStarts) != instructions || len(compiled.ListCounts) != instructions ||
		len(compiled.OperandStarts) != instructions || len(compiled.OperandCounts) != instructions ||
		len(compiled.EvidenceKinds) != instructions || len(compiled.EvidenceStates) != instructions ||
		len(compiled.EvidenceSubjects) != instructions || len(compiled.EvidenceScopes) != instructions ||
		len(compiled.EvidenceTimings) != instructions || len(compiled.RootFlags) != instructions ||
		len(compiled.InstructionSourceStarts) != instructions || len(compiled.InstructionSourceEnds) != instructions {
		return invalidPolicyGraph("instruction columns")
	}
	for row, opcode := range compiled.Opcodes {
		if !opcode.Valid() || !validGraphSpan(compiled, compiled.InstructionSourceStarts[row], compiled.InstructionSourceEnds[row]) ||
			!validGraphCSR(compiled.OperandStarts[row], compiled.OperandCounts[row], len(compiled.Operands)) {
			return invalidPolicyGraph("instruction row")
		}
		start := compiled.OperandStarts[row]
		count := uint32(compiled.OperandCounts[row])
		switch opcode {
		case program.OpcodeAll, program.OpcodeAny:
			if count == 0 {
				return invalidPolicyGraph("empty group")
			}
		case program.OpcodeNot:
			if count != 1 {
				return invalidPolicyGraph("not arity")
			}
		default:
			if count != 0 {
				return invalidPolicyGraph("leaf operands")
			}
		}
		for edge := uint32(0); edge < count; edge++ {
			child := compiled.Operands[start+edge]
			if !validInstructionID(child, instructions) || compiled.Opcodes[int(child-1)] == program.OpcodeEvidence {
				return invalidPolicyGraph("instruction child")
			}
		}
		if opcode == program.OpcodeEvidence {
			if graphEvidenceKindName(compiled, compiled.EvidenceKinds[row]) == nil ||
				graphEvidenceStateName(compiled, compiled.EvidenceStates[row]) == nil {
				return invalidPolicyGraph("evidence instruction")
			}
		} else if compiled.Fields[row] != 0 && graphFieldName(compiled, compiled.Fields[row]) == nil {
			return invalidPolicyGraph("instruction field")
		}
	}

	requirements := len(compiled.RequirementIDs)
	if requirements == 0 || len(compiled.RequirementRoots) != requirements ||
		len(compiled.RequirementClauseStarts) != requirements || len(compiled.RequirementClauseCounts) != requirements ||
		len(compiled.RequirementSourceStarts) != requirements || len(compiled.RequirementSourceEnds) != requirements {
		return invalidPolicyGraph("requirement columns")
	}
	clauses := len(compiled.ClauseAssertionRoots)
	if clauses == 0 || len(compiled.ClauseEvidenceStarts) != clauses || len(compiled.ClauseEvidenceCounts) != clauses ||
		len(compiled.ClauseOnSatisfied) != clauses || len(compiled.ClauseOnFalse) != clauses ||
		len(compiled.ClauseRemediationStarts) != clauses || len(compiled.ClauseRemediationCounts) != clauses ||
		len(compiled.ClauseSourceStarts) != clauses || len(compiled.ClauseSourceEnds) != clauses {
		return invalidPolicyGraph("clause columns")
	}
	for row, requirementID := range compiled.RequirementIDs {
		if requirementID == 0 || !validInstructionID(compiled.RequirementRoots[row], instructions) ||
			compiled.Opcodes[int(compiled.RequirementRoots[row]-1)] == program.OpcodeEvidence ||
			!validGraphCSR(compiled.RequirementClauseStarts[row], compiled.RequirementClauseCounts[row], len(compiled.RequirementClauseIDs)) ||
			!validGraphSpan(compiled, compiled.RequirementSourceStarts[row], compiled.RequirementSourceEnds[row]) {
			return invalidPolicyGraph("requirement row")
		}
		for previous := 0; previous < row; previous++ {
			if compiled.RequirementIDs[previous] == requirementID {
				return invalidPolicyGraph("duplicate requirement ID")
			}
		}
		start := compiled.RequirementClauseStarts[row]
		count := uint32(compiled.RequirementClauseCounts[row])
		for edge := uint32(0); edge < count; edge++ {
			clause := compiled.RequirementClauseIDs[start+edge]
			if clause == 0 || uint64(clause) > uint64(clauses) {
				return invalidPolicyGraph("requirement clause")
			}
		}
	}

	outcomes := len(compiled.Outcomes.Names)
	if outcomes == 0 || len(compiled.Outcomes.Precedence) != outcomes || len(compiled.Outcomes.Terminal) != outcomes ||
		len(compiled.OutcomeSourceStarts) != outcomes || len(compiled.OutcomeSourceEnds) != outcomes {
		return invalidPolicyGraph("outcome columns")
	}
	for row, name := range compiled.Outcomes.Names {
		if graphSymbol(compiled, name) == nil ||
			!validGraphSpan(compiled, compiled.OutcomeSourceStarts[row], compiled.OutcomeSourceEnds[row]) {
			return invalidPolicyGraph("outcome row")
		}
	}

	remediations := len(compiled.Remediations.Kinds)
	if len(compiled.Remediations.Fields) != remediations || len(compiled.Remediations.Values) != remediations ||
		len(compiled.Remediations.EvidenceKinds) != remediations ||
		len(compiled.RemediationSourceStarts) != remediations || len(compiled.RemediationSourceEnds) != remediations {
		return invalidPolicyGraph("remediation columns")
	}
	for row := 0; row < remediations; row++ {
		record, ok := compiled.Remediations.Lookup(schema.RemediationID(row + 1))
		if !ok || !validGraphSpan(compiled, compiled.RemediationSourceStarts[row], compiled.RemediationSourceEnds[row]) {
			return invalidPolicyGraph("remediation row")
		}
		switch record.Kind {
		case result.RemediationSetField:
			if graphFieldName(compiled, record.Field) == nil || record.Value == 0 {
				return invalidPolicyGraph("set-field remediation")
			}
		case result.RemediationAddEvidence:
			if graphEvidenceKindName(compiled, record.EvidenceKind) == nil {
				return invalidPolicyGraph("add-evidence remediation")
			}
		default:
			return invalidPolicyGraph("remediation kind")
		}
	}

	resolutionRows := uint64(clauses) * uint64(truth.ReasonCount)
	if resolutionRows > math.MaxInt || uint64(len(compiled.Resolutions.OutcomeIDs)) != resolutionRows {
		return invalidPolicyGraph("resolution rows")
	}
	for row := 0; row < clauses; row++ {
		assertion := compiled.ClauseAssertionRoots[row]
		if !validInstructionID(assertion, instructions) || compiled.Opcodes[int(assertion-1)] == program.OpcodeEvidence ||
			!validGraphCSR(compiled.ClauseEvidenceStarts[row], compiled.ClauseEvidenceCounts[row], len(compiled.ClauseEvidenceIDs)) ||
			!validGraphCSR(compiled.ClauseRemediationStarts[row], compiled.ClauseRemediationCounts[row], len(compiled.ClauseRemediationIDs)) ||
			!validOutcomeID(compiled.ClauseOnSatisfied[row], outcomes) ||
			!validOutcomeID(compiled.ClauseOnFalse[row], outcomes) ||
			!validGraphSpan(compiled, compiled.ClauseSourceStarts[row], compiled.ClauseSourceEnds[row]) {
			return invalidPolicyGraph("clause row")
		}
		evidenceStart := compiled.ClauseEvidenceStarts[row]
		for edge := uint32(0); edge < uint32(compiled.ClauseEvidenceCounts[row]); edge++ {
			instruction := compiled.ClauseEvidenceIDs[evidenceStart+edge]
			if !validInstructionID(instruction, instructions) || compiled.Opcodes[int(instruction-1)] != program.OpcodeEvidence {
				return invalidPolicyGraph("clause evidence")
			}
		}
		remediationStart := compiled.ClauseRemediationStarts[row]
		for edge := uint32(0); edge < uint32(compiled.ClauseRemediationCounts[row]); edge++ {
			remediation := compiled.ClauseRemediationIDs[remediationStart+edge]
			if remediation == 0 || uint64(remediation) > uint64(remediations) {
				return invalidPolicyGraph("clause remediation")
			}
		}
		for branch := range resolutionBranches {
			if !validOutcomeID(graphClauseOutcome(compiled, row, uint32(branch)), outcomes) {
				return invalidPolicyGraph("clause resolution")
			}
		}
		resolutionBase := row * truth.ReasonCount
		unverifiable := compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonUnverifiable-1)]
		if compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonWrongScope-1)] != unverifiable ||
			compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonWrongSubject-1)] != unverifiable ||
			compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonWrongTiming-1)] != unverifiable ||
			compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonInvalid-1)] != unverifiable {
			return invalidPolicyGraph("unprojected reason resolution")
		}
	}
	return nil
}

func graphClauseOutcome(compiled *program.Program, clause int, branch uint32) schema.OutcomeID {
	switch branch {
	case 0:
		return compiled.ClauseOnSatisfied[clause]
	case 1:
		return compiled.ClauseOnFalse[clause]
	case 2:
		return compiled.Resolutions.OutcomeIDs[clause*truth.ReasonCount+int(truth.ReasonMissing-1)]
	case 3:
		return compiled.Resolutions.OutcomeIDs[clause*truth.ReasonCount+int(truth.ReasonStale-1)]
	case 4:
		return compiled.Resolutions.OutcomeIDs[clause*truth.ReasonCount+int(truth.ReasonUnclear-1)]
	case 5:
		return compiled.Resolutions.OutcomeIDs[clause*truth.ReasonCount+int(truth.ReasonUnverifiable-1)]
	case 6:
		return compiled.Resolutions.OutcomeIDs[clause*truth.ReasonCount+int(truth.ReasonConflict-1)]
	default:
		return 0
	}
}

func validGraphCSR(start uint32, count uint16, edgeLen int) bool {
	return uint64(start)+uint64(count) <= uint64(edgeLen)
}

func validGraphSpan(compiled *program.Program, start, end uint32) bool {
	return start <= end && uint64(end) <= uint64(len(compiled.InputBytes))
}

func validInstructionID(id schema.InstructionID, instructionCount int) bool {
	return id != 0 && uint64(id) <= uint64(instructionCount)
}

func validOutcomeID(id schema.OutcomeID, outcomeCount int) bool {
	return id != 0 && uint64(id) <= uint64(outcomeCount)
}

func graphSymbol(compiled *program.Program, id schema.SymbolID) []byte {
	value, ok := compiled.Symbol(id)
	if !ok || len(bytes.TrimSpace(value)) == 0 {
		return nil
	}
	return value
}

func graphFieldName(compiled *program.Program, id schema.FieldID) []byte {
	if id == 0 || uint64(id) > uint64(len(compiled.FieldNames)) {
		return nil
	}
	return graphSymbol(compiled, compiled.FieldNames[int(id-1)])
}

func graphEvidenceKindName(compiled *program.Program, id schema.EvidenceKindID) []byte {
	if id == 0 || uint64(id) > uint64(len(compiled.EvidenceKindNames)) {
		return nil
	}
	return graphSymbol(compiled, compiled.EvidenceKindNames[int(id-1)])
}

func graphEvidenceStateName(compiled *program.Program, id schema.EvidenceStateID) []byte {
	if id == 0 || uint64(id) > uint64(len(compiled.EvidenceStateNames)) {
		return nil
	}
	return graphSymbol(compiled, compiled.EvidenceStateNames[int(id-1)])
}

func graphOpcodeName(opcode program.Opcode) []byte {
	if int(opcode) >= len(graphOpcodeNames) {
		return nil
	}
	return graphOpcodeNames[opcode]
}

func invalidPolicyGraph(detail string) error {
	return fmt.Errorf("%w: policy graph %s", persistence.ErrInvalidPolicyPersistence, detail)
}
