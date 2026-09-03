# AI Usage

AI tools were used for repository exploration, implementation drafting,
test-case generation, documentation drafting, and targeted code review.
OpenCode provided the interactive coding assistance used during those tasks.

AI output was treated as an untrusted draft and checked against the approved
design, the baseline conformance corpus, compiler and evaluator invariants,
automated tests, race checks, static analysis, and runnable commands. The
implementation and its semantic decisions remain the author's responsibility.

NornRune does not call an AI model at runtime. Policy compilation and request
evaluation are deterministic and operate only on the provided structured policy,
request, and evidence documents.
