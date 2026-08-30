# Addendum, written mid-run and before the result is known

The frozen manifest is not edited. This is recorded separately, while the run is
still executing, because a note written after the outcome would be retrofitting.

## What was discovered after the freeze

An independent Codex review of PR #126 (exact head `16fde8705c`) arrived AFTER
that PR was merged, so it gated nothing. Its P1 finding is real, and was
verified in the source while this run was in flight:

```go
// engine.go:1313 — the code's own comment states the hazard
// "Formatters run first because they can rewrite the candidate ...
//  The diff is therefore re-read afterwards"
evidence, diff, err := e.validate(...)     // POST-format diff

// engine.go:1461
CandidateDigest: candidateRevision(diff),  // POST-format
CandidateTree:   capture.Tree,             // PRE-format (captured at :1199)
```

**The review binding is internally inconsistent whenever validation mutates
candidate bytes.** `D` and `T` then describe different content, which falsifies
the claim made when the binding was built — that `D` is a deterministic function
of `(B, T)` from one measurement, so "the three are one claim". They are three
measurements the moment a formatter touches a file.

Downstream, the mint re-measures, finds `T2 != bound T`, and refuses the
candidate as "moved after it was reviewed". The success path therefore fails for
most real Go changes; every `COMPLETE / ACCEPTED` recorded so far was on content
no formatter altered.

## What this means for THIS run

This task edits `internal/report/report.go` and `internal/workflow/engine.go` —
files a formatter is likely to touch.

> **If this run fails at the mint, that failure is attributable to the P1 defect
> and NOT to the loop's judgement, its reviewer, or the revision machinery this
> experiment was frozen to test.**

Stated in advance so the result cannot be read either way after the fact. The
run is not being killed: the stopping rule says one invocation and whatever
comes out is recorded.

## Decision on the defect

The owner's decision: **leave P1 standing** as the subject of experiment J1,
rather than repairing it now. The loop is to be asked to repair the defect its
own reviewer found in it — which is a materially better experiment than a
manual fix, and which requires the bug to remain in place while J1 runs.
