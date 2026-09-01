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
