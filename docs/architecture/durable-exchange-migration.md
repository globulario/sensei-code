# Durable exchange migration — the map

Written before any code, per the brief: find every place the workflow waits on
something outside its own process, classify it, then migrate them one at a time
onto one durable primitive.

**Governing principle.** No external dependency may require the originating
process to remain alive while waiting for its answer.

## 1. Every external wait, with evidence

| # | Site | Waits for | Held in | Survives process death? |
|---|------|-----------|---------|------------------------|
| W1 | `internal/workflow/engine.go:3204` `case choice := <-ch` | a person | in-memory channel `e.pending[taskID]` | **No** |
| W2 | `internal/ghbridge/architecture_transport.go:98` `AwaitArchitecture` | ChatGPT | polling loop + `ExchangeLog` file | record yes, **waiter no** |
| W3 | `internal/ghbridge/transport.go:271` `AwaitReview` | ChatGPT | polling loop, **no exchange record** | **No** |
| W4 | `internal/control/runner.go:163` `<-p.answer` + `turnTTL` + lease ticker | a registered remote role | in-memory channel + lease | **No** |
| W5 | `internal/processx/runner.go:77` `cmd.Wait()` | implementation worker | child process | **No** (dies with parent) |
| W6 | `internal/provider/codex_appserver.go:150` `c.cmd.Wait()` | codex app-server | child process | **No** |
| W7 | `internal/sensei/mcp.go:219` `Process.Wait()` | awareness-mcp | child process | **No** |

## 2. Classification

**Human authority** — W1.
The only writers to that channel are `ResolveHuman` (TUI only) and
`DeferAuthority` (headless `run`, and control since `ea89e31`). A daemon had
neither, so the question was unanswerable and unstoppable. This is the proven
unreachable-writer case and is migrated first.

**External asynchronous dependency** — W2, W3, W4.
All three block a live goroutine on a party that answers on its own schedule.
W2 has a durable record but no durable waiter, so the two disagree after a
restart: the request stands and nothing is listening. W3 has no record at all.
W4 additionally couples the answer to a lease held in memory.

**Internal deterministic work** — W5, W6, W7.
Child processes. These are *correctly* tied to process lifetime — a worker
cannot outlive its orchestrator meaningfully. What is wrong is not the wait but
the **absence of a typed terminal state when the child dies**: the run stays in
an execution state naming no missing dependency. Migrate last, and migrate the
failure propagation, not the wait.

## 3. What already exists and must be kept

- `ghbridge.ExchangeRecord` + `ExchangeLog` — one file per open exchange,
  `Open`/`Close`/`Pending`, plus `ReconcileAbandonedExchanges`. This is the seed
  of the unified primitive.
- `session.Store.AwaitingAuthority` — the deferred question, persisted whole,
  cleared on `AuthorityResolved`, with `FindInterrupted` reading it back. Three
  bound tests already prove it survives resume verbatim.
- The whole custody layer: task/request binding, objective digest, base commit,
  graph build identity, candidate identity, snapshot construction and
  verification, principal validation, repository-routing separation, append-only
  receipts, typed refusals, audit-before-review, reviewer independence.

None of that is the problem. The problem is that the *waiting* is in memory.

## 4. The unified primitive

Generalize `ExchangeRecord` rather than introducing a new type:

```
Exchange{ TaskID, RequestID, Kind, State, Binding, Request, Response,
          Failure, CreatedAt, Deadline }

Kind  = architecture | review | human_authority
State = OPEN | ANSWERED | EXPIRED | CANCELLED | INVALID
```

`Close` must stop meaning *delete*. An answered exchange keeps its response;
"still open" becomes a state query rather than a directory listing. That is the
one semantic change to existing behaviour, and it is what lets a response
persist across a restart.

## 5. Failure causes, kept separate

`no_response`, `invalid_response`, `provider_unavailable`, `provider_failed`,
`publication_failed`, `snapshot_verification_failed`, `worker_failed`,
`worker_lost`, `human_required`, `deadline_expired`, `binding_mismatch`.

