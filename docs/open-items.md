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

Filed upstream: globulario/sensei#171.

## Remaining first-version control-plane slices

All P0 slices from `docs/first-working-version-review.md` are implemented, with
the P0.4 caveat recorded above: the capability envelope is enforced for push,
force-push and candidate isolation, and open for the rest.

Specified but not yet implemented: `docs/p1-repair-knowledge.md`, which lets the
project learn the repair that actually held rather than the one a reviewer
accepted. Blocked on globulario/sensei#172, which adds the positive counterpart
to `forbidden_fix`.

Specified but not yet implemented: `docs/p1-level-1-routine.md`, which makes
ceremony proportional to measured risk without reducing verification. It is
sequenced after the P0 merge and after the governed acceptance run passes,
because a path that skips steps is much harder to debug while the full path has
never completed.

Not yet started, tracked in the same review: the P1 items, including `doctor` as
the single readiness computation promised by the architecture, splitting the
workflow monolith by semantic ownership, and making event/session persistence
failures visible. Beyond those, the canonical Sensei admission/apply/verify
lifecycle remains the last slice of the original sequence and is deliberately
untouched.
