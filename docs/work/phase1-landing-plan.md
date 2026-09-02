# Phase 1 landing: the review boundary and the merge boundary are not the same

Verified mechanically before landing anything, per the rule that a reviewed
exact head stops being the reviewed composition once `main` moves.

## The branches are NOT stacked

```
feat/publication-reachability   base 5d500cf4   5 commits ahead
feat/governed-subject-state     base 5d500cf4   5 commits ahead
feat/baseline-classification    base 5d500cf4   4 commits ahead
feat/prospective-retrieval      base 5d500cf4   2 commits ahead

git merge-base --is-ancestor #321 #322  ->  NO
```

Four independent branches off one base. I built them that way so each would be
independently reviewable, and the cost is that **none contains the others**.

## Every one of them touches the same files

```
docs/awareness/required_tests.yaml              all four
docs/awareness/generated/*.yaml                 all four
golang/server/embeddata/awareness.nt + stamp    all four
docs/awareness/invariants.yaml                  three
```

So merging any one **guarantees** the next needs conflict resolution on authored
YAML plus a regenerated seed. That is not a formality: it is exactly the #320
case, where the conflicting files were authored knowledge and both sides had
appended different, non-overlapping entries. Taking either side there would have
silently deleted governed knowledge that had just been reviewed.

## Therefore landing is four cycles, not four merges

```
for each PR in dependency order:
    merge main into the branch
    resolve authored YAML as a UNION, never by choosing a side
    regenerate every generated artifact from the merged sources
    verify an entry from EACH side survived
    CI green at the new head
    exact-head review of the NEW head
    merge only on that verdict
```

The step that is easy to skip is the last one. A clean verdict on the
pre-merge head does not transfer across a resolution I performed — the
resolution is mine, unreviewed, and in #320 it was the part that contained the
findings.

## What this costs, stated plainly

Roughly four review rounds rather than one. That is the price of having built
four independently reviewable changes instead of one stack, and it is the right
price: a stack would have made every review depend on its predecessors landing
unchanged.

## Ordering

```
A  #321 reachability          nothing depends on it, and everything is measured against it
B  #322 subjectstate
C  #323 baseline classification
E  #324 prospective            see the ownership decision below
   sensei-code #138 / #139 / #140
```

`sensei-code` has no cross-branch file overlap with these, so its three land
independently.

## Measured composition risk (2026-09-01)

The ordering above was chosen on reasoning. Here is the measurement, taken by
diffing each branch against its merge-base with `main`:

**The Go code is disjoint across all four.** `#321` owns
`golang/reachability` plus the two emitters; `#322` owns `golang/subjectstate`
plus `change_impact.go`; `#323` owns one test file under `cmd/awg`; `#324` owns
`golang/prospective`. No pair touches the same Go file.

**Every pair overlaps anyway, and the overlap is entirely generated truth:**

```
docs/awareness/generated/awareness_graph_annotation_report.yaml
docs/awareness/generated/awareness_graph_code_symbols.yaml
docs/awareness/generated/awareness_graph_go_import_graph.yaml
golang/server/embeddata/awareness.nt
golang/server/embeddata/awareness.transaction.tsv
```

plus the append-mostly sources `invariants.yaml` and `required_tests.yaml`.

Three consequences, none of which ordering can avoid:

1. **The regeneration round is not a contingency, it is the plan.** Merging any
   one of these leaves the other three conflicting on generated files. That is
   true for all 4×3 orderings, so no sequence avoids it. Each of the three
   remaining PRs will need a new head, CI, and a fresh exact-head review.

2. **The conflicts must not be resolved by hand.** Generated truth is
   reconciled by regenerating it from the union of the sources — never by
   editing the artifact until it merges. A hand-merged `.nt` file is a graph
   nobody derived, asserting whatever the resolution happened to produce.

3. **The source YAML may be union-resolved, and only that.** Each PR appends
   distinct entries to `invariants.yaml` and `required_tests.yaml`; taking both
   sides is the correct resolution there precisely because the entries are
   disjoint. If two PRs ever edited the SAME entry, this shortcut would be
   wrong, and the check is to confirm disjointness rather than to assume it.

So the real question each landing must answer is not "does it merge" but: **is
the regenerated graph the union of what the four reviewers actually saw?** The
code composition is provably not at risk; the derived composition is the whole
of it.

### Disjointness, confirmed rather than assumed

Every `id:` added to `invariants.yaml`, `required_tests.yaml`, and
`failure_modes.yaml` by each of the four branches, intersected:

```
#321  1 failure_mode + 4 required_tests   (reachability)
#322  1 invariant    + 7 required_tests   (subjectstate, change_impact)
#323  1 invariant    + 2 required_tests   (baseline classification)
#324  1 invariant    + 4 required_tests   (prospective)

collisions: none
```

So union-resolving the sources is legitimate **for these four specifically**.
The check is cheap and must be repeated for any later set, because the shortcut
is not a property of the file format — it is a property of these entries
happening not to touch each other.

## What landing #321 actually cost (2026-09-01)

Three things the plan did not anticipate, recorded because the next landing
will meet all three.

### 1. An automated commit moves main's corpus after every merge

`chore(awareness): auto-rebuild seed + stamp from authored corpus` lands on
`main` after a merge and regenerates the derived corpus. So the moment #321
landed, **every open branch was behind main's corpus**, and the reachability
check correctly reported the branch's serving graph as unordered against the
admitted one.

The consequence is structural, not incidental: **every open PR goes red after
every merge**, and stays red until it takes main again. That is the reachability
check doing exactly its job — the branch really is not current — but it means
the cost of landing one PR is a forced update of all the others.

I first read that red as *non-determinism*, because the same test passed on one
branch and failed on another with no logic change between them. It was not
flaky. The runs straddled the bot's commit. I was reading the clock, not the
code, and said so before checking whether `origin/main` was what I thought it
was — it was not; my local ref was stale by one commit.

**The check to run first:** `git fetch origin && git rev-parse origin/main`,
before concluding anything about a reachability failure.

### 2. The union rule has to survive batching

The rule was written above, before the landing began: **sources union-resolve,
generated truth regenerates.** I applied it correctly by hand on #322, then
wrote a loop that resolved all four conflicts on #323 and #324 with
`git checkout --ours` — correct for the three generated artifacts, wrong for
`required_tests.yaml`.

It silently dropped #321's `required_test` entries from both branches, and the
seed regenerated from that truncated source, producing **a graph missing
knowledge that had already landed**. Caught by checking for two specific
landed ids rather than by trusting the merge, and repaired by union against
`origin/main`.

The lesson is not "be careful". It is that a rule with two branches applied by
hand becomes one branch when it is batched, and the batch is where it breaks.
Verify the landed ids are present after every merge; the check is one `grep`.

### 3. Reviewer quota is the real rate limit

The window granted **one** review. #321 landed on it; #322's request bounced
minutes later. At that rate the remaining PRs cost one window each, and every
landing forces an update-and-reverify of all the others — so the queue does not
drain at one per window, it drains more slowly than that.
