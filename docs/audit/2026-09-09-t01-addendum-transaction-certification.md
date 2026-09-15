# T01 addendum — runtime transaction certification is UNPROVEN

Belongs to the T01 record set. **Uncommitted and untracked, deliberately:** the
T01 packet is already in commit `1bc5a91` on PR #167, and amending it would move
the head from `2aefd58de6239fd3d892fbba0ecee2a5f4a2a7f3`, which requires another
review. This is recorded beside the packet instead and must be committed in a
later, separately reviewed step.

Owner ruling, 2026-09-09.

## The observation

Publishing the F1 slice from `2aefd58` updated
`.sensei/graph-authority.json` to marker `c0b660fc42a5` / 35,268 triples, but did
not rewrite `.sensei/graph-authority.transaction.tsv`, which still names the
previous seed digest `fc45da2905b4` / 35,234 triples. Briefings that read
`transaction=certified` before the publish now read:

```text
Authority:  authoritative (current, provenance=stamped, transaction=uncertified)
Tx detail:  runtime transaction seed digest does not match expected graph
```

Both files are gitignored and local. No repository state is affected, the tree
is clean, `Authority verdict` is still `authoritative` and `Freshness state` is
still `current`.

## Classification (owner)

This is a **separate F9 / toolchain finding. It is not to be repaired inside
PR #167.**

It does **not** invalidate F1's code-level result: the repair, its three
mutation-checked falsifiers, the live commissioning behaviour and the gate all
stand on their own evidence.

But it fixes a boundary on what may be claimed:

> **This publication cannot be cited as proof that runtime transaction
> certification works.**

Any later document that points at the F1 publication as evidence of a working
certification chain is making a claim this run does not support.

## Probable cause, stated as probable

The `sensei` CLI in use is `0.0.1-dev`, linked 2026-09-02 from
`bin/sensei`. Sensei `origin/main` carries
`7148eee6 fix(build): report a runtime transaction that could not be certified`
(2026-09-08), one of the 15 commits this toolchain does not contain. The same
15 commits contain `3e79c4fa fix(reachability): compare the published corpus
revision, not the binary's`, the repair for F9's `Reachability: UNKNOWN`.

That the newer toolchain resolves either symptom is a **prediction, not a
measurement**. Nothing here tested it.

## Required before final V3 acceptance

1. A **corrected Sensei deployment** — `awareness-graph` and the `sensei` CLI
   rebuilt from a Sensei revision containing `7148eee6` and `3e79c4fa`, with the
   serving identities verified after activation.
2. A **clean certified publication** on that deployment: marker and transaction
   stamp naming the same seed digest, a briefing reading
   `transaction=certified`, and `Reachability` resolved rather than `UNKNOWN`.

Until both hold, runtime transaction certification is **UNPROVEN** and V3
acceptance may not be declared. Deployment is an owner/operator action and is
not authorised by this addendum.

## Current classification (owner, 2026-09-09)

```text
F1 implementation                    VERIFIED   locally and live
F1 graph content                     PUBLISHED  and retrievable
Runtime transaction certification    UNPROVEN
Independent review                   MISSING    -> superseded, see below
Merge admission                      BLOCKED
```

### RETRACTED 2026-09-09 — the acceptance below was invalid

The acceptance recorded in this section **is withdrawn**. The reviewer did not
receive the patch it names: the delivered attachment was 11,471 bytes / 247
lines, sha256 `98750617db47764481214a11fc9527275e154822b556ed50fdc369942933de8a`,
with truncated Go and YAML lines — a text conversion of a description, not
`git format-patch` output. The expected artifact is 110,209 bytes / 1,818 lines,
sha256 `4013d04b98c7e1cdfc42a397a06b9f0dff5d8e2dc8ebc5c768ec91deb9de5eb4`.

So no review of `2aefd58` has occurred. The record below is retained, struck,
rather than deleted: this is precisely the failure family F1 repairs — a record
asserting that a mechanism governed a result when the mechanism never ran
against it — and the implementer wrote it. The transport succeeded, the delivery
did not, and "accepted" was inferred from the first.

