# Phase B checkpoint — 2026-08-27

> **Investigator-to-deterministic-evidence boundary demonstrated across TRUE,
> FALSE and UNRESOLVED outcomes on external fixtures.**

## The claim, at the strength the evidence supports

On two repositories unfamiliar to Sensei — `golang/sync @ 3ffd83cb` and
`golang/groupcache @ 2c02b82`, each pinned, each onboarded by deterministic
extraction alone — an investigator converted cold architectural gaps into
durable, mechanically re-checkable questions, and the deterministic substrate
decided what those questions earned:

```
TRUE      Weighted.waiters under mu   → DERIVED    → anchor  → routing changed → task ran to an accepted candidate
FALSE     Group.err under errOnce     → REFUTED    → nothing → cold
ENVELOPE  cache.nbytes under mu       → UNRESOLVED → nothing → cold
```

The investigator may propose; the deterministic substrate establishes what is
supported; authority moves only when evidence earns it. No component collapsed
into another (V2 §4.1).

This is **external-fixture validation of the Phase-B safety mechanism**. It is
**not** V2 validation: §8.3 asks for unfamiliar maintainers, agents without
project-specific instructions, real tasks with architectural consequences, and
independent usefulness review. We wrote the tasks and adjudicated the answers.

## What is established

- The safety contract, all three sides, at governed-run level, on natural code.
  The ENVELOPE case is the strongest of the three: the relationship is true by
  inspection, and Sensei said *cannot prove* rather than either *true* or
  *false* — a proof boundary reached, not softened.
- The investigator's wrong belief in the FALSE arm was **proposed by the
  investigator itself** and did not reach the graph.
- Compounding, once: a question written in Encounter 1 changed the routing of
  Encounter 2, with a cold control. The question re-derived `DERIVED` on the
  candidate it enabled.
- Every safety property observed rather than argued: future-only, fail-closed
  derivation, fail-closed relevance, no authority moved by assertion.
- Repeated investigator behaviour: five distinct true, derivable questions
  across two repositories, none told. Not statistical; no longer a one-off.

## What is not established

- **That the reader is sound over call shapes it never exercised.** Review of
  `globulario/sensei#310` found, and local reproductions confirmed, three ways
  a false discipline could still earn `DERIVED`: a package-level (non-method)
  caller invoking a never-locking helper unlocked is skipped and counts as no
  call site; `go s.helper()` and `defer s.helper()` inherit the lock state at
  the statement rather than at the moment the helper runs; and the promotion
  gate's notion of "the claimant's change" is the commit that added the
  candidate file, so an uncommitted candidate makes every citation look
  independent. None of the three shapes appears in the specimens above, so the
  observed TRUE / FALSE / ENVELOPE results stand for their specimens — but the
  broader claim "the reader is sound" does not, and must not be read into them.
  Also: `#309`'s status parser attaches a `status:` that precedes its own `id:`
  to the *previous* entry, which both re-creates the defect and excuses a
  canonical invariant — a fail-open in the closure check itself. Both PRs are
  blocked on tests covering these exact shapes; nothing lands until then.
- Generality (§8.3). Two small, disciplined libraries; one vocabulary family
  exercised end to end (`command_invocation_confined_to` not yet run).
- Anything about the 11-task internal benchmark, whose result stands: governed
  is slower and refuses more on sensei-code's own corpus.
- The implementor and reviewer beyond three accepted candidates.
- That the default product does this. `globulario/sensei#309` and `#310` (with
  `feat/second-derivation-family`) are open and **blocked on the review
  findings above**; the reproductions are posted on the PRs as failing shapes
  to target. Until they land, the demonstrated behaviour lives behind
  `SENSEI_BIN`, and it lands only after those counterexamples are covered.

## What it cost — recorded because it is information

Roughly twenty defects between "the loop exists" and "the loop was observed":
two upstream substrate fixes (candidate closure; tri-state flow-sensitive
reader plus a stack overflow), one promotion gate that existed unwired, six
sensei-code product fixes (consequence keyword, agent graph binding, blind-spot
coverage wiring, prompt v4, capacity gate, consumer outcomes), seven onboarding
frictions, and four errors of mine (over-broad claims, a wrong task, a
mis-attributed cause, a build on the wrong branch). Every failure so far was
deterministic, locatable and fixable; none pointed at the premise.

## The corpus this leaves behind

Append-only, provenance-stamped, per run: gap → question → receipt
(`RECORDED/DUPLICATE/REFUSED/NO_PROPOSAL`) → recurrence → derivation verdict
(`DERIVED/REFUTED/UNRESOLVED/UNKNOWN`) → coverage effect → routing → execution
outcome. This is the Phase-C training material the design asked Phase B to
produce, generated by the architecture rather than manufactured.

## Next, on the V2 line, in order

1. Land #309 and the #310 feature line.
2. Add deterministic relationship families structurally unlike locks.
3. Repeat on further foreign repositories, ideally with §8.3's other conditions.
4. Keep the corpus growing.
5. Only then, Phase C ranking. No learned layer before that.

## Experiments

`experiments/coldstart-v1` · `coldstart-link5-v2` · `coldstart-link6-v1` ·
`safety-v1` · `envelope-v1` · `benchmark/proof-v6` (FINAL_VERDICT, unchanged)
