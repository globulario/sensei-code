# proof-v2 — superseded, preserved as evidence

Second harness-blocking defect, second preserved slice.

proof-v2 repaired the infrastructure classifier (see `../proof-v1/README.md`)
and its one recorded attempt is therefore correctly classified — but it is still
wrong, for a different reason:

```
terminal workflow.failed
oracle   INCORRECT
wall     1s
```

One second, and no provider ever ran. The cause:

> the canonical checkout /tmp/pbdiag has uncommitted changes; a governed
> candidate cut from HEAD would omit them, so it would govern a state you are
> not looking at. Commit or stash them, then run this again

The harness withheld the task oracle by **deleting** the test files from the
worker's checkout. That left the checkout dirty, and a governed run correctly
refuses to start in a dirty checkout. Two correct rules — *withhold the oracle*
and *never govern a state you are not looking at* — composed into an arm that
could not run at all.

The product rule is right and was not weakened. The harness now **commits** the
withholding inside the isolated worktree, and records the derived run base
alongside the manifest's pinned base, because they are different commits and
reporting the pinned one as what ran would misstate which tree the provider saw.

`oracle INCORRECT` here is also a true reading that means nothing useful: the
oracle ran against a checkout no worker had touched, so of course it failed.
An instrument that scores an arm which never executed is not measuring the
product, and that is why this slice is superseded rather than counted.

The campaign continues in `../proof-v3/`. Corpus, bases, oracles and thresholds
are unchanged across all three versions; only the harness changed.
