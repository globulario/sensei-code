# Does `golang/prospective` own a semantic fact inference must not own?

**No. Fold it in.**

The question asked was the right one, and the honest answer is that the package
fails it.

## What each side actually owns

```
packageInference (exists, wired, consumed by briefing)
    nodes          the candidate anchors
    class          their governed class
    from           WHICH SIBLING FILE each came from      <- provenance
    Unavailable    the retrieval could not run
    Reason         why not                                 <- typed failure

golang/prospective (built, wired to nothing)
    Basis          established relationship vs resemblance <- THE ONLY NEW THING
    Signal         why it surfaced
    AuthorityEligible  unforgeable, method on the basis
```

Inference already carries provenance and a typed unavailable state. The
retrieval itself — sibling anchors excluding the subject's own — is the same
operation, computed by the same store primitive.

**The one thing prospective adds is the typed authority boundary.** That is a
property of a candidate, not a lifecycle, and it can live on inference.

## The signal difference is not an ownership claim

`prospective` also computes a shared-authority-domain signal that inference does
not. That is a **capability** inference lacks, not a semantic fact it must not
own. Adding a second signal to an existing mechanism is smaller than maintaining
a second mechanism beside it.

And the honest reading of the remaining difference — that prospective retrieval
"happens earlier" — is **sequencing, not architecture**.

## Decision

Fold the type discipline onto inference and remove the package. Preserve the
distinctions, not the boundary that carried them:

```
origin = prospective
why_candidate            (inference has `from`; extend, do not duplicate)
governed_identity
applicability_state
provenance
basis / authority-eligibility   <- moved, unforgeable, unexported
```

and keep asserted, wherever they end up:

```
prospective match  !=  applicable
applicable         !=  authoritative
inference          !=  admission
```

## Why I am recording this rather than doing it now

The fold changes `golang/server` and would create a fifth branch overlapping the
same authored YAML and seed as the other four — adding a fifth landing cycle to
a queue that already needs four. It belongs **after** Phase 1 lands, as its own
reviewable change.

Recording it now so the decision is not re-litigated later, and so #324 is
landed knowing it is scheduled for absorption rather than as a permanent
package. That is a materially different thing to merge, and a reviewer should
see it stated.

## The test this creates

If this campaign is working, the fold should show:

```
capability   same or up   (the domain signal survives, and now participates)
mechanisms   down by one
```

Capability up while mechanism count goes down is the measurable claim. If the
fold turns out to cost capability, that is evidence the package owned something
after all — and this document is wrong rather than the plan.
