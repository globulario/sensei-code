# Open items

## P0 acceptance record

```text
P0 control-plane slices:        9/9 implemented (P0.1-P0.8, plus P0.9 discovered by the canary)
unit/structural tests:          green
integrated governed acceptance: PASS @ 1bc39f29a7a2
known enforcement gap:          per-provider process sandbox (see below)
upstream dependencies:          globulario/sensei#171, #172
```

The accepted run, `task-1786929227938945137`, went the whole distance without
bypass: certifiable start, authority escalated once and answered, resolution
proposed to Sensei's review queue, plan approved, candidate cut from an exact
base, capability guards installed, worker implemented, four validation checks
executed by the broker and bound to the diff digest, `awareness_diff_audit`
returning `decision: pass · availability: available`, and a reviewer ACCEPT that
cited the governing invariant by name. The pull-request rendezvous was declined:
publication stayed human-owned even on an accepted candidate.

**One precision, so the record is not read as more than it is.** P0.8
cross-agent handoff was proven in an *earlier* run — Claude exhausted its
bounded cycles, state transferred, and Codex continued the same candidate rather
than restarting. The final accepted run converged in a single cycle and
therefore exercised no handoff. Both facts are true; they come from different
runs, and the acceptance record should not imply one run demonstrated
everything.

### The accepted candidate

Retained rather than deleted, pending a decision on whether to adopt it.

```text
branch      sensei-code/task-1786929227938945137
worktree    ../.sensei-code-worktrees/task-1786929227938945137
base        1bc39f29a7a2 (clean)
content     internal/tui/model_test.go, +103, one file added
diff digest 17b59fda5bc0
audit       awareness.diff_audit/v1 · decision: pass · availability: available
            digest 6d3ad6032999da1f408763da949800984df9aabe081e076565d77f6edf16a597
            changed_files_count 1 · findings_count 0
evidence    gofmt -l cmd internal   exit 0
            go vet ./...            exit 0
            go build -buildvcs=false ./...  exit 0
            go test ./...           exit 0
```

The work is uncommitted in its worktree, so deleting the branch would discard
it. It is a legitimate test — it proves the invariant "An unrecognised command
is answered, never handed to the architect as work" at the real TUI boundary —
but adopting it is a publication decision, and the run's own pull-request
rendezvous was declined precisely because publication is human-owned. Adopting
it here would take by hand the step the machine correctly refused to take by
itself. Remove the branch once it is either adopted through review or judged not
worth keeping.

**What the canary has not yet covered**, and what the next acceptance expansion
should add: a change that touches production code rather than tests, a candidate
that legitimately fails its checks, and a candidate the audit blocks. Passing on
one task shape is evidence about that shape.

Work that is deliberately incomplete, recorded here so a later reader does not
mistake a partial implementation for a finished one. Each entry says what is
done, what is not, and what "done" would require.

An item leaves this file when it is closed, not when it is tidy.

## Cross-domain leakage of authority resolutions (blocks merge disposition)

`globulario/sensei-code#10`.

A Level-3 resolution in this repository is proposed through `awareness_propose`
and the candidate file lands in the **services** corpus, because the awareness
server runs with `-home-domain github.com/globulario/services` and propose
writes into the server's `-awareness-dir`. The content is correctly domain-tagged
as sensei-code; only its custody is wrong.

It is not data loss, but it puts a governance artifact in the wrong review queue:
services reviewers are asked to promote knowledge about a repository they may
not own, and this repository's own queue never shows its pending governance.

Not yet established whether the fix belongs in Sensei (honour the domain when
choosing a candidate path), in deployment (point sensei-code at a server whose
home domain is sensei-code), or whether it is intended for a shared single-store
installation. **This is an explicit merge disposition rather than something to
carry forward quietly.**

## Upstream (Sensei): two defects found while commissioning

`globulario/sensei#173` — `propose --kind decision` corrupts a scaffolded
`decisions: []` on first append, producing YAML that cannot parse. A governed
file that cannot be parsed is absent from the graph, and nothing in the propose
path re-reads what it wrote, so every decision recorded here was silently
missing until a rebuild was finally required.

`globulario/sensei#174` — `task_status` fails with ENOENT when `.sensei/tasks`
does not exist, which is the ordinary state of a repository that has not used
tasks. An empty answer is being reported as a failure to answer.

## Candidate worktrees have no terminal lifecycle

`globulario/sensei-code#12`.

Every governed task creates a worktree and a branch and nothing ever resolves
them; seven accumulated in one day of acceptance runs. The problem is not disk
but meaning: recovery reads candidate and task state from disk, so
undifferentiated leftovers turn "resume the interrupted task" into archaeology,
and a candidate stops meaning anything specific.

Deleting on exit is the wrong fix — it would have destroyed the accepted
candidate, whose work is unpublished and human-owned. What is needed is
disposition: retain an accepted-but-unpublished candidate, clean up an adopted
or rejected one, retain a failed one only while resumable state references it,
and reference the evidence before removing anything.

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
