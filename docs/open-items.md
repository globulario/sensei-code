# Open items

Work that is deliberately incomplete, recorded here so a later reader does not
mistake a partial implementation for a finished one. Each entry says what is
done, what is not, and what "done" would require.

An item leaves this file when it is closed, not when it is tidy.

## Capability envelope is only partly enforced (P0.4)

**Done.** `internal/broker` mechanically enforces three capabilities for a
worker process: `push`, `force_push` (detected as a non-fast-forward push,
including branch deletion, because git gives a pre-push hook no `--force`
flag), and candidate isolation via `GuardCanonicalCheckout`. These are enforced
by git itself, not by prompt text, and are covered by tests that drive real
pushes against a real remote.

**Not done.** `run_builds`, `run_tests`, `local_commit` and
`production_deploy` are still not mechanically preventable. Nothing stops a
worker process from invoking a compiler, opening a socket, or executing a
deploy script it wrote itself. `Envelope.Unenforceable()` reports exactly this
set so readiness can fail the role, which is the correct fail-honest interim
behaviour — but reporting a gap is not closing it, and the envelope is not
complete until these are enforced.

**What closing it requires.** A process-level sandbox per provider adapter:
restricted exec, filesystem and network policy applied to the worker process
itself. Each provider must declare what it can enforce, and a provider that
cannot honour a required boundary must fail closed for that role rather than
silently widening the envelope.

## Upstream (Sensei): publish blast radius and approval gate structurally

Sensei composes change risk as a single formatted line into
`required_actions` — `"Change risk: blast=local, approval=none"` — in
`golang/server/blast_radius.go:139`. The values exist as struct fields
(`BlastRadius`, `ApprovalGate`) but are not published as structured fields on
the preflight result, so there is no non-textual way for a consumer to obtain
them.

That forces the one string-reading corner of this repository's governance path:
`approvalGate` in `internal/workflow/authority.go`. It is kept deliberately
narrow — it matches a fixed machine-emitted token, never prose, and returns
`"unreadable"` when it cannot find one so the caller escalates rather than
inventing an approval class.

**Do not extend that parser.** When Sensei publishes `blast_radius` and
`approval_gate` as structured fields, delete `approvalGate` and read them
directly. A parser that grows cases is a parser that has become a contract, and
this one must stay disposable.

## Remaining first-version control-plane slices

Tracked in full in `docs/first-working-version-review.md`. Not yet implemented
at the time of writing: P0.3 assisted-default coordinator, P0.6 durable human
authority resolutions, P0.8 cross-agent continuity, and the P1 items including
`doctor` as the single readiness computation.
