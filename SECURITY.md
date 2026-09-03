# Security Policy

## Supported Versions

Until the first stable release, security fixes are provided on the latest
commit of the `main` branch only. After a stable release, the latest minor
release receives security fixes. Older releases and development snapshots are
not supported versions.

## Reporting A Vulnerability

Use [GitHub private vulnerability reporting](https://github.com/sebishogun/nornrune/security/advisories/new).
Do not open a public issue for a suspected vulnerability. Include affected
versions, impact, reproduction steps, and any suggested mitigation. If private
reporting is unavailable, open a minimal issue asking the maintainer to enable
a private channel without publishing vulnerability details.

Receipt and remediation times depend on maintainer availability; no response
or fix deadline is guaranteed. Coordinated disclosure details will be agreed
for each confirmed report.

## Semantic Security Boundaries

NornRune policy decisions depend on the provided policy, request, and evidence,
clock, and execution-environment attestations. The engine does not establish
that external evidence is authentic merely because it is well-formed. Missing,
stale, unclear, incomplete, or conflicting required evidence must escalate.
Protected-data disclosure rules and pre-execution approval cannot be relaxed.

Security reports may cover parser/resource exhaustion, authorization bypass,
incorrect policy decisions, evidence provenance loss, audit loss, database
privilege boundaries, unsafe generated APIs, and service exposure.