Two live instances of collapsing these, both observed today:

- `engine.go:2039` appends "Your previous response was not valid bounded JSON"
  on any attempt>1, so a **timeout with no answer** is reported to the architect
  as its own malformed output. Observed 2026-09-12: `r-f82b243f0ca5f55a` drew no
  comment at all, and its retry carried that sentence.
- `config.Load` silently ignored a stated reviewer (fixed in `aa03270`), so
  "provider unavailable" and "provider not selected" were indistinguishable.

## 6. Order

1. **Human authority** (W1) — proven unreachable writer.
2. **Reviewer** (W3) — the active commissioning target, and the only one with no
   durable record today.
3. **Architect** (W2) — same attendance problem, already has half the record.
4. **Worker lifecycle** (W5–W7) — typed terminal on child death.
5. **Delete** the wake/withdraw/retry machinery the above makes unnecessary.

## 7. Restart proof obligation

For each boundary: reach `WAITING_*`, kill, restart, answer, resume, and require
the result to equal the uninterrupted run. Plus duplicate response, stale
response, wrong task binding, wrong candidate/snapshot binding, wrong principal,
notification never delivered, worker exits unexpectedly, publication fails after
a candidate exists, response after deadline.

## 8. Success metric

Concepts deleted, not added. If the design needs more waiter kinds, more wake
messages, more retry state, or more TUI/headless special cases, it is wrong.

## 9. Open: headless cannot consume a durable human resolution

Observed 2026-09-12 on sensei #353. The architect escalated a real question, the
headless run deferred correctly, and the question was persisted whole — the W1
repair (`ea89e31`) working on a live case rather than a test.

But answering it did not resume the task. `ResolveHuman` is reachable only from
`internal/tui/model.go`, so the adopted decision had to be supplied as INPUT to a
fresh governed task instead of consumed by the deferred one.

```
durable human resolution exists      session.Store.AwaitingAuthority, FindInterrupted
headless consumption of it           MISSING
```

Half of W1 is therefore still open. Deferring no longer deadlocks; resuming from
the answer still requires a TUI. Starting a fresh task with the decision as input
is a commissioning workaround, not the repair.

## 10. W1's second half — the owner, and the invariant a new path must preserve

### The authority owner, measured

One rendezvous, one channel, two writers:

| role | site |
|---|---|
| sole rendezvous | `Engine.awaitChoice` — `internal/workflow/engine.go:3189` |
| sole channel | `e.pending[taskID]`, in-memory, buffered 1 |
| writer: answer | `Engine.ResolveHuman` — TUI only |
| writer: defer | `Engine.DeferAuthority` — headless `run`, control since `ea89e31` |
| sole emitter of `AuthorityResolved` | `awaitChoice` |
| sole caller of `authority.Persist` | `awaitChoice` |
| re-asks a deferred question | `Engine.resumeAuthority`, byte for byte, no re-derivation |

`resumeAuthority` was already correct and already continues the same governed
task via `e.execute(ctx, task.TaskID, task.Task)`. It was simply unreachable
without a TUI, because nothing else wrote the channel it blocks on.

### The invariant

> **A human-owned decision is admitted through exactly one rendezvous.** A
> surface carrying a human's answer delivers it into that rendezvous *by
> identity* — the exact task, the question as it was recorded, one of the
> options that question itself offered — and no surface may originate,
> substitute, re-derive, or replay an answer. An answer is spent once: a second
> question is a second boundary, and it is preserved, not answered.

Four things follow, and they are what the tests assert:

1. **No second authority path.** A new surface may not emit `AuthorityResolved`,
   may not call `authority.Persist`, and may not write `e.pending` directly. It
   calls `ResolveHuman`, which is the owner's own door.
2. **Delivery is by identity, not by position.** The option must appear in the
   recorded question *and* in the live `AuthorityRequired` payload. Agreeing
   with the log is not enough if the run asks something else.