Independent review returns to **MISSING**.

### ~~Update 2026-09-09 — review accepted at the exact head~~ (RETRACTED)

```text
Independent review                   MISSING    the acceptance above is retracted; the
                                                reviewer never received the patch
Merge admission                      BLOCKED    unchanged
```

What the acceptance is, stated precisely so it is not later read as more:

* It is a **human/owner-channel review** of `git format-patch --stdout
  e28a031b..2aefd58`, sha256
  `4013d04b98c7e1cdfc42a397a06b9f0dff5d8e2dc8ebc5c768ec91deb9de5eb4`, verified
  to apply cleanly onto `e28a031b`.
* The **hosted adversarial reviewer never ran** — `completed/skipped`, gated on
  an absent `OPENAI_API_KEY`/`GEMINI_API_KEY`. The automated channel remains
  unexercised on this change, and no key was added to unblock it.
* The reviewed artifact was **produced by the implementer**. It is digest-pinned
  and the same commits are public on PR #167, so it is independently checkable;
  it was not independently extracted. Recorded as a limit on the review's
  independence, not a defect in it.
* It is **review only**. Merge remains a separate owner act, and any movement of
  the head voids it.

Decision D7, added to the T01 packet's pending set:

| # | Decision | Status |
|---|---|---|
| D7 | Corrected Sensei deployment plus a clean certified publication, required before final V3 acceptance | **PENDING OWNER** |

D7 supersedes nothing. It sits beside D4, which asked whether to redeploy
`awareness-graph` from Sensei `origin/main` to clear F9; this addendum makes
that redeploy a **requirement for V3 acceptance** rather than an open question,
while leaving its timing and authorisation to the owner.

---

## CORRECTION 2026-09-09 — T00 finding F8 was WRONG

F8 in `2026-09-09-t00-baseline.md` claims the `:10122` metadata field
`Source repo commit: 39a8d2809ef239f203d5365d7f6e170349186cc4` names "a revision
that exists in no repository". **That is false.**

It is a real commit in a **third repository** I never checked:

```text
/home/dave/Documents/github.com/globulario/services   (globulario/services)
39a8d2809ef2  2026-09-05T23:10:26-04:00
              fix(transport): carry node advertise_ip end-to-end and make infra
              releases settable
```

Which is exactly what the field is documented to hold. `golang/server/main.go:89`
says `-X main.SourceCommit=<services repo SHA>`, `metadata.go:213` says "the
proto says so, and the canonical recipe fills it from `git -C ../services`", and
`Makefile:154` does literally that. The stamp was correct and honest the whole
time.

**How I got it wrong.** I searched `sensei` and `sensei-code`, locally and via
the GitHub API, got "no commit found" from both, and concluded the revision
existed nowhere. I read a three-member set by checking two members and treated
absence from those as absence from the set — the same fail-open shape this
project has recorded before as *reading a closed vocabulary by exclusion*. The
field named `services`; the documentation named `services`; the build recipe
named `services`. I did not look there.

It surfaced only because building the toolchain ran the canonical recipe, which
resolved `../services` and printed the same SHA I had called phantom.

**Consequences.**

* F8 is **withdrawn**. There is no displayed-identity-without-referent defect.
* The T00 §1.4 text asserting the same thing is wrong in the same way and must
  be corrected with it.
* F9 is **unaffected**: `Reachability: UNKNOWN` compares the binary's *sensei*
  revision against the *sensei-code* corpus revision, and neither is this field.
* The finding count drops from nine to eight.

Both corrections are recorded here rather than amended into commit `1bc5a91`,
because the head is held at `2aefd58` for review. They must be committed in a
later, separately reviewed step.

---

## MEASURED 2026-09-09 — the redeploy did NOT clear D7

The remedy this addendum predicted was tested. **The prediction is refuted.**

