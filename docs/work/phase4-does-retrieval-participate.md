# Phase 4: does prospective retrieval participate in the ordinary path?

**No. It participates in nothing.**

Verified 2026-09-01 on `globulario/sensei`, branch `feat/prospective-retrieval`:

```bash
grep -rn "golang/prospective" --include=*.go . | grep -v "^./golang/prospective/"
# (no results)

grep -rn "prospective.Retrieve" --include=*.go . | grep -v _test.go
# (no results)
```

`golang/prospective` is imported by **nothing outside its own package**, and
`Retrieve` is **never called in production**. E built the machinery, measured it
in a harness, and wired it into no decision surface.

## Why this matters more than it sounds

The document asks the right question — not *can the API return related laws*,
but *does the ordinary workflow consult prospective knowledge early enough to
affect reasoning*. The answer changes what F could have shown.

Had F run after A–E landed, a `0/4` result would have been **ambiguous in
exactly the way A–E was built to prevent**: reflex could have failed because the
law was unreachable, or because nothing ever asked for it. The second is true
today, and it is not a reflex failure at all.

**Phase 4 exists to catch this, and it caught it.** That is the most useful
thing this check produced.

It also explains the campaign baseline without appealing to model behaviour:

```
autonomously surfaced prior laws = 0
```

is not evidence that retrieval ranked badly, or that applicability was too
strict. **Nothing called it.** The zero is structural, and it would have stayed
zero after publication.

## What wiring it actually requires — the blocking gap

`Retrieve` needs anchors *other than the subject's own*:

```go
Retrieve(subject, anchors []Anchor{ID, Class, Files, Domains}, subjectDomains)
```

The ordinary path cannot supply them. Preflight resolves impact **for the
requested files** (`collectImpact(ctx, file, requestedDomain)`), so it holds
anchors *for this subject* and has no query for anchors *of other subjects*.
There is no store query returning anchors by scope or domain.

So the missing piece is not ranking, not applicability, and not a learned layer:

```
MISSING: a query that answers "which governed anchors have scope NEAR this
         subject" — by shared authority domain, sibling path, or component —
         without already knowing the answer.
```

Until that exists, retrieval has no input in production and the recall figure
(0.34, one signal) describes a harness rather than a system.

## What must NOT be concluded from this

That E was wasted. It established the type discipline — `matched ≠ applicable`,
basis as a closed set, no numeric score to threshold — and produced a measured
baseline. Those hold. What it did not do is participate, and the two were
conflated in my own reporting of it: I described E as *"prospective retrieval"*
delivered, when what shipped was prospective retrieval **available**.

```
built      ≠ wired
wired      ≠ consulted
consulted  ≠ early enough to affect reasoning
```

Same shape as everything else this program has found.

## Consequence for the ordering

F stays blocked on more than publication. Running it against a system where
nothing consults retrieval would produce a result whose only honest reading is
"the wiring is absent" — which is knowable now, for free, without spending the
sealed subjects.

**A sealed population is a finite resource. It should not be spent answering a
question a grep can answer.**

---

# CORRECTION, same day: the answer above is true and misleading

Written after checking what the ordinary path already does, which I should have
done before writing the section above.

## A prospective-knowledge mechanism already exists and is wired

`golang/server/package_inference.go`:

> `inferPackageAnchors` finds anchors carried by OTHER files in the same package
> directory, **excluding anything already anchored directly to this file**.

That is the same idea as `prospective.Retrieve`'s same-directory signal,
including the exclusion of direct anchors. It is computed by `collectImpact`,
returned as its fourth value, and **briefing consumes it** — `!inference.empty()`
produces `BRIEFING_STATUS_INFERRED_ONLY`.

The store primitive it needs already exists too: `ImpactForPackage(ctx,
pathPrefix)`. The "missing query" I named above is not missing.

## What IS true, and it is narrower and more useful

```
briefing     consumes package inference        surfaces it as INFERRED_ONLY
preflight    DISCARDS it                       impact, _, _, _, err := collectImpact(...)
```

**Preflight — the surface that produces the governance verdict — throws away
sibling knowledge that `collectImpact` already computed for it.** The fourth
return value is discarded at `preflight.go:149`.

So prospective knowledge does participate, in the surface that *advises*, and
not in the surface that *decides*.

## And E duplicated it

`golang/prospective` is a second implementation of a mechanism that already
exists and is already wired. I built a new layer instead of making existing
machinery participate — the exact inversion of the ordering the commissioning
document specifies:

> reuse existing machinery → strengthen shared abstraction → improve
> deterministic applicability/retrieval → only then consider additional learned
> machinery

That is a semantic-compression regression, introduced by the step meant to close
a capability gap, and found only because Phase 4 asked whether the thing
participates rather than whether it works.

## What this changes about the next repair

Not "build the missing query". The candidate repair is now much smaller and
targets participation rather than capability:

```
preflight consumes the packageInference collectImpact already returns,
as GUIDANCE with provenance, never as authority
```

And the open question about `golang/prospective` is whether its type discipline
(`Basis` closed, `AuthorityEligible` a method, no numeric score) should be
applied TO the existing inference mechanism — or whether the package should be
withdrawn as duplication. That is an owner decision, not mine to take by
merging.

## The honest shape of my own error

```
I asked   "does MY mechanism participate?"        answer: no
I claimed "does ANY mechanism participate?"       answer: yes, one does
```

Same collapse this program keeps finding: a narrower question answered, a
broader claim reported.
