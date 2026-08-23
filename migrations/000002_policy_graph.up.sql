SET LOCAL search_path = verifoxx, pg_catalog;

CREATE TABLE policy_nodes (
    policy_version_id bigint NOT NULL,
    node_kind text NOT NULL,
    local_id bigint NOT NULL,
    name text,
    detail text,
    source_start bigint NOT NULL,
    source_end bigint NOT NULL,
    precedence smallint,
    terminal boolean,
    content_hash bytea,
    projected_node_count bigint,
    projected_edge_count bigint,
    projection_xid xid8,
    PRIMARY KEY (policy_version_id, node_kind, local_id),
    CONSTRAINT policy_nodes_version_fkey
        FOREIGN KEY (policy_version_id) REFERENCES policy_versions (id),
    CONSTRAINT policy_nodes_kind_valid CHECK (node_kind IN (
        'policy_version',
        'requirement',
        'clause',
        'expression',
        'evidence_requirement',
        'outcome',
        'remediation'
    )),
    CONSTRAINT policy_nodes_local_id_positive CHECK (local_id > 0),
    CONSTRAINT policy_nodes_name_nonempty CHECK (name IS NULL OR btrim(name) <> ''),
    CONSTRAINT policy_nodes_detail_nonempty CHECK (detail IS NULL OR btrim(detail) <> ''),
    CONSTRAINT policy_nodes_source_range CHECK (source_start >= 0 AND source_end >= source_start),
    CONSTRAINT policy_nodes_hash_size CHECK (content_hash IS NULL OR octet_length(content_hash) = 32),
    CONSTRAINT policy_nodes_projection_counts CHECK (
        (projected_node_count IS NULL OR projected_node_count > 0)
        AND (projected_edge_count IS NULL OR projected_edge_count >= 0)
    ),
    CONSTRAINT policy_nodes_shape CHECK (
        (node_kind = 'policy_version' AND local_id = 1 AND name IS NOT NULL AND detail IS NOT NULL
            AND precedence IS NULL AND terminal IS NULL AND content_hash IS NOT NULL
            AND projected_node_count IS NOT NULL AND projected_edge_count IS NOT NULL
            AND projection_xid IS NOT NULL)
        OR (node_kind = 'requirement' AND name IS NULL AND detail IS NULL
            AND precedence IS NULL AND terminal IS NULL AND content_hash IS NULL
            AND projected_node_count IS NULL AND projected_edge_count IS NULL
            AND projection_xid IS NULL)
        OR (node_kind = 'clause' AND name IS NULL AND detail IS NULL
            AND precedence IS NULL AND terminal IS NULL AND content_hash IS NULL
            AND projected_node_count IS NULL AND projected_edge_count IS NULL
            AND projection_xid IS NULL)
        OR (node_kind = 'expression' AND name IS NOT NULL
            AND precedence IS NULL AND terminal IS NULL AND content_hash IS NULL
            AND projected_node_count IS NULL AND projected_edge_count IS NULL
            AND projection_xid IS NULL)
        OR (node_kind = 'evidence_requirement' AND name IS NOT NULL AND detail IS NOT NULL
            AND precedence IS NULL AND terminal IS NULL AND content_hash IS NULL
            AND projected_node_count IS NULL AND projected_edge_count IS NULL
            AND projection_xid IS NULL)
        OR (node_kind = 'outcome' AND name IS NOT NULL AND precedence IS NOT NULL
            AND terminal IS NOT NULL AND content_hash IS NULL
            AND projected_node_count IS NULL AND projected_edge_count IS NULL
            AND projection_xid IS NULL)
        OR (node_kind = 'remediation' AND name IS NOT NULL
            AND precedence IS NULL AND terminal IS NULL AND content_hash IS NULL
            AND projected_node_count IS NULL AND projected_edge_count IS NULL
            AND projection_xid IS NULL)
    )
);