The `:10122` server was restarted on a binary built from Sensei
`739133dcea2b43a8a070318357d701d55d2ce43b`, which contains both `7148eee6` and
`3e79c4fa`. Restart was narrow and clean: `:10121` (the frozen reflex-v2
instrument, pid 2403), store `7881`, `sensei/bin/sensei` and the graph itself
(marker `c0b660fc42a5`, 35,268 triples) were all verified unchanged.

```text
old pid 3016813  binary 5cb25a413da8bb44...   -> new pid 893103  binary 1846974783fe09e2...
Graph build commit  fd350489a193  ->  739133dcea2b
Authority verdict   authoritative (unchanged)   Freshness  current (unchanged)
```

Both symptoms **persist** on the new build:

```text
Reachability: UNKNOWN — the serving graph was built from 739133dcea2b, which is
              unordered against the authored corpus revision 5f999ba5354c
Tx detail:    runtime transaction seed digest does not match expected graph
Authority:    ... transaction=uncertified
```

So a server redeploy alone does not satisfy D7. The untested remaining step is a
**fresh publication performed by the new CLI** — the current publication was
made by the old `0.0.1-dev` CLI, and both the transaction stamp and whatever the
reachability comparison reads are written at publication time, not at serve
time. That step is **not authorized**: the restart approval explicitly stopped
at verification.

D7 therefore stands as **PENDING**, now on a measurement rather than a guess:

| | |
|---|---|
| predicted | redeploying the toolchain clears `Reachability: UNKNOWN` and `transaction=uncertified` |
| measured | it clears neither |
| next test | a clean publication by the new CLI, from a clean tree — unauthorized |

Recording the refutation matters more than the repair would have: the addendum
committed to a falsifiable claim, and the claim failed. Anything later asserting
that "the redeploy fixed certification" is contradicted by this section.

---

## EXTERNAL BLOCKER 2026-09-09 — no independent reviewer is available

Recorded at the owner's instruction as a **T01 blocker, external in kind**. It is
an organizational constraint, not an engineering one, and no amount of further
implementation resolves it.

### The state

| PR | Exact head | Review state |
|---|---|---|
| sensei-code #167 | `8e2fcf53ad81894536c3e209fc3287199db282fb` | unreviewed; the reviewer's environment cannot fetch the patch and the file channel has failed twice |
| sensei #350 | `c96aced7ebf4d31daea81b9c3453b18a11e794c5` | unreviewed; implementer-verified only |

Neither is mergeable by evidence.

### Why it cannot be resolved from inside

* The **hosted adversarial reviewer never runs** — gated on an `OPENAI_API_KEY`
  or `GEMINI_API_KEY` this repository does not have. The owner has ruled that no
  key will be added to unblock a PR, so that channel stays closed by choice.
* The **implementer must not select its own judge**, so the reviewer field stays
  visibly unresolved rather than being filled with a convenient name.
* The reviewing party in this session **cannot represent itself as a human
  GitHub identity**, and said so.

