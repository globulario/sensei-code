# The three "false refusals" — what Sensei actually claimed

## The correction

I had been scoring a refusal as wrong whenever RAW's patch passed the frozen
oracle. That only establishes **task truth**: the behaviour works under the
oracle. Sensei refuses on **system truth**: whether the change is admissible to
this architecture — ownership, invariants, authority paths, evidence.

Those are different claims, and letting the oracle's silence on the second count
as evidence against Sensei is a category error. A patch can be locally correct
and globally wrong; the whole thesis is that the second thing exists.

So "false refusal" was never an earned label.

## What the record actually says

All six proof-v6 COLD refusals were recorded with the same summary —
*"Architectural authority reached a human-owned boundary."* That is a **route**,
not a reason. The reason is in the event payload, and the three unresolved cases
say three different things:

| task | stated reason | what it asserts |
|---|---|---|
| `setup-e645669` | "a bounded knowledge gap was not closed by investigation: graph coverage is absent for the planned files: only 1 of 2 requested file(s) are examined in the graph" | **I don't know these files** |
| `tui-ea046ba` | "requires approval for this change class: `human_approval_required` (blast radius security)" | **I need permission** |
| `session-4d32937` | "requires approval for this change class: `review_required` (blast radius service)" | **I need permission** |

**Not one claims RAW's patch is wrong.** No violated invariant is named, no
bypassed owner path, no broken contract. Two are permission-class policy stops;
one is Sensei reporting a gap in its own coverage.

So the strongest case for Sensei — *RAW violates an architectural contract, the
tests pass, Sensei catches it* — **did not occur here**. Sensei never made that
claim. That is a real negative finding and it should not be softened.

## But the label still changes

- **`tui-ea046ba`** — the credentials invariant fired on a *scroll-viewport*
  task. That is the over-broad anchor Repair 2 already corrected. Sensei was
  wrong, for a known data reason, since fixed.
- **`session-4d32937`** — "reconstruct only explicitly authorized tasks …
  without mistaking a proposed plan for authority". Changing how the system
  decides what was authorized is arguably exactly what should require review.
  Plausibly justified **on policy grounds**, while still asserting nothing about
  the patch.
- **`setup-e645669`** — an admission of missing coverage, which is the path
  Repair 1 wired. Not an architectural objection at all.

The honest label for all three is **"refused on authority or coverage grounds,
making no claim about the candidate"** — neither "false refusal" nor "correct
refusal".

## Why it cannot be settled retrospectively

Deciding whether those RAW patches were *architecturally* admissible needs the
patches. They are gone:

```
candidate_dir      None            (RAW worked in a temp worktree, since cleaned)
/tmp/proofbench/proof-v5           deleted
transcript         ~3KB            the model's PROSE SUMMARY, not its code
surviving evidence diff HASH only
```

A hash proves two runs produced the same patch and supports no other claim. The
evidence needed to answer the question was discarded — the same failure class as
instrument defect #13 and as issue #82.

**These three are `RAW-correct / Sensei-refused, justification unresolved`, and
they must stay that way.** They cannot be adjudicated, and back-filling a
judgement now would be invention.

## Repair 5 — make the question answerable

Landed with harness v2:

- **`CandidateDiff` preserves the patch**, not just its hash, under
  `patches/<task>/<arm>/<n>.patch`. Kilobytes, and the only way an architectural
  adjudication can ever run against what a run actually did.
- **`RefusalClaims` records what a refusal asserted**, verbatim and structurally,
  so no future adjudication depends on transcript archaeology surviving a
  truncation cap.

## The architectural oracle, and its hazard

A second oracle — *should this change be allowed?* alongside *does it work?* —
is the right instrument for the thesis. The beautiful case is RAW passing the
functional oracle, failing the architectural one, and Sensei having refused.

**The hazard is real and must be designed against.** Building an architectural
oracle *after* seeing which cases Sensei refused, then scoring Sensei with it, is
tuning the proposition until it passes. That would destroy the campaign's value
faster than any defect so far.

Three constraints, and the design is worth nothing without them:

1. **The oracle is the graph as it already exists.** Invariants, ownership and
   forbidden fixes that predate the campaign — `sensei edit-check` and
   `awareness_impact` against a preserved patch. No invariant written for these
   cases, and no invariant edited to make a case come out right.
2. **It must be able to fail Sensei.** If RAW's patch violates nothing, the
   refusal is not vindicated. An oracle that can only exonerate is not an oracle.
3. **It is applied to every arm, not to the refusals.** Scoring only the cases
   Sensei stopped is selection on the dependent variable. RAW patches that
   Sensei *delivered on* must face the same check — including the four COLD
   candidates the benchmark already scored CORRECT.

Constraint 3 has teeth: it can produce the finding that a **COLD** candidate
violated an invariant Sensei itself holds, which would be the most damaging
result available and must remain reachable.

## Status

```
the three cases        UNRESOLVED — patches destroyed, not adjudicable
"false refusal" count  withdrawn; the label was never earned
"correct refusal"      NOT established either; no claim of a violation was made
p ≈ 0.12 calibration   unaffected — it counts capture, not justification
Repair 5               landed: patches and refusal claims now preserved
architectural oracle   designed, NOT built; needs the three constraints above
```
