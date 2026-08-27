# confinement-v2 — result: links 6–7 for the second family; then a 9 MB binary defeated review

```
E3   16:57:37Z → 17:02:17Z   exit 1   workflow.failed
coverage   derived coverage: 1 anchor(s) over 1 planned file(s) — gosumcheck/main.go [invocation confinement]
route      architectural-authority-granted                    ← links 6 and 7, second family
plan       one file, as the task was written to allow
worker     Claude, 29s: +2 −2 in main.go — exactly the requested change
then       candidate also contains gosumcheck/gosumcheck, an ELF binary, 9,211,341 bytes, TRACKED
audit      refused: malformed_diff · diff payload exceeds 5242880 bytes
edit_check ResourceExhausted: 6860500 > 4194304 (gRPC max)
review     Codex: no bounded decision → Claude: no bounded decision
terminal   no bounded implementor produced an acceptable candidate; candidate kept, resumable
```

## Established

**Links 6 and 7 hold for `command_invocation_confined_to`.** With a plan that
stays inside `gosumcheck/main.go`, the confinement anchor closed the coverage
gap and the route changed from cold to granted; the run proceeded to an
implementor. With confinement-v1's link 5, the second family now has the full
`recipe → DERIVED → relevant coverage → routing changed` chain observed on a
foreign repository. The mechanism is not a lock analyzer.

The code change itself is correct (doc comment and the one verbose line), and
`"go" confined to gosumcheck` re-derives `DERIVED` on the candidate.

## The failure, which is a product finding and not about the family

The implementor verified its edit by building the command. The 9.2 MB binary
it produced sat untracked in the candidate worktree, the candidate commit swept
it in, and from there every governance surface failed **on size** rather than
on substance: the diff audit refused a payload over 5 MB, `edit_check`'s gRPC
message exceeded 4 MB, and both independent reviewers could not bound a review
of a diff they could not read. The engine then treated "no bounded review" as
an implementor problem and handed the *same oversized candidate* to the next
executor, which changed nothing about the binary — twice.

Three defects, in order of weight:

1. **The candidate boundary admits build artifacts.** A worker's compilation
   output is not part of the change; nothing strips or refuses it before the
   candidate is committed and judged.
2. **Size failures are attributed as review failures.** A candidate that
   cannot be audited for a structural reason should terminate with that reason
   (`candidate not auditable: binary artifact`), not be retried through every
   implementor and reviewer as if the judgement were the hard part.
3. **Audit and edit-check refuse whole diffs by byte count**, so one binary
   blinds them to a two-line text change they could have judged.

None of this is the fixture's fault, and none of it is about derivation.

## Not established

Nothing about compounding beyond one encounter for this family; nothing about
the reviewer's verdict on the change, which it never got to give.

## E3.log trimmed

The committed `E3.log` keeps every non-`output` event (routing, coverage, audit, review, git, terminal). Worker/reviewer stream-json `output` events -- 104,600 of them, 34353534 bytes, carrying the 9 MB binary through the review transcripts -- are dropped. Untrimmed original: sha256 a0da2009b9ad27128e6160d72c7d82d063fb39a1078daf1a2399b45b471149ae, preserved outside the repository.

## #89 natural reproducer — unmet, not failed (3/3)

Identical task, unchanged; base `9c7e562` (sealed specimen alone);
`sensei-code @ d373d5d` carrying #91. Stopping rule frozen before attempt 2.

```
attempt 1  17:40:39Z  plan: gosumcheck/main.go + gosumcheck/main_test.go (NEW)  1 anchor / 2 files  cold  exit 3
attempt 2  17:45:54Z  plan: gosumcheck/main.go + gosumcheck/main_test.go (NEW)  1 anchor / 2 files  cold  exit 3
attempt 3  17:50:10Z  plan: gosumcheck/main.go + gosumcheck/main_test.go (NEW)  1 anchor / 2 files  cold  exit 3
```

**Escalation, attempt 3, verbatim** (the system isolating the design fork on its
own; not an incidental coverage miss):

> *Should this wait for authoritative coverage of the regression-test file, or
> should the requirement for deterministic regression coverage be removed from
> the change?*

Neither answer may be chosen merely to get #91 merged. The first says the test
requirement is legitimate and Sensei lacks a principled way to authorize a new
surface. The second says the test is outside the authorized change, and removing
it weakens a reasonable engineering requirement solely because governance cannot
yet represent it. Both sides are behaving correctly under their local
contracts: **the architect knows what responsible work requires; the authority
model cannot yet authorize creating the evidence needed to do that work
responsibly.**

> The original one-file E3 path is no longer naturally reproduced under the
> unchanged task: 3/3 repaired attempts independently planned a new test file,
> exposing a systematic prospective-surface authority gap before candidate
> capture.

`gosumcheck/main_test.go` does not exist. A file that does not exist has no
anchor, no anchor can cover it, and one anchor over a two-file plan is not
coverage — so the route stayed cold and the implementor never ran. All three
refusals are **correct under the coverage law**: authority earned on `main.go`
was not smuggled across to an unborn file because the architect thought the
addition sensible. Attempt 3's own escalation states the gap exactly: *"Should
this wait for authoritative coverage of the regression-test file, or should
the requirement for deterministic regression coverage be removed from the
change?"*

Evidence as it stands:

- **#91 synthetic regression: green** at three levels, including the stub-driven
  e2e (6,291,464-byte artifact excluded and named; 334-byte diff; audit pass;
  review ACCEPT; `workflow.completed`).
- **original #89 natural failure path: not reached.**
- **three natural reruns: correctly blocked, all for the same new reason.**
- **prospective-new-file authority: a new observed architectural gap** — a plan
  can become more responsible by adding a file and unauthorizable by the same
  act, because coverage exists only for surfaces that already exist. Recorded
  as a phenomenon; not patched, not assigned yet (Family 3, prospective
  authority semantics, or a product issue — a decision, not a reflex).

#91 is **held**. #89 stays **open**: its closing criterion requires both halves,
and the natural half is unreachable under the unchanged task for a reason that
is itself architecturally meaningful. Nothing was bent to manufacture a green
witness.

Logs: `runs/E3-repaired.attempt{1,2,3}.log`, trimmed of worker stream-json as
`E3.log` was; untrimmed hashes in the commit.
