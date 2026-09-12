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
