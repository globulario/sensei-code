# A check reports its inability to run by failing, never by skipping

Found three times on 2026-09-01, in three different PRs, within one working
session. Recorded here rather than proposed to the graph yet, for a sequencing
reason given at the end.

## The shape

A test cannot run under some condition. It calls `t.Skip`. The package prints
`ok`. CI is green. The check has been retired and nobody was told.

It is the same error as reading an unavailable value as an empty one, moved
into the test layer: **silence is produced by both "nothing was wrong" and
"nothing was examined", and the two are indistinguishable downstream.**

## How it was found

Not by review. By a falsifier of mine that could not fail.

I claimed twice, in a PR comment, that the promotion gate had *run* in CI, on
the evidence of **zero `--- SKIP` lines** in the `build-and-test` log. `go test`
without `-v` prints nothing at all for a skipped test — just `ok <pkg>`. So the
count is zero whether the test ran or skipped. Measured rather than argued: a
package containing one always-skipping test prints `ok awgprobe 0.002s` and
greps to zero SKIP lines.

The count could not have come out any other way. It was never evidence.

Following that back found the defect it was hiding, and then five more.

## The three instances

**#321 — the guard that vanished.** `TestTheGateTestCanActuallyRunHere` exists
to report "the promotion gate could not run in this environment." It called
`olderCommit(t)`, which *skips its caller* when no commit qualifies. The guard
disappeared under exactly the condition it existed to detect. Four further
skips in the same file stood where failures belonged: a selector returning a
non-ancestor (the selector is wrong, not the environment), two fixtures that
were no longer the case under test, and fixture steps failing after
`git --version` had already succeeded.

**#323 — the test retirable by one file.** `TestOwnershipIsProvenanceNot-
CurrentExistence` hardcoded a deleted path and skipped if that path returned.
Its own comment says it exists so that "re-adding an existence gate breaks
nothing that fails" — yet it could be retired silently by a single file
reappearing, leaving that precise bypass uncaught. The path is now selected
from `git log --diff-filter=D`.

**#324 — the instrument that switches itself off.** `TestRecallAndNuisanceAre-
MeasuredTogether` skipped below 20 authored anchors. The corpus is part of the
repository, not the environment, so the recall and nuisance numbers the whole
retrieval program is judged by would stop being produced while CI stayed green.

## The rule that came out of it

A skip is legitimate only for a **named, externally detected** limitation:
git is not installed, Oxigraph is absent, the platform lacks the syscall.
Detect it **once, up front, by name**. Everything after that point is a defect
and must fail.

Three questions separate the cases:

1. Is the condition outside the repository? A missing binary is. A shrunken
   corpus, a deleted-then-restored path, and a wrong selector result are not.
2. If this skips, does some check stop being performed? If yes, someone must
   be told, and green does not tell them.
3. Could the fixture be *not the case under test*? Then the assertion below
   proves nothing, and passing is worse than failing.

## Matched pairs, not single mutations

Each repair was checked with two mutations differing only in whether the guard
may skip — same broken input, `Fatal` vs `Skip`:

```
#321  M10 broken fixture + Skip    SURVIVES      M11 broken fixture + Fatal   KILLED
#321  M8  selection returns ""     KILLED        M9  same, Skip restored      SURVIVES
#324  M16 corpus starved + Fatal   KILLED        M17 same, Skip restored      SURVIVES
```

The survivors are the point. They are not failures of the mutation testing;
they are the defect, exhibited.

## Why this is not proposed to the graph yet

The invariant's `required_test` bindings would name tests that live on three
unlanded branches. Proposing now would put dangling references into
`main`'s corpus and turn CI red — the same failure already recorded when five
of my own cap-law entries cited tests that did not exist yet.

**Owed:** after #321, #323 and #324 land, propose one invariant plus its
failure_mode, binding all three tests by exact name. Not before.

## What it found on its first CI run

The repair earned its keep immediately, and not in the way I expected.

Making the helpers **fail** rather than skip turned CI red at `041abab8` and
named four tests:

```
TestARealCitationIsVerifiedFromGitNotFromTheCandidate
TestEvidenceTheClaimantIntroducedCannotEstablishItsOwnAuthority
TestVerifiedEvidenceIsAVerdictAboutCitationsOnly
TestACitationOnTheClaimantsOwnLineIsNotIndependent
```

**They had never executed in CI.** `build-and-test` checked out with
`fetch-depth: 2` — enough for the commit-scope guard, not enough for anything
needing the admitted branch. On a push event `HEAD` is the branch tip rather
than a merge commit and `origin/main` is never fetched, so no promotion base
resolved, all four skipped, and the package printed `ok`.

#323 then went red for the same underlying reason: its selector reads
`git log --diff-filter=D`, and there is no deletion history at depth 2. Worse,
`homeDomainPath` had been asking `git log --all` of a two-commit history in
every CI run, so every path read as foreign and
`TestNoHomeDomainProofEdgeHidesBehindTheBaseline` passed with no corpus to
check against.

Two PRs, one cause: **provenance questions asked of a history CI does not
have.** Fixed with `fetch-depth: 0` (~60 MB pack, seconds).

### The evidence that replaced the vacuous one

The old claim was "zero `--- SKIP` lines," which is zero either way. The new
one is a chain that could have come out otherwise:

> `baseCommit` fatals when it cannot resolve a base. All four tests call it.
> CI is green at `da39611b`. Therefore a base was resolved, therefore they ran.

Without the fetch-depth repair, that same chain produces red — and did.

**This is the difference the whole document is about.** The first claim was
compatible with every world. The second is false in the world where the fix
did not work.

## The sequence, which is the point

A falsifier of mine that could not fail
→ the guard it was hiding
→ four more skips standing where failures belong
→ four tests that had never run, reported by CI itself
→ a fifth passing with nothing to check against.

None of it came from reading the code more carefully. Each step was forced by
turning a silence into a failure.
