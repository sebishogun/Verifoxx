CREATE SCHEMA verifoxx AUTHORIZATION verifoxx_migrator;
REVOKE ALL ON SCHEMA verifoxx FROM PUBLIC;
SET LOCAL search_path = verifoxx, pg_catalog;

CREATE TABLE policies (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL,
    active_version_id bigint,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT policies_name_key UNIQUE (name),
    CONSTRAINT policies_name_nonempty CHECK (name = btrim(name) AND name <> '')
);

CREATE TABLE policy_versions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    policy_id bigint NOT NULL,
    semantic_version text NOT NULL,
    source bytea NOT NULL,
    content_hash bytea NOT NULL,
    compiler_version text NOT NULL,
    published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT policy_versions_policy_fkey
        FOREIGN KEY (policy_id) REFERENCES policies (id),
    CONSTRAINT policy_versions_policy_semantic_version_key
        UNIQUE (policy_id, semantic_version),
    CONSTRAINT policy_versions_content_hash_key UNIQUE (content_hash),
    CONSTRAINT policy_versions_policy_id_id_key UNIQUE (policy_id, id),
    CONSTRAINT policy_versions_semantic_version_nonempty
        CHECK (semantic_version = btrim(semantic_version) AND semantic_version <> ''),
    CONSTRAINT policy_versions_source_nonempty CHECK (octet_length(source) > 0),
    CONSTRAINT policy_versions_hash_size CHECK (octet_length(content_hash) = 32),
    CONSTRAINT policy_versions_compiler_version_nonempty
        CHECK (compiler_version = btrim(compiler_version) AND compiler_version <> '')
);

