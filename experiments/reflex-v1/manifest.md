# reflex-v1 — does a published law fire without a human pointing at it?

> Can a reviewed, published law learned from Sensei's own failures later fire
> against a fresh self-change without a human pointing at the defect class?

Everything found in `globulario/sensei#318` was found because attention was
directed at `golang/server` — by the owner, by the reviewer, or by me reading
line by line. That is a repaired system, not a self-improving one. The cycle
under test is the last arrow only:

```
experience -> generalized law -> durable knowledge -> LATER AUTONOMOUS APPLICATION
```

## Frozen before publication, and that ordering is the point

The three entries recorded at `b1cf9c69` are authored but **unreachable**: no
run can consult them until the store is rebuilt. This manifest is committed
**before** that publication so the experiment cannot be tuned to the law it is
testing. If this file's commit does not precede the publishing commit, the run
is void.

## The flaw in the obvious subject, and why the subject changed

The natural candidate was `matchPatternsForBriefing` in
`golang/server/implementation_patterns.go` — raised during #318 and never
traced. It is disqualified: that file is named in `source_files` of
`invariant.sensei.decisions_do_not_rest_on_presentation_ordering_guarantees`.
A briefing there returns the law **because the file is anchored to it**. That
is a graph lookup working correctly, and it proves nothing about judgment.

So the tiers are separated, and only the second is the experiment:

```
TIER 1  RETRIEVAL     an ANCHORED file surfaces the law.
                      Necessary (proves publication worked), never sufficient.
TIER 2  APPLICATION   an UNANCHORED file with the same shape surfaces the law.
                      This is the reflex.
```

## Subjects, selected mechanically, sealed before any is opened

Rule: every non-test `golang/server/*.go` that declares a `max*` integer
constant AND is named in no `source_files` of the three entries, in path
order. No file is skipped for looking unpromising; the full ordered list is
the subject set.

```
1  golang/server/code_symbol.go            maxConventionGroups = 8
2  golang/server/controlstate_provider.go  maxCatalogClassFetch, maxDiscoveryNodes
3  golang/server/intent_triggers.go        maxIntentsPerTaskMatch = 3
4  golang/server/provenance.go             maxCitationsShown = 3
5  golang/server/query.go                  maxQueryLimit, maxScopedListFetch
6  golang/server/runtime_evidence.go       maxEvidenceSurfaced = 2
7  golang/server/surface_limits.go         seven maxSurface*/maxBriefing* caps
```

TIER 1 control: `golang/server/preflight.go`, which is anchored by all three.

**I have not traced any of these seven.** They were listed by grep and sorted;
whether any contains a cap-before-decision shape is unknown to me at freeze
time and is deliberately left unknown. Determining it first would convert the
experiment into "does Sensei agree with a conclusion I already reached".

## The task text, frozen verbatim

Run against each subject, unchanged, with no additional prose:

```
Reduce duplication in this file and simplify its helper functions.
```

It names no cap, no presentation, no ordering, no decision, and no governance.
Task prose leaks into results, so this string is part of the seal: if it is
edited after any run, the run is void and the label restarts.

## Outcomes, frozen

```
PASS            the law surfaces on >=1 unanchored subject, AND that subject
                is afterwards verified BY HAND to contain a cap-before-decision
                shape. Verification happens after the run, never before.
MISS            a subject verified to contain the shape did not surface the law.
FALSE_POSITIVE  the law surfaced on a subject verified NOT to contain the shape.
                Recorded, not discarded: a law that fires everywhere is not
                judgment, and reach is not relevance.
NO_SUBJECT      none of the seven contain the shape. Inconclusive. No claim of
                either success or failure is permitted from this outcome, and
                nothing is edited to manufacture a subject.
```

TIER 1 failing (the anchored control does not surface the law) means
publication did not take, and voids the run rather than scoring it.

## What a PASS would and would not license

It would license: "a law generalized from Sensei's own failures, recorded and
published, later identified an instance of that failure class in code no human
had pointed at." It would NOT license "Sensei improves itself" — application is
not repair, and the repair would still be proposed, reviewed and admitted
through the ordinary lanes.

One instance is one instance. The claim is the loop closing **once**.
