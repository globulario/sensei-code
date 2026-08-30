# Exact Candidate Admission — the lifecycle survey

F1 says the main success path cannot produce a COMPLETE receipt because the
loop never commits its candidate. This survey answers the question that comes
before any `git commit`:

> **At what exact transition has the candidate become immutable enough that
> minting its Git object does not prematurely grant durability or authority?**

No code is changed here.

## The governing invariant

`sensei_code.candidate.disposition_is_decided_and_evidence_outlives_removal`
(critical, active): *a candidate is kept or removed by decision, and its
evidence is recorded before anything is removed.*

`internal/candidate/disposition.go` states the two properties that carry it:
evidence — base, diff digest, audit verdict, changed paths — outlives the
worktree, and retention is a decision with a reason. `Resolution.Validate`
refuses any removing disposition whose evidence does not name a base and either
the shape of the work or an explicit `ProducedNoWork`.

Note what evidence means today: **metadata about the content, not the content**.
A committed candidate would make the work itself outlive removal, which is
strictly stronger than the invariant requires.

## The lifecycle, as it actually runs

```text
1  CreateWorktreeAt(base)        worktree + branch exist; content empty
2  worker turn                   content mutates. local_commit is GRANTED by
                                 default, so the worker MAY commit, repeatedly
3  CandidateCapture(base)        the diff, taken AGAINST THE BASE
4  awareness_audit_diff          Sensei's verdict on that diff
5  review                        binding = {base, sha256(diff)[:16]}
6  revise -> back to 2           content mutates; a new digest; a new binding
7  accept                        content is final for this run
8  publication offer             HUMAN decision
     accepted -> publish.Open    commits the reviewed paths, pushes, opens a PR
     declined / not offered      nothing is ever committed
9  resolveCandidate              disposition + evidence; worktree may be removed
10 emitRunTerminal               receipt, then the terminal event
```

## Three frictions that turn out not to exist

**The diff path already tolerates a commit.** `gitx.CandidateDiff` diffs against
the recorded base precisely so that "committed and uncommitted work look the
same to the reviewer" — written because a worker that used its granted
`local_commit` left a clean tree and the run reported it had produced nothing.
Committing therefore does not break capture, audit, review or confinement. The
integration cost I expected to be largest is already paid.

**A commit path already exists.** `publish.CommitArgs` stages **exactly the
reviewed paths** — `add --all` is deliberately absent, because the worktree can
hold what the candidate does not. That is precisely the property an identity
commit needs. This slice mostly *relocates an existing act*, it does not invent
one.

**Committing is already permitted.** `local_commit` is granted in the default
config, and `publish.Open` refuses without it. No new capability is required.

## The distinction that answers the question

```text
IDENTITY    a commit object: this tree, this parent, this digest
CUSTODY     a ref that keeps that object reachable
ADMISSION   a decision that this object may govern
```

A commit on the candidate's own branch mints **identity** and nothing else. If
the disposition then removes the branch, the object becomes unreachable and
collectable — so committing grants no durability by itself. Durability arrives
only when some surviving ref points at it, and that ref is where admission
lives.

> **Commit establishes identity. A protected ref establishes custody. Admission
> is a separate decision that creates one.**

That is the safeguard the question asked for: the commit cannot become admission
because it confers nothing that outlives the candidate's own branch.

## Where the commit can honestly be made

```text
A  before each review          the reviewer names a commit, not only a digest
                               COST: one object per revision, and keeping first
                               parent == base means resetting the branch the
                               worker is standing in
B  at acceptance (step 7)      one object per run; its tree is exactly what was
                               reviewed; first parent is the base
                               COST: publish.Open must stop committing, or
                               become idempotent over an existing commit
C  at disposition (step 9)     every candidate holding work gets identity,
                               including rejected ones, so the CONTENT outlives
                               removal rather than only its digest
                               COST: mints objects for work judged not worth
                               keeping (unreferenced, but minted)
```

**B is the transition the question names.** At acceptance the content has
stopped changing, the reviewed digest is fixed, and nothing downstream mutates
the tree. It must happen **before step 10**, or the receipt cannot name what it
is reporting.

## Four decisions that are the owner's, not mine

### 1. First parent, or ancestry?

A worker with `local_commit` may leave a chain of its own commits. The tip's
first parent is then the previous worker commit, not the base.

```text
first parent == base   an exact, reproducible lineage claim (the C5 predicate),
                       but it requires minting a governance commit whose tree is
                       the reviewed tree -- discarding the worker's own history
base is an ancestor    preserves the worker's commits as evidence, but weakens
                       the claim to "reachable from", which is what a merge also
                       satisfies
```

I lean to a governance commit with first parent exactly the base, because the
admitted object should be reproducible from base + reviewed tree by anyone. But
it destroys evidence a worker produced, and that is a real cost, not a
formality.

### 2. Does "candidate exists" mean a worktree, or work?

`noteCandidateCreated` fires when the worktree is created, so a run that
produced nothing still reports `PRESENT` — and `PRESENT` requires a commit.
Committing an empty tree to satisfy that would be minting a specimen. Either
the axis tracks **work** rather than the worktree, or an empty candidate needs
its own state. `Evidence.ProducedNoWork` already exists and says exactly this,
which suggests the axis should read it.

### 3. Who authors the commit, and does it look like a person?

A governance commit is a new artifact. Its author, message and trailers decide
whether a later reader can tell it from a human's work. It should say what it
is: base, plan digest, task id, reviewed digest — and it must not be
attributable to the account whose identity a push would carry.

### 4. Does publication keep committing?

If B lands, `publish.Open` is committing something that already exists. Making
it idempotent is small; leaving both is two paths that can disagree about what
was committed, which is the class of defect this whole chain has been repairing.

## What would close F1

With B, an accepted run measures `noteCandidateCommit(commit, tree, firstParent)`
before `emitRunTerminal`, and the main success receipt becomes COMPLETE for the
first time — with the candidate's identity re-derivable by anyone from the
preserved record.

Nothing here admits anything.
