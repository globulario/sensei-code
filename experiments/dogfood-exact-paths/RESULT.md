# Task A result: the judgement path worked, the accounting did not

Scored against the nine criteria frozen in `manifest.md`, and the attribution
procedure frozen in `ADDENDUM-2-attribution.md`. Nothing here was decided after
the fact.

## Outcome

```text
exit 5 — "sensei-code run: timed out; the task was stopped and its candidate
          left in place"

terminal events   0
run receipts      0
reviews completed 1  (REVISE)
elapsed           25m, timed out inside the REVISION turn
```

## Criterion by criterion

```text
1  reviewer evaluated a real behavioural implementation   PASS
2  T1/T2 distinguishable                                  UNTESTED — T2 never completed
3  no verdict over T1 authorised T2                       UNTESTED
4  C wraps the accepting verdict's tree                   UNTESTED — no mint
5  validation/audit match the final state                 UNTESTED
6  COMPLETE under ACCEPT or REFUSE                        NOT REACHED — no receipt
7  no event-stream reconstruction required                FAIL
8  schema matches its vocabulary                          N/A
9  facts recorded before the terminating edge             FAIL (new shape)
```

## Criterion 1, in the reviewer's own words

Codex, reviewing candidate `73449817d223`, returned **REVISE** with a blocking
finding:

> `exactChange` claims it can safely fall back to `report.FromDiff` when
> FromCapture detects a mismatch. The architectural plan explicitly requires
> structural mismatches to fail closed. This code instead accepts the reviewed
> candidate, then records a report and decision using the known-lossy
> parser — **the exact authority bypass this change is meant to eliminate.**

It caught the worker removing a lossy parser while leaving a fallback *to that
parser* on the error path, and named it as fail-closed violation and
"fallback-as-truth". That is the first genuine architectural judgement this
chain has observed, and it was not engineered: the task text named no
implementation.

## The new defect

**A timeout is a process-exiting terminal that emits no receipt.** Same family
as R1's authority-deferral, which was closed earlier today with the manifest
stating plainly that other non-terminal exits had not been surveyed. This is one
of them.

Criterion 9 is the candidate law under test, and this run falsifies the current
implementation of it: the timeout edge terminates the encounter and records
nothing.

## P1 activation: UNMEASURED, and the procedure has a hole

```text
measurement 1  mint refusal naming two trees   unavailable — never reached mint
measurement 2  relation == MATCH with minted C unavailable — no receipt
measurement 3  validation evidence             INSUFFICIENT
```

Measurement 3 was supposed to be the fallback. It cannot answer the question:
`Format: gofmt -w cmd internal` IS configured and runs first, but the design
**deliberately discards its evidence** ("everything gathered before a rewrite is
evidence about different bytes"). The pre-format digest is emitted nowhere, so
no comparison is possible.

That is a hole in the procedure I pre-registered, recorded rather than closed by
guessing. A future version needs the pre-format digest emitted — not the
formatter's own evidence, just the identity of what it received.

## Classification

Per the frozen decision table: **Task A exposed a NEW defect.** It is preserved
here; it does not replace J1; J1's falsifiers are unchanged.

## Consequence for J1

A single revision cycle consumed more than 25 minutes. J1 as frozen carries the
same 25m timeout and asks for work of comparable size, so it would likely hit
this same wall **before reaching criteria 8–12** — the conditional revision
criteria it exists to test.

The blocker is real and stands in front of J1. Two clean options, neither taken
unilaterally: repair the timeout-emits-no-receipt gap first, or raise J1's
timeout and run it as frozen.

## Preserved

```text
A1.timeout-mid-revision.log   the whole run
A1.candidate-T1.diff          36,107 bytes — the reviewed candidate T1
A1.candidate-state.txt        four files: report.go, report_test.go,
                              engine.go, and a new exactpaths_test.go
A1.review.json                Codex's finding and verdict
```

No receipt exists for this run, and none can: the stopping rule is ONE
invocation, and re-running to manufacture one would destroy the freeze that
makes the result mean anything.