CREATE TABLE policy_edges (
    policy_version_id bigint NOT NULL,
    edge_id bigint NOT NULL,
    edge_kind text NOT NULL,
    source_kind text NOT NULL,
    source_id bigint NOT NULL,
    target_kind text NOT NULL,
    target_id bigint NOT NULL,
    ordinal bigint NOT NULL,
    branch text,
    PRIMARY KEY (policy_version_id, edge_id),
    CONSTRAINT policy_edges_version_fkey
        FOREIGN KEY (policy_version_id) REFERENCES policy_versions (id),
    CONSTRAINT policy_edges_source_fkey
        FOREIGN KEY (policy_version_id, source_kind, source_id)
        REFERENCES policy_nodes (policy_version_id, node_kind, local_id),
    CONSTRAINT policy_edges_target_fkey
        FOREIGN KEY (policy_version_id, target_kind, target_id)
        REFERENCES policy_nodes (policy_version_id, node_kind, local_id),
    CONSTRAINT policy_edges_id_positive CHECK (edge_id > 0),
    CONSTRAINT policy_edges_ordinal_nonnegative CHECK (ordinal >= 0),
    CONSTRAINT policy_edges_kind_valid CHECK (edge_kind IN (
        'CONTAINS',
        'CHILD',
        'APPLIES_WHEN',
        'REQUIRES',
        'RESOLVES_TO',
        'REMEDIATES_WITH'
    )),
    CONSTRAINT policy_edges_shape CHECK (
        (edge_kind = 'CONTAINS' AND branch IS NULL AND (
            (source_kind = 'policy_version' AND target_kind = 'requirement')
            OR (source_kind = 'requirement' AND target_kind = 'clause')
            OR (source_kind = 'clause' AND target_kind = 'expression')
        ))
        OR (edge_kind = 'CHILD' AND source_kind = 'expression'
            AND target_kind = 'expression' AND branch IS NULL)
        OR (edge_kind = 'APPLIES_WHEN' AND source_kind = 'requirement'
            AND target_kind = 'expression' AND branch IS NULL)
        OR (edge_kind = 'REQUIRES' AND source_kind = 'clause'
            AND target_kind = 'evidence_requirement' AND branch IS NULL)
        OR (edge_kind = 'RESOLVES_TO' AND source_kind = 'clause'
            AND target_kind = 'outcome'
            AND branch IS NOT NULL
            AND branch IN ('satisfied', 'false', 'missing', 'stale', 'unclear', 'unverifiable', 'conflict'))
        OR (edge_kind = 'REMEDIATES_WITH' AND source_kind = 'clause'
            AND target_kind = 'remediation' AND branch IS NULL)
    )
);

CREATE INDEX policy_edges_source_idx
    ON policy_edges (policy_version_id, source_kind, source_id);
CREATE INDEX policy_edges_target_idx
    ON policy_edges (policy_version_id, target_kind, target_id);
CREATE INDEX policy_edges_kind_idx
    ON policy_edges (edge_kind, policy_version_id);

CREATE FUNCTION protect_policy_graph_insert() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    claim_xid xid8;
BEGIN
    IF TG_TABLE_NAME = 'policy_nodes' THEN
        IF NEW.node_kind = 'policy_version' AND NEW.local_id = 1 THEN
            IF NEW.projection_xid IS DISTINCT FROM pg_current_xact_id() THEN
                RAISE EXCEPTION 'policy graph claim must use the current transaction'
                    USING ERRCODE = '55000';
            END IF;
            RETURN NEW;
        END IF;
    END IF;

    SELECT projection_xid
    INTO claim_xid
    FROM verifoxx.policy_nodes
    WHERE policy_version_id = NEW.policy_version_id
      AND node_kind = 'policy_version'
      AND local_id = 1;

    IF claim_xid IS DISTINCT FROM pg_current_xact_id() THEN
        RAISE EXCEPTION 'published policy graph % is immutable', NEW.policy_version_id
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION protect_policy_graph_insert() FROM PUBLIC;

CREATE TRIGGER policy_nodes_insert_guard
    BEFORE INSERT ON policy_nodes
    FOR EACH ROW EXECUTE FUNCTION protect_policy_graph_insert();
CREATE TRIGGER policy_edges_insert_guard
    BEFORE INSERT ON policy_edges
    FOR EACH ROW EXECUTE FUNCTION protect_policy_graph_insert();

CREATE TRIGGER policy_nodes_immutable
    BEFORE UPDATE OR DELETE ON policy_nodes
    FOR EACH STATEMENT EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER policy_edges_immutable
    BEFORE UPDATE OR DELETE ON policy_edges
    FOR EACH STATEMENT EXECUTE FUNCTION reject_immutable_change();

