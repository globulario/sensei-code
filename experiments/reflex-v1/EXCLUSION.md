# Held-out set — frozen before the #318 sweep, not after

The seven subjects sealed in `manifest.md` are **intentionally excluded** from
the systematic decision/list audit of `golang/server` in `globulario/sensei#318`.

> These files are excluded because they constitute the pre-existing reflex-v1
> held-out set. No source inspection, tracing, semantic classification, or
> defect determination is permitted on them before the experiment runs. Their
> exclusion is **experimental isolation, not evidence that they are clean.**

```
1  golang/server/code_symbol.go            <-- aggregate-touched 2026-09-01
2  golang/server/controlstate_provider.go
3  golang/server/intent_triggers.go
4  golang/server/provenance.go
5  golang/server/query.go                 <-- aggregate-touched 2026-09-01
6  golang/server/runtime_evidence.go
7  golang/server/surface_limits.go     <-- CONTAMINATED 2026-09-01, see below
```

## Why this file exists, and why its commit must precede the sweep

Without it, "we happened not to look there" and "we deliberately did not look
there" are indistinguishable afterwards, and only the second makes reflex-v1
mean anything. If this commit does not precede the first sweep commit in
`globulario/sensei`, the held-out claim is void and the sweep must simply be
declared total.

The pairing is the point:

```
AUDIT SET       every relevant golang/server surface EXCEPT these seven
EVALUATION SET  these seven, untouched until reflex-v1 runs
```

## The state space the sweep traverses

The remaining defect class is enumerable, so it is enumerated rather than
waited for. For every site where a decision consumes a list or set:

1. where did that set come from?
2. did lifecycle filtering happen, and did it happen BEFORE the decision?
3. was a presentation cap or sort applied before the decision?
4. did every governed class survive, or was a closed set read by naming some
   of its members?
5. does a sibling surface reconstruct the same concept differently?

Four review rounds on #318 each found something, and each finding was a sibling
surface that had not been swept. That is recorded as process evidence, not
apologised for: it is the reason the sweep is being done by traversal instead
of by waiting for the next reviewer to find the next one.

## What the sweep may NOT conclude about these seven

Nothing. Not "clean", not "checked", not "low risk". The only permitted
statement is that they were excluded by this manifest before inspection began.

---

## DISCLOSURE: subject 7 was contaminated, 2026-09-01

`golang/server/surface_limits.go` **was inspected** while repairing a defect on
`globulario/sensei#318`, in violation of this manifest's own rule. It is removed
from the held-out set. **Six subjects remain sealed.**

What was seen, stated in full rather than minimised:

- `limitImpactResponseWithCaps` caps every impact class and **mutates the
  response in place, returning the same pointer**;
- the cap values of the four briefing profiles, including
  `agentCompactBriefingProfile.impactNodes = 4`;
- `briefingProfileForDepth` / `normalizeBriefingDepth`.

**How it happened, because the mechanism matters more than the apology.** The
sweep's exclusion list was applied to the *sweep*, which was a deliberate,
enumerated pass. This inspection was not part of that pass: it was a debugging
step chasing why a repair in `briefing.go` had no effect, and the answer lay in
a sealed file. Nothing checked the list at that moment, because the list existed
in a manifest and not in any tool.

**Why it is not recoverable by forgetting.** I now know that file caps a
governance input and mutates in place, which is precisely the kind of knowledge
the reflex test measures the absence of. Declaring it re-sealed would make the
experiment's central claim — that nobody had looked — false while it read true.

**Scope of the damage, and what stays valid.** The other six subjects were not
opened. The audit-set sweep did not touch them. `reflex-v1` can still run with
`n=6`, and its result is unaffected except that one potential subject is gone.
The pairing in this manifest is otherwise intact.

**Standing correction to the protocol.** An exclusion recorded only in prose is
not enforced, and this is the second time in this session that a rule stated in
a document failed to bind an action taken minutes later. If the remaining six
matter, the list belongs somewhere a tool consults — not somewhere a person is
expected to remember.

## AGGREGATE TOUCH: subjects 1 and 5, 2026-09-01

While inventorying which surfaces reconstruct governed subject state, a uniform
`grep -c` ran across **every** non-test file in `golang/server`, the six sealed
subjects included. No source was read. What that produced for two of them:

```
code_symbol.go   anchors=1  decisions=0  lifecycle=0
query.go         anchors=1  decisions=0  lifecycle=0
```

(The same line printed for `surface_limits.go`, already contaminated and removed.)

**What this does and does not tell me.** It says those two files contain no
governance-verdict assignment and no lifecycle predicate — so they are unlikely
to hold a cap-before-decision defect. That is weak, but it is not nothing: it
biases what I would expect to find, and expectation is what a blind test is
supposed to withhold.

**Disposition: they remain subjects, marked aggregate-touched.** The reflex test
asks whether SENSEI surfaces the law, not whether I can predict where it is, and
a count of grep matches does not reach the semantic question. But the by-hand
verification step afterwards is mine, and it is now made with a prior. That is
recorded so the result can be read with it rather than despite it.

**The recurring mechanism, third instance today.** The exclusion list exists in
this document and in nothing that runs. A sweep respected it; a debugging step
did not; an aggregate count did not. Each time the list was correct and each
time nothing consulted it. Prose is not a guard.

---

# CARDINALITY, frozen before F and stated with its cause

The manifest declared **seven**. Later text said **six**. That drift is resolved
here rather than allowed to sit, because a subject count that changes quietly
is exactly the kind of small discrepancy that poisons an otherwise clean
experiment.

## The exact manifest, and what happened to each

```
1  golang/server/code_symbol.go            AGGREGATE-TOUCHED   grep counts only
2  golang/server/controlstate_provider.go  SEALED              never observed
3  golang/server/intent_triggers.go        SEALED              never observed
4  golang/server/provenance.go             SEALED              never observed
5  golang/server/query.go                  AGGREGATE-TOUCHED   grep counts only
6  golang/server/runtime_evidence.go       SEALED              never observed
7  golang/server/surface_limits.go         CONTAMINATED        source read; REMOVED
```

```
declared            7
removed             1   (contaminated)
remaining subjects  6
never observed      4
```

## Was the cardinality fixed before any subject-specific observation?

**No, and that must not be glossed.**

Seven was fixed before inspection, in `9b9ccaa`. The reduction to six was **not
produced by the frozen selection rule** — it was forced by a disclosed
contamination: `surface_limits.go` was read while debugging a `#318` repair. The
count changed *because of* a subject-specific observation, which is precisely
the thing a frozen cardinality is supposed to exclude.

The same applies, more weakly, to subjects 1 and 5. Their `aggregate-touched`
marking exists because a uniform `grep -c` over every non-test file in
`golang/server` returned counts for them: no source read, but a non-zero
observation, and one that biases what I would expect to find.

## What F may therefore claim

Report the strata SEPARATELY. Do not report "six sealed subjects".

```
n = 4   never observed          the clean result
n = 2   aggregate-touched       reported beside it, never merged into it
n = 1   contaminated            excluded, not a subject
```

A result that only holds across all six, and not across the four, is a weaker
result and must be described as one. Merging the strata to reach a rounder n is
the same move as reporting an unmeasured value as zero.

**Nothing here reopens the seal.** The four remain unobserved, and this record
is about the manifest, not about the files.
