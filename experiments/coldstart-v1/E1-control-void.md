# Encounter 1 (control) — VOID_MEASUREMENT

```
launched   2026-08-27 01:05:22Z   base cb39cd0 (setup over pinned 3ffd83c)
terminal   01:08:52Z  exit 3  workflow.awaiting_authority   wall 210s
closure    NEVER RAN — 0 gap/closure events, no receipt, no recipe
outcome    not one of RECORDED / DUPLICATE / REFUSED / NO_PROPOSAL:
           the investigator was never reached
```

Preserved: `runs/control/E1.log` (full stream), `E1.attempt0-selfdirty.log`.

## The seven-link chain stopped before link 1

The run escalated through the **consequence lane**, not the coverage lane:

```
consequence decision:
  UNACCEPTABLE — none established: the plan states it will act outside the worktree
    effect: the plan declares an outward action: release
routed: human-authority-required
```

The architect's plan said *"acquisition and **release** logic remains
unchanged"* — `Weighted.Release`, the semaphore method. `consequence.go`
matches `outwardVerbs` by `strings.Contains` over the lowercased steps and
consequences, and `"release"` is in the list. A deployment verb and a method
name collided, and the collision routed to a human before any knowledge-gap
routing could run.

**This is structural for the frozen subject.** Any honest plan touching a
semaphore mentions `Release`. On this product, Encounter 1 cannot reach the
closure round for `semaphore/semaphore.go` — it is pre-empted every time.

Same defect class as `403` inside a commit hash and `backend is unreachable` in
prose: a closed vocabulary read by substring against free text.

## Second contamination: the architect consulted the wrong graph

```
engine     domain github.com/golang/sync · 1498 triples · authoritative   ✓
architect  awareness_briefing → "unknown domain scope github.com/golang/sync:
           graph domains are [globulario/sensei, globulario/sensei-code]"    ✗
```

The engine's MCP was bound to the fixture graph (`.sensei-code/config.json`,
`:10190`). The **architect agent's** MCP comes from `~/.codex/config.toml`,
which points at `localhost:10122` — **sensei-code's graph**. The investigator's
graph inputs were not the fixture's graph. Its `PREFLIGHT_STATUS_DEGRADED` was
true for the wrong reason, and it was exposed to a domain it must not see.

Instrument defect #15: `proofbench environment` verifies the graph the *engine's*
child reaches and says nothing about the graph the *agent* reaches. The two
diverged silently. proof-v5 died on this shape with the roles reversed.

## What the run did show, for the record only

The architect read the source and stated, as an evidence-bearing repository
claim:

> *Weighted currently stores held weight in `cur` and queued callers in
> `waiters`, with both protected by `mu`.* (semaphore/semaphore.go)

That is the relationship the frozen vocabulary expresses. It was found in the
**plan** stage, unprompted, and never got the chance to become a question
because the closure round never ran. It is not evidence about the investigator
socket — it was not produced by it — and must not be counted as link 2.

## Third onboarding defect, before this one

Attempt 0 failed in one second: `sensei-code` writes `.sensei-code/sessions/` at
startup, the foreign repository has no `.gitignore` for it, and the run refused
its own self-dirtied checkout. Fixed by adding the ignore sensei-code keeps for
itself. A foreign repository cannot run a governed task without this.

## Status

Two contaminations, both pre-investigator, both product-side. Under the frozen
rule, repairing either costs a restart of the experiment — which is the right
price, because this attempt measured nothing about the socket.

Not fixed, pending decision:

1. **`outwardVerbs` false positive** — a production consequence-routing change.
   Not a one-line fix: `"release"` in `"cut a release"` is outward and in
   `"release logic"` is not, and a claim-reader that cannot tell them apart
   either over-escalates (today) or under-escalates (if the word is dropped).
2. **Agent MCP binding** — the engine should hand each agent the graph address
   it verified, rather than trusting the agent's global config. Until then the
   environment check must also verify what the agent reaches, or refuse.