The requirement is not being relaxed because it is inconvenient. It has already
caught real defects in this work — including, on the same day, a withdrawn
finding (F8) and a candidate CI found incomplete (#350's corpus drift).

### What unblocks it

A real person who did **not** implement these changes, reviewing each exact SHA
and returning concrete findings or an explicit acceptance bound to that SHA. One
person may review both if they read Go and understand the governance contracts.
Any head movement invalidates the acceptance.

If no suitable collaborator exists, the honest next step is to recruit one — a
trusted programmer or a paid external reviewer — not to lower the gate.

### Consequence for T01

T01 gains an external blocker independent of D1–D5 and D7:

| # | Blocker | Kind | Status |
|---|---|---|---|
| B1 | No independent reviewer is available for #167 or #350 | external / organizational | **OPEN** |

Until B1 clears, no slice bound under T01 can pass reviewed-candidate readiness,
regardless of how complete its implementation is.

---

## T01 PREREQUISITE B2 2026-09-09 — corpus generation reads gitignored filesystem state

Recorded at the owner's instruction as a separate T01 prerequisite. **Not to be
fixed in sensei #350.**

### The defect

> Corpus generation includes gitignored filesystem state, making committed
> reports depend on developer-local caches and tooling directories.

### The measurement

`scripts/build-awareness-graph-self.sh` run at the same commit
`c96aced7ebf4d31daea81b9c3453b18a11e794c5`, in two trees:

| Tree | `scanned_files` | `discovered_tests` |
|---|---|---|
| committed artifact | 1750 | 5445 |
| CI (fresh checkout) | **1753** | — |
| pristine clone | **1753** | 5456 |
| developer working tree | **1767** | 5456 |

`discovered_tests` agrees everywhere (+11, the new tests). `scanned_files`
differs by 14 in the working tree, and the commit contains nothing that explains
it.

**Cause, established by tree comparison rather than assumed.** The working
directory holds **4,595 gitignored files** the clone does not: `.sensei/`
(3,256), `editor/vscode/node_modules/` (1,313), `bin/`, `.cache/`, `.awg/`,
`.claude/`, `.vscode/`. The scanner walks the filesystem rather than the tracked
set, so 14 of them entered the count. Go files are identical in both trees
(1,717 each), which is why only the non-Go portion moved.

An earlier guess — that the five *untracked* files explained it — was wrong and
is recorded as wrong: 5 ≠ 14, and checking stopped the wrong cause from being
written down.

### Why it matters beyond one PR

A developer following the documented procedure in their own checkout commits an
artifact CI will reject. Worse, the drift check runs **before** `Run tests` and a
failure **skips** the tests, so the visible symptom is "no test evidence" rather
than "your corpus is stale". CI is correct only incidentally: `seed-rebuild.yml`
runs on a fresh checkout, so the defect never manifests there.

### Acceptance criterion for the eventual repair

> Identical generated output between a clean clone and a working tree containing
> representative ignored directories.

That is a falsifiable test: populate a checkout with `node_modules/`, `bin/`,
`.cache/` and similar, regenerate in both, and require byte-identical artifacts.
The current code fails it, which is what makes it a real criterion rather than a
restatement.

### T01 blocker register

| # | Blocker | Kind | Status |
|---|---|---|---|
| B1 | No independent reviewer available for #167 or #350 | external / organizational | **OPEN** |
| B2 | Corpus generation reads gitignored filesystem state | engineering prerequisite | **OPEN** |

B2 does not block #350: that PR carries artifacts generated in a clean clone,
which are exactly what CI computes.

---

## T01 FINDING B3 2026-09-09 — an identifier without its repository is not an identifier

Recorded at the owner's instruction as a T01 finding for a **separately bound
repair**. Deliberately NOT added to #167 or #350: widening a PR under review to
carry an unrelated governance rule is the scope creep this register exists to
prevent.

### The rule

> Every GitHub command and every piece of evidence derived from one must bind the
> full `owner/repository`, never an issue or PR number alone. Mutating commands
> must additionally verify the resulting remote state rather than trusting a
> successful-looking invocation.

### The two incidents that produced it, on the same day

**Read side.** `gh pr view 167` was issued from the `sensei` working directory
with no `--repo`. `gh` resolved it against `globulario/sensei`, where #167 is an
unrelated PR merged 2026-08-14. The reported state — `MERGED`, head `bbef6167` —
was true of a real PR and false of the intended one, and it was reported to the
owner as if `sensei-code#167` had been merged. Both repositories have a #167.
The number was read without its scope.

**Write side.** `gh pr edit 350 --body-file …` returned a GraphQL
projects-classic deprecation error and **silently did not apply the edit**. The
command looked like it had done something. Only re-reading the remote body and
diffing it against the intended bytes revealed that nothing had changed; the
edit then succeeded through a REST `PATCH`.

### Why they are one finding

Both are the same defect in different directions: **acting on a command's
apparent success instead of on the state it claims to have produced.** One read a
number without its scope; the other read an invocation without its effect. Both
were caught only by verifying afterwards against the thing itself.

This is the session's recurring shape — *a name is not a scope*, and *delivery is
not transport* — arriving a third time, in the tool layer rather than the code.

### Acceptance criterion for the eventual repair

Falsifiable, so it is a criterion rather than a restatement:

1. Every `gh` invocation in tooling or documented procedure carries an explicit
   `--repo owner/name`, provable by a check that fails on any that does not.
2. Every mutating GitHub call is followed by a read of the mutated object, and
   the recorded evidence is that read — not the exit status of the call.
3. A negative control: an invocation deliberately issued against the wrong
   repository, or one whose write silently no-ops, is detected by the check
   rather than reported as success.

### T01 register

| # | Finding | Kind | Status |
|---|---|---|---|
| B1 | No independent reviewer available for either PR | external / organizational | **OPEN** |
| B2 | Corpus generation reads gitignored filesystem state; a `build-and-test` job can fail without running a test | engineering prerequisite | **OPEN** |
| B3 | GitHub identifiers used without their repository; mutating calls trusted without verifying the result | engineering prerequisite | **OPEN** |

None of B1–B3 is repaired in #167 or #350.

---

## T01 PREREQUISITE B4 2026-09-09 — admission-base resolution trusts local clone topology

Recorded at the owner's instruction as a separate prerequisite. **Not to be
repaired in sensei #350.**

### The finding

> Admission-base resolution trusts the local `refs/remotes/<remote>/HEAD`, which
> a `git clone --branch <feature>` can point at an unmerged feature branch. This
> can classify unmerged corpus changes as admitted, and the repository-level
> reachability test depends on local clone topology.

### The measurement

`golang/reachability` resolves a corpus revision through `ResolveFromGit`, which
reads `refs/remotes/origin/HEAD`. In a clone created with
`git clone --branch fix/governed-abandon-transition`, that ref points at the
feature branch:

```text
refs/remotes/origin/HEAD  ->  refs/remotes/origin/fix/governed-abandon-transition
git remote show origin    ->  HEAD branch: fix/governed-abandon-transition
origin/HEAD  docs/awareness last commit -> 768bacd8   (the PR branch)
origin/main  docs/awareness last commit -> 8c2fdf59
```

`TestTheCorpusRevisionComesFromTheAdmittedBase` then failed with its own
description of the situation:

```text
the corpus revision 768bacd8a96f is not contained in origin/main: an unmerged
edit was counted as admitted, which reports the published graph stale during
its own review
```

Same commit, correctly rooted clone (`origin/HEAD -> origin/main`), forced
execution with `-count=1`:

```text
control 1  detached 768bacd8                                   --- PASS
control 2  detached dfed80ed + the three generated artifacts   --- PASS
```

So the distinguishing input is `origin/HEAD` and nothing else.

### The severity is not the test

The failing test is the visible symptom. The finding underneath is that a
**feature branch can be selected as the admitted corpus base**, which is the
inverse of what admission means. In the failing clone the resolver did exactly
that, and only the containment check downstream caught it. A caller without that
check would have treated unmerged corpus edits as admitted.

### Acceptance criterion for the eventual repair

> A hermetic fixture reproducing this topology proves that a feature branch
> cannot become the admitted corpus base.

Hermetic matters here: the reproduction currently depends on how a clone was
constructed, which is exactly the dependency the repair must remove. A fixture
that builds the ref layout in a temp repository tests the resolver; one that
relies on the developer's checkout tests the developer's checkout.

### Two operational notes this produced

* Generating in "a pristine clone" is insufficient guidance. The clone must also
  be **rooted at the default branch**. `git clone --branch <feature>` gives a
  clean tree and a wrong `origin/HEAD`.
* `go test` reported `ok (cached)` for a test that reads git refs, which the
  cache key does not cover. A cached pass is not an executed test; both controls
  above were re-run with `-count=1` and only the forced results are recorded.

### T01 register

| # | Finding | Kind | Status |
|---|---|---|---|
| B1 | No independent reviewer available for either PR | external / organizational | **OPEN** |
| B2 | Corpus generation reads gitignored state; a `build-and-test` job can fail without running a test | engineering prerequisite | **OPEN** |
| B3 | GitHub identifiers used without their repository; mutating calls trusted unverified | engineering prerequisite | **OPEN** |
| B4 | Admission-base resolution trusts local clone topology; a feature branch can become the admitted base | engineering prerequisite | **OPEN** |

None of B1–B4 is repaired in #167 or #350.

---

## T01 PREREQUISITE B5 2026-09-09 — a governed state must be accepted by its own consumers

Recorded at the owner's instruction as a separate prerequisite. **Not repaired in
sensei #350**, which pins only the concrete abandonment path.

### The finding

> Extending a governed state requires updating every classifier, validator,
> projection, and policy vocabulary that closes over that state, with one
> end-to-end test proving the produced value is accepted by its own consumers.

### Why it is stated at that width

Three instances in one PR, at three different layers, and the third was committed
by the person who had just repaired the second:

| Layer | Closed set | Symptom |
|---|---|---|
| terminal history | `classifyTerminalFacts` | an abandoned ledger reconstructed as `not_completed` — the state before anything happened to it |
| projection state | `AssessmentBoundStates()` → `validCompletionTerminalState` | every abandoned projection rejected by its own canonical contract; `task-status` returned an internal-unavailable envelope |
| closure verdict | `validClosureVerdict` | a projection carrying `ClosureAbandoned`, a verdict the producer emits and the validator rejects |

The first two were found by review. **The third I introduced while fixing the
second**, in the same session, having just written the sentence explaining the
defect. That is the strongest evidence for stating the rule at this width rather
than recording two missing enum cases: knowing the shape did not prevent
repeating it, because nothing mechanical connects a value to the sets that close
over it.

`TerminalAbandoned` had a fourth instance before any of these: it existed in the
vocabulary for months with **no producer at all** — declarable, storable,
checkable, and unreachable.

### The shape

A governed value is emitted by a producer and consumed by classifiers,
validators, projections and policy vocabularies that each maintain their own
closed set. Adding the value to the type is one edit; the consumers are several,
in different packages, with no compiler relationship to it. So the value becomes
producible before it becomes acceptable, and the failure surfaces as
*off-vocabulary* — a system rejecting its own output.

Go's type system does not help here: these are string-typed closed sets read by
membership, which is the correct design for a governed vocabulary and precisely
why the synchronisation is manual.

### Acceptance criterion for the eventual repair

Falsifiable, and deliberately end-to-end rather than per-enum:

1. For a newly added governed state, one test drives the **real producer** and
   asserts the value is accepted by **every** consumer that closes over it —
   classifier, validator, projection, policy vocabulary.
2. A negative control: removing the state from any single consumer's closed set
   fails that test, naming the consumer.
3. The check is not satisfied by enumerating today's known sets. A newly
   introduced closed set that omits the state must also fail, or the repair only
   pins the instances already known.

Point 3 is the hard one and the reason this is a prerequisite rather than a
patch: pinning the three sets above prevents these three regressions and does
nothing about the fourth.

### What #350 does instead

It pins the concrete path: `TestAbandonedIsInTheCanonicalProjectionVocabulary`
asserts both `validCompletionTerminalState` and `validClosureVerdict` accept the
abandoned values, and `TestAnAbandonedProjectionValidates` drives
`BuildCompletionProjection` — the real producer — and validates its output.
Mutation M6 reproduces the exact regression. That is coverage of one path, not a
general mechanism, and the PR does not claim otherwise.

### T01 register

| # | Finding | Kind | Status |
|---|---|---|---|
| B1 | No independent reviewer available for either PR | external / organizational | **OPEN** |
| B2 | Corpus generation reads gitignored state; a `build-and-test` job can fail without running a test | engineering prerequisite | **OPEN** |
| B3 | GitHub identifiers used without their repository; mutating calls trusted unverified | engineering prerequisite | **OPEN** |
| B4 | Admission-base resolution trusts local clone topology | engineering prerequisite | **OPEN** |
| B5 | A governed state can be produced before its consumers accept it | engineering prerequisite | **OPEN** |

None of B1–B5 is repaired in #167 or #350.