CREATE VIEW policy_version_vertices AS
    SELECT policy_version_id, node_kind, local_id, name, detail,
           source_start, source_end, precedence, terminal, content_hash
    FROM policy_nodes WHERE node_kind = 'policy_version';
CREATE VIEW requirement_vertices AS
    SELECT policy_version_id, node_kind, local_id, name, detail,
           source_start, source_end, precedence, terminal, content_hash
    FROM policy_nodes WHERE node_kind = 'requirement';
CREATE VIEW clause_vertices AS
    SELECT policy_version_id, node_kind, local_id, name, detail,
           source_start, source_end, precedence, terminal, content_hash
    FROM policy_nodes WHERE node_kind = 'clause';
CREATE VIEW expression_vertices AS
    SELECT policy_version_id, node_kind, local_id, name, detail,
           source_start, source_end, precedence, terminal, content_hash
    FROM policy_nodes WHERE node_kind = 'expression';
CREATE VIEW evidence_requirement_vertices AS
    SELECT policy_version_id, node_kind, local_id, name, detail,
           source_start, source_end, precedence, terminal, content_hash
    FROM policy_nodes WHERE node_kind = 'evidence_requirement';
CREATE VIEW outcome_vertices AS
    SELECT policy_version_id, node_kind, local_id, name, detail,
           source_start, source_end, precedence, terminal, content_hash
    FROM policy_nodes WHERE node_kind = 'outcome';
CREATE VIEW remediation_vertices AS
    SELECT policy_version_id, node_kind, local_id, name, detail,
           source_start, source_end, precedence, terminal, content_hash
    FROM policy_nodes WHERE node_kind = 'remediation';

CREATE VIEW policy_contains_requirement_edges AS
    SELECT * FROM policy_edges
    WHERE edge_kind = 'CONTAINS' AND source_kind = 'policy_version' AND target_kind = 'requirement';
CREATE VIEW requirement_contains_clause_edges AS
    SELECT * FROM policy_edges
    WHERE edge_kind = 'CONTAINS' AND source_kind = 'requirement' AND target_kind = 'clause';
CREATE VIEW clause_contains_expression_edges AS
    SELECT * FROM policy_edges
    WHERE edge_kind = 'CONTAINS' AND source_kind = 'clause' AND target_kind = 'expression';
CREATE VIEW expression_child_edges AS
    SELECT * FROM policy_edges
    WHERE edge_kind = 'CHILD';
CREATE VIEW requirement_applies_when_edges AS
    SELECT * FROM policy_edges
    WHERE edge_kind = 'APPLIES_WHEN';
CREATE VIEW clause_requires_evidence_edges AS
    SELECT * FROM policy_edges
    WHERE edge_kind = 'REQUIRES';
CREATE VIEW clause_resolves_to_edges AS
    SELECT * FROM policy_edges
    WHERE edge_kind = 'RESOLVES_TO';
CREATE VIEW clause_remediates_with_edges AS
    SELECT * FROM policy_edges
    WHERE edge_kind = 'REMEDIATES_WITH';

