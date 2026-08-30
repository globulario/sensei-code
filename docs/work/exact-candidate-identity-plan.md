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

`CanonicalCommit(B, T)` is a **function with a test vector**, not a convention
around a subprocess. Step 1 fixes the exact serialized commit bytes:

```text
tree        T
parent      B
author      Sensei Code Candidate Identity <reserved non-person address>
            <base committer timestamp + 1> +0000
committer   the same identity, the same timestamp
message     derived only from (B, T) -- no task id, no reviewer, no plan --
            with an exact trailing newline
```

Everything is an input or a constant; nothing is read from the environment. The
tests assert that none of these change `C`:

```text
user.name   user.email   TZ   arbitrary git config   the current clock
task id     reviewer     plan digest
```

The task id, reviewer, plan digest and verdict live in the **receipt**.
Putting them in the commit would give identical candidate content different Git
identities merely because a different task produced it.

Verifier, requiring no signature and no trust in local Git config:

```text
reconstruct C' from (B, reviewed tree)   ->   C' == recorded CandidateCommit
```

## The transition

**The tree is measured at capture and travels in the review binding.** An
earlier version built `T` after acceptance, which meant `T` was a tree
*constructed from* what was reviewed rather than one of the identities *of* what
was reviewed — leaving a race between "review succeeded" and "build the tree".

`D` cannot close that gap on its own: `CandidateCapture.Diff` deliberately
renders a kept binary as `Binary files differ` rather than bytes, so two
different binary blobs can share a textual review representation. **Tree
equality, not textual-diff equality, carries exact content identity.**

The reviewer binding therefore grows one field:

```text
BaseSHA
CandidateDigest    the representation the reviewer consumed
CandidateTree      the exact content identity          <-- new
```

```text
worker finishes
  ↓ capture the final candidate against B      (CandidateCapture)
  ↓   -> exact Paths, review Diff D, canonical Tree T, built HERE from the
  ↓      same measured state
  ↓ binding = { B, D, T }
  ↓ audit + review that binding
  ↓ ACCEPT
  ↓ re-capture: D2 must equal D AND T2 must equal T
  ↓ mint C from the ALREADY-BOUND T -- no worktree read at this point
  ↓ point the candidate branch at C
  ↓ verify: C^1 == B, C^{tree} == the built tree, C reconstructs
  ↓ noteCandidateCommit(C, tree, first parent)
  ↓ emitRunTerminal  ->  COMPLETE / ACCEPTED for the first time
  ↓ disposition, publication, admission -- each a separate decision
```

## Step 0 of step 1 — an exact reviewed path set

The tree builder needs "exactly the reviewed paths". **That set does not exist
today.** `Capture` carries `Diff`, `Excluded` and `Binaries` and no path list at
all, so every consumer re-derives it from text:

```text
CandidateCapture   --numstat WITHOUT -z, TrimSpace, SplitN on tab
report.FromDiff    parses `diff --git a/P b/P` headers, fails open on a
                   quoted path
changedPaths(diff) the same, again
```

That is the representation seam reserved for C6, and canonical identity cannot
depend on the lossy form while promising to repair it later. **An assumption
moves earlier when a new load-bearing mechanism starts depending on it**, so
that piece of C6 moves into step 1.

```text
CandidateCapture
  ↓ artifact exclusions established, as today
  ↓ git diff --no-renames --name-status -z <base>
  ↓ parse on NUL boundaries -- no trimming, no tab splitting
Capture.Paths      the ONE authority for the reviewed path set
```

**No rename detection**, deliberately: a rename then arrives as exactly what
the tree builder needs -- a deletion at the old path and an addition at the new
one -- rather than a presentation-level encoding the builder would have to undo.

That single set is the authority for all three consumers, with no independently
reconstructed list anywhere:

```text
what the reviewer judged
what the temporary index stages
what the diff(B,C) path verification compares against
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
1. C^{tree} == the BOUND T, the tree the reviewer's binding named   MUST hold
2. changed-path set of diff(B,C) == reviewed path set              MUST hold
3. digest(diff(B,C)) == D                                          MEASURED
```

With `T` in the binding, (3) is properly secondary: exact content identity is
already carried by (1), and the digest comparison becomes a measurement about
two *representations* rather than the thing the identity rests on.

If (3) does not hold in practice, the receipt records both digests and says
they are produced by different mechanisms. It does **not** force them equal, and
it does not quietly redefine `CandidateDigest` to mean whichever one matches.
That would be the false equivalence this chain keeps repairing.

**v3 reserves the typed space for that measurement now**, rather than landing v3
and discovering step 5 needs v4:

```text
CandidateDigest             the reviewed CandidateCapture digest, D
CandidateCommitDiffDigest   recomputed from diff(B,C)
CandidateDigestRelation     MATCH | DIFFER | UNKNOWN, read by enumeration
```

`UNKNOWN` is the honest value before step 5 has measured anything, and it is
never sufficient for COMPLETE once both digests are present.

## Schema: v3

`CandidateState` changes meaning — `PRESENT` becomes *measured work*, not
*worktree exists*:

```text
NONE      candidate content was measured and there is no work
          (Evidence.ProducedNoWork already states exactly this)
PRESENT   candidate content was measured and differs from B
UNKNOWN   the run cannot yet say
```

The transitions are explicit, and the important one is the middle:

```text
before the candidate lifecycle    NONE, when structurally known absent
worktree opened / worker running  UNKNOWN   <-- content is mutating and
                                            unmeasured; a stale NONE here
                                            would deny work that exists
final capture empty               NONE      (Evidence.ProducedNoWork)
final capture non-empty           PRESENT
```

A failure after the worker mutated the tree but before `CandidateCapture` must
therefore read `UNKNOWN`, not `NONE`.

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

## Custody, in four layers

Three was wrong: publication already creates a durable ref, and says plainly
that it is not admission.

```text
IDENTITY               the exact object C
LOCAL CANDIDATE        the local candidate branch -> C; disposable with the
CUSTODY                candidate lifecycle
PUBLICATION CUSTODY    origin's candidate branch -> C, via
                       `push --set-upstream`. Durable, and explicitly NOT
                       admission: publish.Body says so in the pull request
                       itself -- "not a Sensei admission", "Sensei Code does
                       not merge".
ADMISSION CUSTODY      a policy-owned ref -> C, created by a separate decision
```

This is better evidence for the architecture than the three-layer version:

> **Durability does not imply admission either.** Authority comes from the
> class and creator of the surviving ref, not from reachability.

And the claim about removal must be narrower than "deleting the branch makes C
unreachable" -- reflog and GC timing can retain objects transiently. The claim
we actually need is:

> no policy-recognised candidate custody remains once its candidate refs are
> removed.

**Admission custody is out of scope for this slice** and is named only so the
boundary is explicit.

Before implementing admission custody, re-read
`sensei_code.candidate.disposition_is_decided_and_evidence_outlives_removal`:
the dangerous seam is not minting `C`, it is what happens to the ref afterwards.

## Order of work

```text
1  gitx: exact NUL-safe paths (capture AND the artifact boundary), canonical
   tree, pinned serialization, a non-writing verifier         DONE (d9d9a9e, 0960822)
2  workflow: build T at capture, carry { B, D, T } in the review binding,
   re-check D and T at acceptance, mint C from the bound T, verify,
   noteCandidateCommit -- all before the receipt
3  receipt: CandidateState reads measured work; schema v3
4  publish: remove the commit path; verify HEAD == C before pushing; guard test
5  measure whether digest(diff(B,C)) == D, and record the answer either way
```

Steps 1-3 close F1. Step 4 removes the second commit path. Step 5 is a
measurement whose result belongs in the record whichever way it falls.