3. **Spent once.** The answer is bound to the question it was validated
   against. A later boundary in the same run falls back to deferral — the
   preserved-question behaviour `ea89e31` established.
4. **Undeliverable is preserved, never assumed.** If the answer cannot be
   delivered for any reason, the question stands. Failure to answer must never
   become an answer.

### What this slice deliberately does not add

No new event kind, no new artifact, no parallel state file, no new waiter kind.
The question is already durable — `session.Store.AwaitingAuthority`, read back
by `FindInterrupted` — and `FindInterrupted`'s own contract says why a parallel
file would be wrong: *"the log is already the account of what happened; a
parallel state file could disagree with it, and then neither could be trusted."*

What was missing was a **non-TUI surface that finds the standing question and
carries a person's answer to it**, which is `sensei-code resume`.

This closes the answer→resume edge for the headless path. It does not complete
W1: the human still answers by invoking a command, so the *answer* is supplied
rather than durable. A person who answers while no process runs still has
nowhere to put it. That remainder is the `human_authority` exchange kind in §4,
and it is not built here.

## 11. The legacy boundary — an unprovable record authorizes nothing

Added 2026-09-13, forced by an incident rather than by design. See
`docs/audit/2026-09-13-incident-unauthorized-authority-consumption.md`.

§10's repair preserved the scope going forward. It did not say what to do with the
records already on disk, which carry no scope and cannot be made to. The first
answer was a **warning**: state the boundary and continue. That is what failed.

A warning depends on its reader, and the reader was an agent that had already
concluded the command was inert. It printed the warning, continued, satisfied a
human-owned boundary, and wrote a proposal attributing the decision to the owner.

### The rule

> A durable authority record that cannot prove what it was asked about may admit
> **only** an answer that authorizes nothing. Every other answer is refused
> before the resolution is appended, and the question is left standing.

`Stop` is the one admissible outcome, and it is named **positively**: a new value
added to the outcome vocabulary defaults to refused rather than slipping past an
exclusion list. Stop qualifies because it ends the task instead of permitting a
change, so coverage has nothing to govern.

### What is deliberately not done

- **The historical scope is not reconstructed** — not from the repository, not
  from the graph, not from the objective, not from a fresh plan. There is no
  honest way to recover which files a question nobody can see was asked about,
  and a reconstructed scope would be an invented authorization wearing a
  measurement's clothes.
- **There is no override flag.** No env var, no `--force`, no config key. A
  record that cannot prove its coverage does not become authoritative because
  someone asked twice.
- **`Covers` is untouched.** It was never wrong; it refused correctly, and the
  defect was upstream of it.

### Where it is enforced, and why in two places

| layer | when | why |
|---|---|---|
| `Engine.awaitChoice` | at the rendezvous, after the option is known and before the resolution exists | the enforcement that matters: every surface, including the TUI, goes through it |
| `sensei-code resume` | at selection, before a process, graph or bridge exists | a refusal should cost nothing, and a person naming an inadmissible answer should be told, not watch a run start and end |

The command layer is a convenience and is not trusted: the engine refuses
independently. `TestTheCommandRefusesAnInadmissibleAnswerAndLeavesTheLogUntouched`
covers the wiring between them, because the incident's proximate cause was a
helper that computed the right answer while the caller ignored it.

### A refusal must not consume what it refuses

`WorkflowFailed` sets `done` in `FindInterrupted`, which drops the task and makes
its preserved question unreachable. A refusal that terminalized as FAILED would
therefore **consume the question by refusing it**.

So the refusal re-preserves instead: `preserveQuestion` records the question whole
and the run ends `WorkflowAwaitingAuthority` / `OutcomeDeferred`. The typed
refusal unwraps to `errAuthorityDeferred`, so every existing caller already reads
it as "the question stands". One function writes that record for both ways a
question survives an answer attempt — deferred without an answer, and answered
with one that could not be admitted — because two copies would drift on exactly
the field this section exists to protect.
