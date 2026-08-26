SET LOCAL search_path = nornrune, pg_catalog;

DROP PROPERTY GRAPH policy_graph;

DROP VIEW clause_remediates_with_edges;
DROP VIEW clause_resolves_to_edges;
DROP VIEW clause_requires_evidence_edges;
DROP VIEW requirement_applies_when_edges;
DROP VIEW expression_child_edges;
DROP VIEW clause_contains_expression_edges;
DROP VIEW requirement_contains_clause_edges;
DROP VIEW policy_contains_requirement_edges;

DROP VIEW remediation_vertices;
DROP VIEW outcome_vertices;
DROP VIEW evidence_requirement_vertices;
DROP VIEW expression_vertices;
DROP VIEW clause_vertices;
DROP VIEW requirement_vertices;
DROP VIEW policy_version_vertices;

DROP TABLE policy_edges;
DROP TABLE policy_nodes;

DROP FUNCTION protect_policy_graph_insert();