CREATE PROPERTY GRAPH policy_graph
    VERTEX TABLES (
        policy_version_vertices AS policy_version
            KEY (policy_version_id, local_id)
            LABEL policy_version PROPERTIES ALL COLUMNS,
        requirement_vertices AS requirement
            KEY (policy_version_id, local_id)
            LABEL requirement PROPERTIES ALL COLUMNS,
        clause_vertices AS clause
            KEY (policy_version_id, local_id)
            LABEL clause PROPERTIES ALL COLUMNS,
        expression_vertices AS expression
            KEY (policy_version_id, local_id)
            LABEL expression PROPERTIES ALL COLUMNS,
        evidence_requirement_vertices AS evidence_requirement
            KEY (policy_version_id, local_id)
            LABEL evidence_requirement PROPERTIES ALL COLUMNS,
        outcome_vertices AS outcome
            KEY (policy_version_id, local_id)
            LABEL outcome PROPERTIES ALL COLUMNS,
        remediation_vertices AS remediation
            KEY (policy_version_id, local_id)
            LABEL remediation PROPERTIES ALL COLUMNS
    )
    EDGE TABLES (
        policy_contains_requirement_edges AS policy_contains_requirement
            KEY (policy_version_id, edge_id)
            SOURCE KEY (policy_version_id, source_id) REFERENCES policy_version (policy_version_id, local_id)
            DESTINATION KEY (policy_version_id, target_id) REFERENCES requirement (policy_version_id, local_id)
            LABEL "CONTAINS" PROPERTIES ALL COLUMNS,
        requirement_contains_clause_edges AS requirement_contains_clause
            KEY (policy_version_id, edge_id)
            SOURCE KEY (policy_version_id, source_id) REFERENCES requirement (policy_version_id, local_id)
            DESTINATION KEY (policy_version_id, target_id) REFERENCES clause (policy_version_id, local_id)
            LABEL "CONTAINS" PROPERTIES ALL COLUMNS,
        clause_contains_expression_edges AS clause_contains_expression
            KEY (policy_version_id, edge_id)
            SOURCE KEY (policy_version_id, source_id) REFERENCES clause (policy_version_id, local_id)
            DESTINATION KEY (policy_version_id, target_id) REFERENCES expression (policy_version_id, local_id)
            LABEL "CONTAINS" PROPERTIES ALL COLUMNS,
        expression_child_edges AS expression_child
            KEY (policy_version_id, edge_id)
            SOURCE KEY (policy_version_id, source_id) REFERENCES expression (policy_version_id, local_id)
            DESTINATION KEY (policy_version_id, target_id) REFERENCES expression (policy_version_id, local_id)
            LABEL "CHILD" PROPERTIES ALL COLUMNS,
        requirement_applies_when_edges AS requirement_applies_when
            KEY (policy_version_id, edge_id)
            SOURCE KEY (policy_version_id, source_id) REFERENCES requirement (policy_version_id, local_id)
            DESTINATION KEY (policy_version_id, target_id) REFERENCES expression (policy_version_id, local_id)
            LABEL "APPLIES_WHEN" PROPERTIES ALL COLUMNS,
        clause_requires_evidence_edges AS clause_requires_evidence
            KEY (policy_version_id, edge_id)
            SOURCE KEY (policy_version_id, source_id) REFERENCES clause (policy_version_id, local_id)
            DESTINATION KEY (policy_version_id, target_id) REFERENCES evidence_requirement (policy_version_id, local_id)
            LABEL "REQUIRES" PROPERTIES ALL COLUMNS,
        clause_resolves_to_edges AS clause_resolves_to
            KEY (policy_version_id, edge_id)
            SOURCE KEY (policy_version_id, source_id) REFERENCES clause (policy_version_id, local_id)
            DESTINATION KEY (policy_version_id, target_id) REFERENCES outcome (policy_version_id, local_id)
            LABEL "RESOLVES_TO" PROPERTIES ALL COLUMNS,
        clause_remediates_with_edges AS clause_remediates_with
            KEY (policy_version_id, edge_id)
            SOURCE KEY (policy_version_id, source_id) REFERENCES clause (policy_version_id, local_id)
            DESTINATION KEY (policy_version_id, target_id) REFERENCES remediation (policy_version_id, local_id)
            LABEL "REMEDIATES_WITH" PROPERTIES ALL COLUMNS
    );

REVOKE ALL ON policy_nodes, policy_edges FROM PUBLIC;
REVOKE ALL ON
    policy_version_vertices,
    requirement_vertices,
    clause_vertices,
    expression_vertices,
    evidence_requirement_vertices,
    outcome_vertices,
    remediation_vertices,
    policy_contains_requirement_edges,
    requirement_contains_clause_edges,
    clause_contains_expression_edges,
    expression_child_edges,
    requirement_applies_when_edges,
    clause_requires_evidence_edges,
    clause_resolves_to_edges,
    clause_remediates_with_edges
FROM PUBLIC;
REVOKE ALL ON PROPERTY GRAPH policy_graph FROM PUBLIC;

GRANT SELECT, INSERT ON policy_nodes, policy_edges TO verifoxx_runtime;
GRANT SELECT ON
    policy_version_vertices,
    requirement_vertices,
    clause_vertices,
    expression_vertices,
    evidence_requirement_vertices,
    outcome_vertices,
    remediation_vertices,
    policy_contains_requirement_edges,
    requirement_contains_clause_edges,
    clause_contains_expression_edges,
    expression_child_edges,
    requirement_applies_when_edges,
    clause_requires_evidence_edges,
    clause_resolves_to_edges,
    clause_remediates_with_edges
TO verifoxx_runtime;
GRANT SELECT ON PROPERTY GRAPH policy_graph TO verifoxx_runtime;
