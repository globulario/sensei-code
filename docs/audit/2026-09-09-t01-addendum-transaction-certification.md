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
