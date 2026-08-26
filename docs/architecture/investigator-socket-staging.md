# The investigator socket is staged in the consumer, on purpose

## Decision

> **sensei-code hosts the reference implementation of the investigator socket.
> The semantic contract belongs to Sensei v2.**

`V2_ARCHITECTURE_DIRECTION` describes the investigator as a substrate
capability: §4 places it between deterministic extraction and governed
promotion, and §4.1 makes it one of three roles the *architecture* keeps
separate. The graph, `sensei derive`, and promotion all live in
`globulario/sensei`.

The closure round, the recipe writer and the receipt log live in
`globulario/sensei-code`, which is a **consumer** of that substrate. So the
first investigator is a per-consumer instance of something the design places
underneath.

That is deliberate staging — prove the socket in one consumer before it becomes
a contract every consumer inherits — and recording it is the point of this note.
An implicit version of this decision would let sensei-code's orchestration leak
into a contract that has to serve consumers with no worktrees, no coding agents
and no task ids.

## The split to preserve

**Substrate (Sensei v2) — the semantic contract:**

```
ResidualUnknown  →  InvestigationRequest  →  CandidateEvidenceRequest  →  InferenceReceipt
```

Nothing in that chain mentions a worktree, a provider, a prompt or a task id.
`CandidateEvidenceRequest` is §6.2's own vocabulary; a *recipe* is one concrete
encoding of it.

**Consumer (sensei-code) — the execution:**

- how a residual unknown is detected during a governed run
- the prompt that asks an investigator for a question
- which provider executes it, in which worktree, under which task identity
- when the write is attempted, and the future-only rule against a task id

## What must not leak into the contract

- **task id** — the future-only rule is expressed today as "not the task that
  wrote it". The substrate concept is *"not the investigation that proposed
  it"*, and a consumer without tasks still needs it.
- **worktree and repo root** — a path convention, not a semantic.
- **prompt text** — `FeatureExtractorVersion` names it precisely so the contract
  does not have to contain it.
- **provider vocabulary** — a receipt records a model *identity*; that it is a
  hosted LLM behind a CLI is a consumer fact. §6.4 requires the substrate to
  work with no learned layer at all.

## What is already contract-shaped

These were built to the document rather than to this repository, and should move
essentially unchanged if the socket is promoted:

- `derived.Recipe` — a question, not an assertion
- `derived.InferenceReceipt` — §6.3's field set
- `Outcome ∈ {RECORDED, DUPLICATE, REFUSED, NO_PROPOSAL}` — the loop's own
  measurement vocabulary
- `AnchorsFor`'s rule that only `Outcome == Derived` yields coverage — Law 5,
  and the reason a forged question is harmless

## Consequence

Any other consumer of the graph gets none of this today. That is the accepted
cost of staging, and it is the thing to revisit once the cold-start experiment
says whether the socket produces anything worth inheriting.

Reversible: promoting the contract into the substrate later is a move, not a
rewrite, provided the leaks above stay out.
