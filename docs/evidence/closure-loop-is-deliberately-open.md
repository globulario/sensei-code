# Stage 1 does not exist, and the reason is already on the record

The proposed experiment is **Stage 0 ignorance → Stage 1 autonomous learning →
Stage 2 architectural discrimination**. Stage 1 cannot run today, and not
because of a bug.

## The closure round is read-only, by design

From the product's own gap-closure prompt:

> **YOU ARE READ-ONLY IN THIS STAGE.** Do not try to write a proposal: this role
> runs without write permission, deliberately, so that investigation cannot
> become authorship. Establishing DURABLE knowledge — an anchor that survives
> this task and spares the next one — needs a write path this stage does not
> have, and how that knowledge may enter the graph without the graph becoming a
> mirror of what you asserted is an open question
> (`dq.closure_knowledge_admission`).

So the loop is: investigate → verify claims from the repository → **fold them
into this plan only** → forget. It can close a gap *within* a task. It cannot
spare the next one.

`n = 1` is not a slow start. **There is no autonomous writer.** The one recipe
was committed by a human in the same change that introduced the mechanism.

## Your "terrifying" case is not hypothetical — it was tested

You wrote that closure must not become `task difficult → invent invariant →
future task constrained`. The project declared exactly that risk, pre-registered
a falsifiable hypothesis, and ran it. From the epistemic ledger:

> **A**: a true claim supported by repository evidence → accepted.
> **B**: *"the bus mutex exists to serialize concurrent map access"* — plausible,
> well-formed, non-contradictory and **FALSE** → **accepted, identically**.
> **C**: a claim whose evidence cites only files the same change introduced, and
> whose promotion would remove the exact coverage gap blocking the run →
> **accepted, identically**.
>
> The complete validation is: class is known, `status=="candidate"`,
> `confidence!="low"`, evidence is a non-empty STRING, `discovered_from` is a
> non-empty string. It never opens the source file, never checks that the cited
> evidence establishes the claim, and cannot distinguish a true claim from a
> plausible false one.

`sensei promote` validates **form, not evidence**. That is why the write path is
shut: opening it would automate self-approval, which is the failure the project's
own brief names as item 8.

## You independently re-derived alternative 4

Your Encounter-1-stops / Encounter-2-benefits design is `future_only`, declared
2026-08-23:

> Closure knowledge is admitted, but may never be the anchor that covers the
> region it was written to unblock. The current task still stops; the next task
> over that region does not. Self-approval is structurally impossible because the
> author never benefits from its own writing.

It is a good instinct and it is a real alternative. But note what it does **not**
fix: case **B**. An agent that writes a plausible false rule gains nothing now
and still poisons every later task over that region. `future_only` prevents
self-approval; it does not prevent false accumulation.

## The recipe design already solves this — for one class of knowledge

A recipe is **not an assertion**. It is a *question*, and it is worthless until
re-answered against the world being assessed:

```go
if res.Outcome == Derived && res.Anchor != nil {
        anchors = append(anchors, *res.Anchor)
}
```

A recipe that does not derive yields **no anchor and no coverage**. The file
states the consequence outright: *"the worst a forged entry achieves is spending
one derivation."*

This is a sixth option the ledger does not enumerate, and it is better than the
`mechanically_derived_only` alternative it resembles. That alternative was
rejected as *"autonomous but narrow: it cannot state why anything matters."* The
recipe splits the difference:

| | who supplies it | can it be forged? |
|---|---|---|
| **which relationship is worth checking** | judgment — the agent | yes, and it costs one derivation |
| **whether it holds right now** | mechanism — `sensei derive` | **no** |

Judgment about *relevance*, mechanism about *truth*. Self-approval cannot arise,
because writing the question grants nothing — only the derivation does, and the
agent does not control it.

## What this means for the experiment

Stage 1 is buildable **if the closure round may write a recipe rather than a
rule**. Two blockers, and the ledger already separates them:

1. *"the closure round is wired into the ARCHITECT stage, which runs
   `--permission-mode plan` and is read-only by design, so it cannot write a
   proposal at all"* — the ledger calls this **a defect in that PR's wiring**.
2. *"even a written proposal is a candidate, and only promotion plus rebuild
   moves the graph"* — **this is the open question**, and it is what `promote`
   was shown to be unable to police.

Blocker 2 does not apply to recipes. A recipe needs no promotion and no rebuild:
it is inert data revalidated on every read. So the minimal honest unlock is
narrow and does not touch admission at all:

> **Let a closure round that could not close its gap write a recipe — a
> checkable question — instead of nothing.**

Nothing about invariants, contracts or decisions changes. Those keep needing a
human, correctly, until `dq.closure_knowledge_admission` is answered.

## The experiment this makes possible

Run entirely outside the 11-task corpus.

```
Encounter 1   uncovered area, legitimate task
              → NO_ANCHORED_RULES
              → closure round investigates
              → writes a RECIPE (a question), not a rule
              → this task still stops          ← future_only, structurally

Encounter 2   different task, same area
              → recipe revalidates against the world
              → anchor exists → coverage → proceeds autonomously

Control       same two encounters, recipe writing disabled
              → ignorant both times
```

Stage 2 follows: present a change that conflicts with the derived property, and
see whether Sensei moves from *"I don't know this area"* to *"this conflicts
with X"*.

**The falsifier must stay reachable.** If the closure round writes recipes that
do not derive, or that derive but cover nothing anyone needed, that is a
negative result about the loop — not a reason to hand-write better recipes.

## Not done

No production behaviour changed. The recipe-write path is **described, not
built** — it is a production change, and the standing instruction is to change
nothing without a decision.
