# Inventory: checks in this repository that retire themselves

A survey, not a change. Produced after the same defect was found three times in
one session in `globulario/sensei` (#321, #323, #324) and written up in
[a-check-that-skips-is-not-a-check.md](a-check-that-skips-is-not-a-check.md).
The question asked here: **does the same shape sit in sensei-code?**

It does, and it is concentrated in the layer that exists to prove governance
works.

## Scope of the survey

39 `t.Skip` sites across `internal/` and `cmd/`. Of those, **6** name a genuine
external limitation (`gofmt` not on PATH, a filesystem that will not hold an
awkward name, a missing binary). Those are correct and are not listed below.

The PRs currently in flight are clean: **#139 and #140 add four test files
between them and contain zero skips.** That was verified rather than inferred —
the earlier sweep printed nothing, and printing nothing is exactly the signal
this whole document says not to trust.

## Class C — an empty result reads as nothing-to-test (most severe)

```go
// internal/workflow/protection_live_test.go:43
func TestEverythingSenseiProtectsIsRefused(t *testing.T) {
	protected := protectedFiles(t, root)
	if len(protected) == 0 {
		t.Skip("no protection snapshot to read, so the delegation was never exercised")
	}
```

An **empty protection set is the worst possible state** — it means nothing is
protected at all — and the test asserting that everything Sensei protects is
refused treats it as an absence of work. A misconfiguration that silently
empties the snapshot turns this test green. Same at line 99.

`internal/acceptance/semantic_retrieval_test.go:66` has the same structure: the
endpoint holding no invariants for a domain is read as "nothing a question
could find," when it may equally be the retrieval being broken.

## Class B — the specimen drifted, so the check evaporates

```go
// internal/derived/binding_referents_test.go:294
// ... so the guarantee does not quietly depend on nobody ever writing one.
func TestBindingsResolveThroughTheParserNotTheBytes(t *testing.T) {
	const file = "internal/workflow/routine_test.go"
	raw, err := os.ReadFile(...)
	if err != nil { t.Skipf("fixture file is gone — this test no longer has a subject") }
	if !strings.Contains(string(raw), "func TestOldGuard(") {
		t.Skip("the fixture string that made this a real risk is gone")
	}
```

Identical in shape to the #323 defect repaired today, including the irony: the
comment says the guarantee must not *"quietly depend on nobody ever writing
one,"* and the test then quietly depends on somebody having written one. Two
unrelated edits — deleting that file, or renaming that function — retire it
with nothing red.

`internal/workflow/unexamined_live_test.go:54` skips when *"the specimen's
premise is gone."* That is the failure already recorded in
[witness-specimens-must-pin-the-request]: a specimen that does not pin its own
premise cannot tell "the defect is fixed" from "the question is no longer
being asked."

Also here: `corpus absent`, `ledger %s absent`, `%s transcript absent`,
`arm 1 transcript absent` (`internal/derived`, `internal/proofbench`) — the
evidence a proof rests on going missing is a result, not a reason to be quiet.

## Class D — runs only under configuration it does not have in CI

`internal/acceptance/domain_authority_matrix_test.go` (3 sites) requires
`SENSEI_CODE_MATRIX_ENDPOINTS` and `SENSEI_CODE_MATRIX_BUILD`.
`internal/acceptance/governed_run_test.go:58` refuses a dirty checkout.

These are defensible, but their consequence should be stated rather than
discovered: **they never run in CI.** Whatever they protect is protected only
when someone runs them by hand.

## What this does not claim

I have read four of these closely and classified the rest from their condition
and message. The classification is a starting point for a scoped change, **not
a verdict on each site** — deciding Class B vs. a legitimate limit needs the
surrounding test read, and I have not done that for all 33.

## Proposed next change, deliberately not started

One PR, Class C only, four sites: make an empty protection snapshot and an
empty invariant set **fail**. That is the smallest change with the largest
consequence, and it is independently reviewable.

Class B is a second PR: pin each specimen's premise so it fails when the
premise disappears, rather than falling silent.

Not started now. Four PRs are already in flight and blocked on reviewer
availability; opening a fifth adds queue, not throughput.