ALTER TABLE policies
    ADD CONSTRAINT policies_active_version_fkey
    FOREIGN KEY (id, active_version_id)
    REFERENCES policy_versions (policy_id, id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE requests (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_key text NOT NULL,
    content_hash bytea NOT NULL,
    payload jsonb NOT NULL,
    captured_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT requests_request_key_hash_key UNIQUE (request_key, content_hash),
    CONSTRAINT requests_key_nonempty CHECK (request_key = btrim(request_key) AND request_key <> ''),
    CONSTRAINT requests_hash_size CHECK (octet_length(content_hash) = 32),
    CONSTRAINT requests_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

CREATE TABLE evidence_snapshots (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    evidence_key text NOT NULL,
    content_hash bytea NOT NULL,
    payload jsonb NOT NULL,
    captured_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz,
    CONSTRAINT evidence_snapshots_evidence_key_hash_key UNIQUE (evidence_key, content_hash),
    CONSTRAINT evidence_snapshots_key_nonempty
        CHECK (evidence_key = btrim(evidence_key) AND evidence_key <> ''),
    CONSTRAINT evidence_snapshots_hash_size CHECK (octet_length(content_hash) = 32),
    CONSTRAINT evidence_snapshots_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT evidence_snapshots_expiry_order CHECK (expires_at IS NULL OR expires_at >= captured_at)
);

CREATE TABLE evaluation_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    idempotency_key text NOT NULL,
    policy_version_id bigint NOT NULL,
    engine_version text NOT NULL,
    started_at timestamptz NOT NULL,
    completed_at timestamptz NOT NULL,
    row_count bigint NOT NULL,
    execution_metadata jsonb NOT NULL,
    CONSTRAINT evaluation_runs_idempotency_key_key UNIQUE (idempotency_key),
    CONSTRAINT evaluation_runs_policy_version_fkey
        FOREIGN KEY (policy_version_id) REFERENCES policy_versions (id),
    CONSTRAINT evaluation_runs_idempotency_key_nonempty
        CHECK (idempotency_key = btrim(idempotency_key) AND idempotency_key <> ''),
    CONSTRAINT evaluation_runs_engine_version_nonempty
        CHECK (engine_version = btrim(engine_version) AND engine_version <> ''),
    CONSTRAINT evaluation_runs_time_order CHECK (completed_at >= started_at),
    CONSTRAINT evaluation_runs_row_count_nonnegative CHECK (row_count >= 0),
    CONSTRAINT evaluation_runs_metadata_object CHECK (jsonb_typeof(execution_metadata) = 'object')
);

CREATE TABLE evaluation_findings (
    run_id bigint NOT NULL,
    row_index bigint NOT NULL,
    request_id bigint NOT NULL,
    decision text NOT NULL,
    rationale text NOT NULL,
    driver_requirement_id text,
    driver_clause_id text,
    driver_reason text,
    applied_requirements jsonb NOT NULL,
    missing_or_conflicting_evidence jsonb NOT NULL,
    assumptions jsonb NOT NULL,
    unresolved_uncertainty jsonb NOT NULL,
    remediation jsonb NOT NULL,
    PRIMARY KEY (run_id, row_index),
    CONSTRAINT evaluation_findings_run_fkey
        FOREIGN KEY (run_id) REFERENCES evaluation_runs (id),
    CONSTRAINT evaluation_findings_request_fkey
        FOREIGN KEY (request_id) REFERENCES requests (id),
    CONSTRAINT evaluation_findings_row_index_nonnegative CHECK (row_index >= 0),
    CONSTRAINT evaluation_findings_decision_valid
        CHECK (decision IN ('Approve', 'Reject', 'Revise', 'Escalate')),
    CONSTRAINT evaluation_findings_rationale_nonempty CHECK (btrim(rationale) <> ''),
    CONSTRAINT evaluation_findings_driver_requirement_nonempty
        CHECK (driver_requirement_id IS NULL OR btrim(driver_requirement_id) <> ''),
    CONSTRAINT evaluation_findings_driver_clause_nonempty
        CHECK (driver_clause_id IS NULL OR btrim(driver_clause_id) <> ''),
    CONSTRAINT evaluation_findings_driver_reason_nonempty
        CHECK (driver_reason IS NULL OR btrim(driver_reason) <> ''),
    CONSTRAINT evaluation_findings_applied_requirements_array
        CHECK (jsonb_typeof(applied_requirements) = 'array'),
    CONSTRAINT evaluation_findings_missing_evidence_array
        CHECK (jsonb_typeof(missing_or_conflicting_evidence) = 'array'),
    CONSTRAINT evaluation_findings_assumptions_array CHECK (jsonb_typeof(assumptions) = 'array'),
    CONSTRAINT evaluation_findings_uncertainty_array
        CHECK (jsonb_typeof(unresolved_uncertainty) = 'array'),
    CONSTRAINT evaluation_findings_remediation_array CHECK (jsonb_typeof(remediation) = 'array')
);

CREATE TABLE evaluation_evidence (
    run_id bigint NOT NULL,
    row_index bigint NOT NULL,
    evidence_ordinal bigint NOT NULL,
    evidence_snapshot_id bigint NOT NULL,
    PRIMARY KEY (run_id, row_index, evidence_ordinal),
    CONSTRAINT evaluation_evidence_finding_fkey
        FOREIGN KEY (run_id, row_index)
        REFERENCES evaluation_findings (run_id, row_index),
    CONSTRAINT evaluation_evidence_snapshot_fkey
        FOREIGN KEY (evidence_snapshot_id) REFERENCES evidence_snapshots (id),
    CONSTRAINT evaluation_evidence_ordinal_nonnegative CHECK (evidence_ordinal >= 0)
);

CREATE TABLE debug_traces (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    policy_version_id bigint NOT NULL,
    evaluation_run_id bigint,
    format text NOT NULL,
    payload bytea NOT NULL,
    content_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    CONSTRAINT debug_traces_policy_version_fkey
        FOREIGN KEY (policy_version_id) REFERENCES policy_versions (id),
    CONSTRAINT debug_traces_evaluation_run_fkey
        FOREIGN KEY (evaluation_run_id) REFERENCES evaluation_runs (id),
    CONSTRAINT debug_traces_format_nonempty CHECK (format = btrim(format) AND format <> ''),
    CONSTRAINT debug_traces_payload_nonempty CHECK (octet_length(payload) > 0),
    CONSTRAINT debug_traces_hash_size CHECK (octet_length(content_hash) = 32),
    CONSTRAINT debug_traces_expiry_order CHECK (expires_at >= created_at)
);

CREATE TABLE benchmark_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    policy_version_id bigint,
    engine_version text NOT NULL,
    environment jsonb NOT NULL,
    parameters jsonb NOT NULL,
    measurements jsonb NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT benchmark_runs_policy_version_fkey
        FOREIGN KEY (policy_version_id) REFERENCES policy_versions (id),
    CONSTRAINT benchmark_runs_engine_version_nonempty
        CHECK (engine_version = btrim(engine_version) AND engine_version <> ''),
    CONSTRAINT benchmark_runs_environment_object CHECK (jsonb_typeof(environment) = 'object'),
    CONSTRAINT benchmark_runs_parameters_object CHECK (jsonb_typeof(parameters) = 'object'),
    CONSTRAINT benchmark_runs_measurements_object CHECK (jsonb_typeof(measurements) = 'object')
);

