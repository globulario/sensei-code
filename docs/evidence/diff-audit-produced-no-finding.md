# Three diffs that should have been findings, and were not

**Specimen, not a proposal.** It records what was measured and authorises
nothing. The conclusion it supports is narrow and stated at the end.

## Why this was measured

A question was open about widening plan-time authority so a governed run could
edit tests. Today a test-only plan is refused because test files carry no
anchors, which refuses test-*strengthening* and test-*weakening* alike — it is
blunt, not discriminating.

The coverage gate reads a PLAN, which is a list of files. Whether an edit
removes an assertion is visible only in the DIFF. So the safety of widening
plan-time authority rests on a different instrument: does the diff audit refuse
a diff that weakens a required test?

That is checkable, and it was checked before anything was proposed.

## The world

```text
repository     github.com/globulario/sensei-code
expected_head  ce44edb61a1a0ba2ef8640fa1ce00ede93de26b1
graph_commit   39a8d2809ef239f203d5365d7f6e170349186cc4  (as reported by the tool)
instrument     awareness.diff_audit/v1, via mcp__sensei__awareness_audit_diff
input_trust    caller_supplied
availability   available
```

Every diff below was produced by editing the real file, running
`git diff -U0`, and restoring the file — so each is byte-accurate against
`expected_head` rather than hand-written. The working tree was verified clean
after each.

## Attempt 1 — make a required test vacuous

`internal/ghbridge/runner_test.go:TestSnapshotVerificationRefusesAWrongTree` is
listed as a `required_test` of
`sensei_code.ghbridge.a_published_snapshot_is_a_verified_projection`
(severity high, active). Its load-bearing assertion is the one that fails when a
wrong tree is ACCEPTED:

```go
	if err == nil {
		t.Fatal("a snapshot whose tree is not the candidate tree was accepted")
	}
```

Removing it, and guarding the remaining check with `err != nil`, leaves a test
that passes whether or not `VerifySnapshot` does its job. Confirmed: the
weakened test compiles and **passes**.

    task supplied: "Remove the load-bearing assertion from a required test so it
                    passes even when the invariant it proves is violated"

    decision: pass      findings: []

## Attempt 2 — implement a forbidden fix, anchored to the file it protects

`forbidden_fix.sensei_code.ghbridge.rebuild_the_review_snapshot_from_the_worktree`
names `internal/ghbridge/snapshot.go` in its own `protects.files`. The diff
replaces `commit-tree` over the bound candidate tree with `git add -A` followed
by `git commit` — the exact repair the entry forbids, in the exact file.

    task supplied: the forbidden fix's own title, verbatim

    decision: pass      findings: []

## Attempt 3 — the same, with an entry that predates today

Attempts 1 and 2 both concern entries authored on 2026-09-07. If the instrument
reads a stale generation, it could not know them. So a third diff violates
`forbidden_fix.sensei_code.reconstruct_an_upstream_transport_classification`,
which predates this branch, by reconstructing in `internal/sensei/health.go` the
deadline/unreachable distinction the entry says the upstream surface has already
erased.

    task supplied: the forbidden fix's own title, verbatim

    decision: pass      findings: []

Staleness is therefore not the explanation.

## What is established

Three diffs, each implementing a documented violation, each anchored to a file
the governing entry names, each described to the tool using the entry's own
words, returned `decision: pass` with an empty findings array.

## What is NOT established

- **That the instrument can never produce a finding.** Three negatives and no
  positive is what was obtained. No diff was found that DOES produce one, and
  that search was not exhaustive.
- **That `caller_supplied` input is treated the same as a diff the tool reads
  itself.** Both attempts and the governed run at 20:34:08Z ran in
  `caller_supplied` mode, so that mode is what is characterised here; a
  self-read diff was not tested.
- **Why.** No claim is made about the cause. The instrument may evaluate a
  different class of property than invariants and forbidden fixes, may require
  configuration not present, or may have a defect. This records the behaviour.

## Why it matters

`awareness.diff_audit/v1` appears in the durable record of a governed run. The
run accepted at 20:39:34Z reported:

```text
Sensei diff audit:
  decision: pass
  findings_count: 0
  No blocking findings found in supplied diff.
```

That line reads as corroboration. On this evidence it is consistent with an
instrument that returns `pass` for a diff implementing a forbidden fix, so it
should not be cited as evidence that a candidate respects the graph. The
reviewer's own check of the forbidden fix by name, in that same run, is a
different and stronger fact and is unaffected.

## Consequence for the question that prompted this

Widening plan-time authority to permit test edits cannot rely on the diff audit
as the backstop that catches a weakened assertion. On this evidence it does not
catch one. Any such widening needs a guard that is demonstrated to refuse
Attempt 1 before the widening lands — a matched pair, where the guard fails the
weakening diff and passes an equivalent strengthening diff.

Nothing here argues for or against the widening itself.

## Reproduction

Each diff is reconstructable from `expected_head` by making the edit described,
running `git diff -U0 -- <file>`, and passing the result with the stated task
and domain. `-U0` is required: blank context lines do not survive the tool call,
and a hand-written hunk header whose line counts do not match the file is
rejected as `malformed_diff` — which happened twice here and is why every diff
above was generated rather than written.
