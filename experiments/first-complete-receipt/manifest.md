# The first COMPLETE governed-run receipt

Three ordinary governed runs, recorded because they are the first non-VOID
execution artifacts this chain has produced, and because the two that failed
are better evidence than the one that worked.

Nothing here is admitted. Every candidate is retained and unpublished.

## What was being tested

F1, found by the receipt slice itself: an accepted run reported
`CandidateState PRESENT`, which requires the candidate's commit, tree and first
parent, and **the loop never committed its candidate**. The main success path
could not produce a complete account of itself. Steps 1-3 of Exact Candidate
Identity were meant to close that. These runs asked whether they did.

## The three runs

```text
R1  deferred authority        NO RECEIPT AT ALL
R2  incomplete                COMPLETE-shaped but INCOMPLETE for one true reason
R3  complete                  COMPLETE / ACCEPTED
```

### R1 — the receipt that was never emitted

Task: a one-word spelling fix in `internal/gitx/capture.go`.

That file has **zero anchors** — it is the file C4 reserved for C6, unexamined
by the graph. The router therefore took the *bounded knowledge gap* path,
opened a closure round, failed to close the gap, and escalated to a human-owned
boundary, recording an open question:

```text
state_mutation_confined_to_owner(Artifact.Excluded under Artifact.) in internal/gitx
```

Exit 3, `workflow.awaiting_authority`, no candidate, and **no receipt**.

That is a finding, not a failure. `WorkflowAwaitingAuthority` was deliberately
classified as non-terminal — the run is resumable and settles later — so no
receipt was wired there. The reasoning holds inside the process model and fails
outside it: **the process exited.** From any observer's position a governed run
started, consumed two model turns, reached a boundary and terminated, and the
only account of it is the event stream — which is exactly the reconstruction the
receipt exists to abolish.

> "Every run emits a receipt" is false as stated. It is every run that reaches a
> terminal, and a deferred-authority exit is an axis the schema has no
> vocabulary for — as `STOPPED` was before it was added.

Open, unresolved. The obvious shape is a `DEFERRED` outcome carrying the
recorded question, but that is a schema decision and was not taken unilaterally.

### R2 — INCOMPLETE, for one true reason

Task: document that `report.Status` is a closed vocabulary. Comment only.
`internal/report/report.go` is `PREFLIGHT_STATUS_OK`, `ARCHITECTURE_SENSITIVE`.

The loop worked: architect planned it, a candidate was cut from the base, the
implementor edited, validation ran, Sensei audited, **Codex reviewed and
accepted**, and the candidate was minted. Every identity held — first parent
exactly the base, `candidate_tree == reviewed_tree`, relation `MATCH`.

The receipt still said INCOMPLETE, and named why:

```text
graph_digest: the certified start did not carry a live graph digest;
              the build commit is a different fact and does not stand in for it
```

It was right. `certifiedStart.GraphDigest()` read the **preflight** authority
block, where the field is empty, while the digest sat in the **workspace
contract** the same gate had already decoded. The receipt refused to name a fact
it had not been given, and refused to substitute the build commit.

**That defect survived roughly twenty rounds of human review of the branch and
was found by one two-minute execution.** It is the first time a mechanism in
this system caught an authoring error before a person did.

### R3 — COMPLETE / ACCEPTED

Same task, same file, governor rebuilt at `f0858b6` with the digest read from
where Sensei reports it.

```text
schema                        sensei-code.governed-run-receipt/v3
completeness / outcome        COMPLETE / ACCEPTED
plan_state / candidate_state  PRESENT / PRESENT

governor_commit               f0858b69aa91122527010d6a637b86fc3ff3f235
governor_binary_sha256        e95ee74498595736472de0188a176c88996f500b…
serving_producer              b5b6113661ee8afeac19bfd7b239272005249fad…  (/proc/<pid>/exe)
base_commit                   f0858b69aa91122527010d6a637b86fc3ff3f235
plan_digest                   49fe1c12113be314034c27e433d7db8cde5d6bfc…
graph_digest                  42e6e12cd5737530c4c8d054f8178cde849b72ca…

reviewer_provider             codex
review_verdict                accept
reviewed_digest               72e92f28058161a743d429995840b8bd632004a6…
reviewed_tree                 3e810fbd559918bf180212f96c8ed87a60ae2e10

candidate_commit              d0a7e9bfc8cc7e5fa42fc62f4722630f326ced82
candidate_tree                3e810fbd559918bf180212f96c8ed87a60ae2e10
candidate_first_parent        f0858b69aa91122527010d6a637b86fc3ff3f235
candidate_digest              72e92f28058161a743d429995840b8bd632004a6…
candidate_commit_diff_digest  72e92f28058161a743d429995840b8bd632004a6…
candidate_digest_relation     MATCH

attempts: 1
  provider=codex  delivery=DELIVERED  verdict=accept  tree=3e810fbd5599
```

Every identity closes:

- `candidate_first_parent == base_commit == governor_commit` — the governor
  named its own commit, and the candidate is parented on exactly it
- `candidate_tree == reviewed_tree` — the minted object IS the content accepted
- `candidate_commit_diff_digest == candidate_digest == reviewed_digest`, through
  one renderer → `MATCH`
- the delivered attempt binds provider, verdict, digest AND tree to the
  receipt's own
- `serving_producer` is the digest of the process that answered awareness, read
  from `/proc/<pid>/exe` after it answered — not a file that was intended to run

`d0a7e9bf` is reconstructible by anyone holding `(f0858b69, 3e810fbd)`. No
signature and no trust in the machine that produced it.

## What these runs do NOT establish

- **Nothing is admitted.** All three candidates are retained and unpublished.
  There is no X+1, and self-hosting has not happened.
- **A comment-only change proves the wiring, not the judgement.** Nothing here
  says the loop handles substantial work.
- **R1's gap is open.** A deferred-authority exit still emits no receipt.
- The external-witness size claim (673 lines → ~100) remains unmeasured: no new
  witness was built.

## The finding worth carrying

Two runs found two structural defects that ~20 rounds of diff review missed, and
both lived in the SEAMS BETWEEN COMPONENTS — the ordering of capture, mint,
disposition and receipt, and which of two decoded results a fact comes from.
Unit tests exercise components. Review reads changes. Only execution traverses
ordering.