CREATE INDEX policies_active_version_idx
    ON policies (id, active_version_id)
    WHERE active_version_id IS NOT NULL;
CREATE INDEX policy_versions_policy_idx ON policy_versions (policy_id);
CREATE INDEX evaluation_runs_policy_version_idx ON evaluation_runs (policy_version_id);
CREATE INDEX evaluation_findings_request_idx ON evaluation_findings (request_id);
CREATE INDEX evaluation_evidence_snapshot_idx ON evaluation_evidence (evidence_snapshot_id);
CREATE INDEX debug_traces_policy_version_idx ON debug_traces (policy_version_id);
CREATE INDEX debug_traces_evaluation_run_idx ON debug_traces (evaluation_run_id);
CREATE INDEX benchmark_runs_policy_version_idx ON benchmark_runs (policy_version_id);

CREATE FUNCTION reject_immutable_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'immutable table %.% rejects %', TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_OP
        USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION protect_policy_identity() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'policy identities cannot be deleted' USING ERRCODE = '55000';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'only policies.active_version_id may be updated' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER policies_identity_immutable
    BEFORE UPDATE OR DELETE ON policies
    FOR EACH ROW EXECUTE FUNCTION protect_policy_identity();
CREATE TRIGGER policy_versions_immutable
    BEFORE UPDATE OR DELETE ON policy_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER requests_immutable
    BEFORE UPDATE OR DELETE ON requests
    FOR EACH STATEMENT EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER evidence_snapshots_immutable
    BEFORE UPDATE OR DELETE ON evidence_snapshots
    FOR EACH STATEMENT EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER evaluation_runs_immutable
    BEFORE UPDATE OR DELETE ON evaluation_runs
    FOR EACH STATEMENT EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER evaluation_findings_immutable
    BEFORE UPDATE OR DELETE ON evaluation_findings
    FOR EACH STATEMENT EXECUTE FUNCTION reject_immutable_change();
CREATE TRIGGER evaluation_evidence_immutable
    BEFORE UPDATE OR DELETE ON evaluation_evidence
    FOR EACH STATEMENT EXECUTE FUNCTION reject_immutable_change();

REVOKE ALL ON ALL TABLES IN SCHEMA verifoxx FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA verifoxx FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA verifoxx FROM PUBLIC;

GRANT USAGE ON SCHEMA verifoxx TO verifoxx_runtime;
GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA verifoxx TO verifoxx_runtime;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA verifoxx TO verifoxx_runtime;
REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER
    ON ALL TABLES IN SCHEMA verifoxx FROM verifoxx_runtime;
GRANT UPDATE (active_version_id) ON policies TO verifoxx_runtime;

ALTER DEFAULT PRIVILEGES FOR ROLE verifoxx_migrator IN SCHEMA verifoxx
    REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE verifoxx_migrator IN SCHEMA verifoxx
    REVOKE ALL ON SEQUENCES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE verifoxx_migrator IN SCHEMA verifoxx
    REVOKE ALL ON FUNCTIONS FROM PUBLIC;
