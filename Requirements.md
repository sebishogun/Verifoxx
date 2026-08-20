Verifoxx AI Engineer Test | Semantic Decision Representation Exercise
__________________________________________________________________________________________________________________________
Timebox 4-5 hours. Use of AI tools is permitted; please state briefly where they were used.
Objective
Build a small program or service that turns natural-language requirements into an intermediate semantic representation and
uses that representation to evaluate requests. We are looking for clear modelling, careful reasoning and readable engineering -
not a large system or polished UI.
Task
• Read the three requirement statements below and create an intermediate semantic representation.
• Evaluate each request against that representation and the evidence pack.
• Return one of four outcomes for each request: Approve, Reject, Revise, or Escalate.
• Explain which requirement, evidence condition or uncertainty drove each result.
This is not a flat JSON extraction exercise. Your representation should preserve enough meaning to support consistent
downstream decision-making.
Decision meanings
Decision Meaning
Approve The request satisfies the relevant requirements and supporting

evidence.

Reject The request violates a non-negotiable condition.
Revise The request is not acceptable as submitted, but could become
acceptable through a bounded change, such as reducing scope,
lowering usage, or providing an additional allowed evidence item.
Escalate The request cannot be decided safely and automatically because
required evidence is missing, incomplete, stale, conflicting, or the
operating condition cannot be verified.

Requirement statements
ID Requirement
R1 External partners may request aggregate analytical outputs from the
protected dataset only if no individual-level information is disclosed
and a valid approval record exists before execution.

R2 Any processing involving protected data must run in the approved local
execution environment. If the execution environment cannot be
verified, the request must not be automatically approved.
R3 Trusted internal teams may request a temporary increase above the
standard usage limit, but only where a specific usage-adjustment
approval exists. Disclosure restrictions and pre-execution approval
conditions cannot be relaxed. If approval evidence is unclear, stale or
conflicting, the case should be escalated rather than assumed safe.

Verifoxx - Candidate Exercise

Candidate input pack
Request pack
ID Requester / trust Action / output Environment / usage Evidence
R1 external_partner; external aggregate_analysis;
aggregate_counts;
protected_dataset

local_approved_env;
standard

E1, E2

R2 external_partner; external row_level_export;
individual_records;
protected_dataset

local_approved_env;
standard

E1, E2

R3 internal_team;
trusted_internal

aggregate_analysis;
aggregate_counts;
protected_dataset

local_approved_env;
above_standard_limit

E1, E2

R4 external_partner; external aggregate_analysis;
aggregate_counts;
protected_dataset

unverified_remote_env;
standard

E1

R5 internal_team;
trusted_internal

aggregate_analysis;
aggregate_counts;
protected_dataset

local_approved_env;
above_standard_limit

E2, E3, E4

Evidence pack
ID Type Details
E1 approval_record status=valid; timing=before_execution;
reviewer=designated_reviewer;
timestamp_state=current

E2 execution_environment_attestation subject=local_approved_env; status=verified;

attestation_state=valid

E3 usage_limit_adjustment status=approved; scope=trusted_internal_only;
adjustment_type=above_standard_limit;
reviewer=designated_reviewer;
timestamp_state=current

E4 approval_record status=conflicting; timing=before_execution;
reviewer_state=one_valid_one_revoked;
timestamp_state=conflicting

What to submit
Deliverable Expectation
Source code Runnable implementation, with clear structure and minimal setup.
README How to run, dependencies, and input/output format.
Design note Maximum one page: explain the intermediate representation, why it is more
useful than flat extraction, how decisions are made, where escalation occurs,
and what you would improve next.

Outputs for R1-R5 Machine-readable results for all five requests. Suggested fields: request_id,

decision, rationale, requirements_applied, evidence_used,
missing_or_conflicting_evidence, assumptions, unresolved_uncertainty.
Optional tests Automated tests are welcome but not mandatory. Meaningful edge cases

matter more than volume.

Minimum expectations and evaluation
Minimum expectations Evaluation focus
Define an intermediate representation; do not hardcode each request.
Distinguish non-negotiable constraints from revisable conditions. Treat
missing or conflicting evidence explicitly. Produce bounded explanations.

Semantic modelling; decision quality; uncertainty handling; engineering
quality; clarity of reasoning and assumptions.

Note: We prefer honest partial work with clear reasoning over polished but opaque submissions.
