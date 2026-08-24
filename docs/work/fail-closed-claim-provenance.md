# Implementation brief: fail closed on claim provenance

## Why this exists

A live `sensei-code observe` audit found that the premise checker treats only the literal source value `inference` as unverified. Empty, unknown, or alternate reasoning labels can therefore pass through as though they carried repository-grade provenance.

That is the wrong default for a mechanism whose purpose is to stop load-bearing unverified premises.

The rule for this slice is:

> Unknown or unrecognised provenance must never be interpreted as stronger provenance.

Do not fix this with prompt wording. The authority decision must be enforced in workflow code.

## Required investigation before editing

1. Enumerate every claim/proposition source value actually emitted today by all architect/provider paths.
2. Identify which values are backed by repository observation, graph evidence, executable observation, or another independently checkable source.
3. Identify which values are inference/reasoning/assumption or otherwise claimant-controlled.
4. Record the observed vocabulary in the PR before choosing the implementation shape. Do not invent a taxonomy first and force existing producers into it.

## Required behavior

- Explicitly recognised evidence-bearing provenance may participate in technical assessment.
- `inference`, empty source, unknown values, misspellings, future unrecognised values, and reasoning-only values must fail closed as unverified unless a stronger independent evidence object establishes the premise.
- An unverified premise is a bounded technical knowledge gap when it is technically closable. It must not become human authority merely because its provenance is weak.
- More confident wording must not upgrade provenance.
- Provider-controlled labels must not be sufficient to choose the stronger trust lane.

Prefer a small classifier/typed conversion owned by the engine over scattered string comparisons. The exact type is not prescribed. Derive it from the current producer vocabulary.

## Attacks that must be pinned

At minimum, table-drive the premise gate with:

- the exact currently trusted repository/evidence source values discovered in the investigation;
- `inference`;
- `""`;
- `assumed`;
- `reasoning`;
- an arbitrary future value such as `model_certified`;
- case/whitespace variants if the current protocol treats source labels case-insensitively.

The important assertion is route-level, not merely classifier-level: no unrecognised claimant-controlled source may reach a granted technical route solely because the string was not `inference`.

Add one test showing that a real repository-backed premise still proceeds, so the fix does not turn every premise into a knowledge gap.

## Live proof

Run one real headless/governed planning path that emits a premise with repository-backed evidence and one path/specimen with reasoning-only or unknown provenance. Capture the structured route result.

Expected:

```text
repository-backed premise  -> ordinary assessment may continue
unknown/reasoning premise  -> bounded knowledge gap / cannot establish as appropriate
```

Do not parse prose to decide this.

## Non-goals

- Do not build a universal claim ontology.
- Do not make another model the provenance authority.
- Do not ask a human to certify ordinary technical premises.
- Do not weaken the premise check to preserve the current route distribution.
- Do not add a flag that lets a caller assert stronger provenance.

## Success criterion

A claimant can choose what proposition to make, but cannot choose a spelling that causes the engine to treat an unverified premise as repository-grounded. Unknown provenance remains visibly unknown and routes accordingly.
