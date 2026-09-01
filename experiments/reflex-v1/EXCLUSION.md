# Held-out set — frozen before the #318 sweep, not after

The seven subjects sealed in `manifest.md` are **intentionally excluded** from
the systematic decision/list audit of `golang/server` in `globulario/sensei#318`.

> These files are excluded because they constitute the pre-existing reflex-v1
> held-out set. No source inspection, tracing, semantic classification, or
> defect determination is permitted on them before the experiment runs. Their
> exclusion is **experimental isolation, not evidence that they are clean.**

```
1  golang/server/code_symbol.go
2  golang/server/controlstate_provider.go
3  golang/server/intent_triggers.go
4  golang/server/provenance.go
5  golang/server/query.go
6  golang/server/runtime_evidence.go
7  golang/server/surface_limits.go
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
