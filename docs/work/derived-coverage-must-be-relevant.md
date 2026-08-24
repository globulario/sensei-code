# Implementation brief: derived truth must be relevant to the gap it closes

## Why this exists

The first two derivation families proved that Sensei can establish narrow technical facts from project reality. A later survey exposed the mirror-image failure mode:

> A proposition can be true, independently derivable, and still be architecturally useless for the uncertainty that blocked the task.

The preserved adversary is a broad layering fact such as:

```text
package P does not import net/http
```

It can be 100% true and 100% derivable while saying almost nothing about the architectural question that caused `CLOSE_GAP`. If any derived fact over a file is allowed to satisfy coverage, an agent can manufacture autonomy honestly by selecting cheap, wide, irrelevant truths.

The rule for this slice is:

> Truthfulness is necessary for coverage, but not sufficient. A derived anchor may close only the specific architectural uncertainty it actually resolves.

Do not optimize family selection for reach.

## First task: falsify the current path before designing a solution

Build an adversarial test/harness that asks whether the current router could be made to accept a trivially true but irrelevant derived proposition as sufficient coverage for a real knowledge gap.

Do not add a permanent derivation family merely to create the test. A test-only deriver/fake is acceptable if it exercises the same consumer boundary and cannot leak into production.

The test must distinguish:

```text
P is DERIVED
```

from:

```text
P resolves gap G
```

If the current architecture already prevents the adversary, document exactly which existing mechanism does so and pin that mechanism. Do not build a new relevance engine without a failing specimen.

## If the adversary succeeds

Implement the smallest machine-owned sufficiency relation that blocks it. The exact representation is not prescribed. It must satisfy these constraints:

- The claimant/provider cannot self-label a proposition as `relevant` or `sufficient` and thereby gain authority.
- The unresolved knowledge gap must have a structured identity/requirement owned by the control plane or Sensei response, not free-text invented after the proposition is known.
- A `CoverageAnchor` must carry/derive which gap requirement it can satisfy.
- Subject overlap alone is insufficient. A fact about a file does not imply understanding of every architectural question involving that file.
- A true negative layering fact must not close an ownership, locking, provenance, contract, or purpose gap merely because its subjects overlap.
- Existing lock-discipline closure for `internal/event/bus.go` must continue to close the gap it was actually introduced to answer.
- The refuted git-confinement specimen remains `NOT_DERIVED`; do not shop for another proposition until one passes.

Prefer a narrow set of machine-checkable gap/anchor relationships over a natural-language model judge.

## Family admission rule

Add a test or documented gate for future derivation families:

1. What real unresolved architectural question does this family answer?
2. What subjects does a successful proof establish?
3. What uncertainty is still outside its claim?
4. Can a trivial/wide true proposition in the family manufacture coverage?
5. Does the family still produce useful `NOT_DERIVED`/`UNKNOWN` outcomes, or was it tuned until it passes?

Reach is only a tiebreaker after architectural significance and soundness.

## Required attacks

- wide true but irrelevant layering proposition -> no unrelated coverage;
- proposition over same file but wrong relation kind -> no closure;
- proposition whose subjects partially overlap the planned files -> only the actual resolved gap/subjects count;
- provider-supplied `relevant=true` or equivalent metadata -> ignored/refused as authority;
- lock-discipline happy path -> still closes its matching gap;
- false/refuted confinement -> no closure;
- unread/unproved gap identity -> `CannotEstablish` or remains open, never guessed.

## Non-goals

- Do not build a universal semantic relevance ontology.
- Do not use another LLM's agreement as the establishing relevance test.
- Do not add family 3 merely to improve the coverage statistic.
- Do not treat file reach as architectural significance.
- Do not weaken current truth/derivation guarantees to make relevance easier.

## Success criterion

A derived proposition can influence coverage only when the system can establish that it answers the specific unresolved knowledge requirement. A wide, true, independently derived but irrelevant proposition cannot make an otherwise unknown task look understood.
