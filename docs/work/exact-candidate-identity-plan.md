# Exact Candidate Identity — the implementation plan

Decisions taken by the owner, recorded before any code:

```text
1. Candidate first parent   EXACTLY BaseCommit
2. CandidateState PRESENT   actual measured work, never worktree existence
3. Commit identity          canonical Sensei Code identity, mechanically
                            reproducible from (base, reviewed tree)
4. Publication              never commits again
```

> The commit gives the candidate a name. The receipt proves what that name
> refers to. Admission decides whether that name deserves to survive.

## The object

```text
        w1 → w2 → w3     execution provenance, recorded, not durable
       /
B ─── C                  canonical candidate identity

C^1        == B                 exactly the governed base, never w3
C^{tree}   == the reviewed tree, built from B plus exactly the reviewed paths
```

A worker's own commits are implementation history. They do not determine the
identity of the object considered for admission, and the previous worker tip is
recorded as provenance rather than retained as evidence. Nothing claims worker
history is durable; if it becomes unreachable, no claim is lost.

`C` is not a "governance commit". Creating it grants nothing.

## Determinism, so the object is its own proof

An author string is not proof — anyone can write one. The proof is that `C` is
**reconstructible**: given `B` and the reviewed tree, an independent
implementation must produce the same SHA.

```text
parent      B
tree        the reviewed tree
author      Sensei Code Candidate Identity <reserved non-person address>
committer   the same
dates       derived from B's commit time, +1 second, fixed +0000 offset
message     derived only from (B, tree) -- no task id, no reviewer, no plan
```

The task id, reviewer, plan digest and verdict live in the **receipt**.
Putting them in the commit would give identical candidate content different Git
identities merely because a different task produced it.

Verifier, requiring no signature and no trust in local Git config:

```text
reconstruct C' from (B, reviewed tree)   ->   C' == recorded CandidateCommit
```

## The transition

```text
worker finishes
  ↓ capture the final candidate against B      (CandidateCapture)
  ↓ audit + review the exact digest D
  ↓ ACCEPT
  ↓ re-capture; the digest must still be D     (nothing moved after review)
  ↓ build the canonical tree: B's tree, plus exactly the reviewed paths
  ↓ mint C deterministically; point the candidate branch at it
  ↓ verify: C^1 == B, C^{tree} == the built tree, C reconstructs
  ↓ noteCandidateCommit(C, tree, first parent)
  ↓ emitRunTerminal  ->  COMPLETE / ACCEPTED for the first time
  ↓ disposition, publication, admission -- each a separate decision
```

## Mechanics

Built in a **temporary index** so the live worktree index is untouched
(`CandidateCapture` mutates it with `add --intent-to-add`, and the two must not
interfere):

```text
GIT_INDEX_FILE=<tmp>  git read-tree B
GIT_INDEX_FILE=<tmp>  git add --all -- <reviewed paths>     # scoped: handles
                                                            # adds, edits and
                                                            # deletions, and
                                                            # sweeps nothing
GIT_INDEX_FILE=<tmp>  git write-tree                        # -> T
GIT_AUTHOR_* GIT_COMMITTER_* fixed
                      git commit-tree T -p B -m <message>   # -> C
                      git update-ref refs/heads/<branch> C
```

`add --all` is scoped to the reviewed path list, so an excluded artifact or a
worker's scratch file cannot enter `C` — the property `publish.CommitArgs`
already protects, kept.

## The risk this plan is most likely to break on

**`digest(diff(B,C))` may not equal `D`.** They are produced by different
mechanisms: `D` comes from `CandidateCapture`, which runs
`git add --intent-to-add -- .` and diffs the **working tree** against `B`
through the artifact boundary; `diff(B,C)` is a commit-to-commit diff. Equality
is plausible and is **not assumed**.

The plan therefore verifies, in descending strength:

```text
1. C^{tree} == the tree built from B + reviewed paths        MUST hold
2. changed-path set of diff(B,C) == reviewed path set        MUST hold
3. digest(diff(B,C)) == D                                    MEASURED, and
                                                             recorded as a
                                                             measurement
```

If (3) does not hold in practice, the receipt records both digests and says
they are produced by different mechanisms. It does **not** force them equal, and
it does not quietly redefine `CandidateDigest` to mean whichever one matches.
That would be the false equivalence this chain keeps repairing.

## Schema: v3

`CandidateState` changes meaning — `PRESENT` becomes *measured work*, not
*worktree exists*:

```text
NONE      candidate content was measured and there is no work
          (Evidence.ProducedNoWork already states exactly this)
PRESENT   candidate content was measured and differs from B
UNKNOWN   the run cannot yet say
```

`noteCandidateCreated` moves from worktree creation to capture. **No empty
commit is ever minted** to satisfy `PRESENT`: a run that produced nothing is
`NONE`, and minting a specimen to satisfy an axis is the failure this whole
chain is about.

Redefining a v2 axis silently would be the v1→v2 mistake repeated, so the
emitted schema becomes **v3**.

## Publication stops committing

```text
candidate identity owner   MAY mint the candidate commit, exactly once
publish                    MUST NOT mint one
                           MAY push an existing C, after verifying branch HEAD == C
```

`publish.Open` loses its commit path; `ErrCommitNotGranted` becomes a
precondition check on the identity step instead. A test asserts the publication
package invokes no `git commit` at all — two paths that can disagree about what
was committed is the defect class this chain exists to remove.

## Custody, in three layers

```text
IDENTITY                    the exact object C
CANDIDATE CUSTODY           the candidate branch points at C, and disappears
                            with it if the disposition removes the branch
ADMISSION CUSTODY           a policy-owned durable ref -- refs/sensei-code/
                            admitted/<...> -- created by a separate decision
```

Minting `C` does give it *candidate* custody, because a ref points at it. That
custody belongs to the candidate lifecycle and dies with it, so it still implies
no admission. **Admission custody is out of scope for this slice** and is named
here only so the boundary is explicit.

Before implementing admission custody, re-read
`sensei_code.candidate.disposition_is_decided_and_evidence_outlives_removal`:
the dangerous seam is not minting `C`, it is what happens to the ref afterwards.

## Order of work

```text
1  gitx: canonical tree + deterministic commit-tree, with a reconstruct verifier
2  workflow: mint at acceptance, verify, noteCandidateCommit, before the receipt
3  receipt: CandidateState reads measured work; schema v3
4  publish: remove the commit path; verify HEAD == C before pushing; guard test
5  measure whether digest(diff(B,C)) == D, and record the answer either way
```

Steps 1-3 close F1. Step 4 removes the second commit path. Step 5 is a
measurement whose result belongs in the record whichever way it falls.
