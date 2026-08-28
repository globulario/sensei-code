# Self-Hosting Witness 1 — FROZEN (before any run)

Decision: M26 (`docs/evidence/mediation/ledger.jsonl`); brief
`docs/work/self-hosting-witness-1.md`. Owner's instruction (2026-08-28):
freeze on governor X = `f01592b`, not `5f02225` (#117 improves the record,
not the governor). No run before this commit exists.

```text
governor source     f01592b0f0828605ed254047fc064f41dacc78f2  (main, the #115 merge; admitted)
governor binary     sensei-code-shw1, sha256 7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea
governor build      cd <clean worktree at X> && go build -o sensei-code-shw1 ./cmd/sensei-code   (go1.25.0 linux/amd64)
subject / base      f01592b0f0828605ed254047fc064f41dacc78f2, detached clean worktree, seeded ONLY with .sensei-code/config.json (sha256 94ed3b1723e3f9bd…);
                    the subject holds no docs/work/self-hosting-witness-1.md and no experiments/self-hosting-witness-1/
plan                experiments/self-hosting-witness-1/plan.json, sha256 3cc668d80a8b603ca81828aecb4697d71472b7c16898332bd1af741bd17349f0, supplied byte for byte via run --plan;
                    no architect provider is consulted for it
task text           the plan's summary, verbatim (--task)
Sensei              domain github.com/globulario/sensei-code, endpoint localhost:10122 via awareness-mcp; producer sensei f79f96f9 (SENSEI_BIN)
graph at freeze     combined digest 42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64, 164,506 triples, CURRENT,
                    graph_build_commit fac399f8225f, source_repo_commit f56f5a305798 -- recorded again per run
worker              Claude, invoked by the governor, confined to the candidate worktree the governor creates
reviewer            the configured independent reviewer (Codex); a provider failure is an instrument outcome, never a substitute verdict
env                 SENSEI_CODE_BENCHMARK=1; derive receipts moved aside before the invocation; --json --timeout 30m
stopping rule       ONE invocation; no opportunistic replanning; exit 3 preserves the question; timeout / crash / provider void is
                    an instrument finding; the candidate is retained, never admitted or merged by the run
```

## Falsifiers

1. The governor never reads or executes the candidate: the governor binary's
   sha256 is recorded at start and end and must be equal; the candidate lives
   in the worktree the governor creates, and the governor's own checkout at X
   is not written by the run (its tree must be clean at the end, except for
   the run's own records under docs/awareness and .sensei-code).
2. No authority in the record is supplied by the candidate's own text or
   code: the routing line names its evidence (graph anchors, derived
   coverage, grants), and the plan's claims are marked `repository`, never
   `graph`.
3. **X governs Y, but X remains X**: the governor commit recorded at the end
   equals the one recorded at the start (`f01592b0f0828605ed254047fc064f41dacc78f2`), and the candidate commit
   produced differs from it.

## Predictions

- Routing on X: the four planned files are examined (all four exist at X and
  are indexed unless the graph is older than they are); route
  `architectural-authority-granted` on graph anchors (engine.go carries
  critical invariants), or `coverage-unexamined` naming any file the graph
  never examined -- the first live observation of #115's rule inside a run.
- The routing line prints `unexamined by the graph` only if some file is.
- Worker implements inside the candidate; validation, audit, one independent
  review; ACCEPT or REVISE is ordinary. `workflow.completed: candidate ready
  for governed admission` is offered, not taken.
- Level-1 classification of the candidate: NOT routine (a critical invariant
  governs engine.go) -- so the change being made is not itself exercised by
  the run that makes it, which is the correct reading of FUTURE_ONLY one
  level up.
