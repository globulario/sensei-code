# Dogfood: a medium behavioural task, frozen before it runs

Every governed run so far in this chain has been a comment-only change or a
deferral. The loop's WIRING is proven; its JUDGEMENT is untested. This is the
first task where a reviewer could reasonably ask for a revision, and where more
than one implementation is legitimately defensible.

Frozen before the run. Nothing below is edited afterwards.

## The task, stated as the problem and not as a solution

```text
internal/report/report.go derives changed-path information by PARSING DIFF TEXT:
it reads `diff --git a/P b/P` headers and `+++ b/` lines (report.go:80, 95). A
pathname Git quotes -- one containing a tab, a newline, a quote, or a non-UTF-8
byte -- is therefore misread or missed. internal/workflow/engine.go's
changedPaths (engine.go:1072) reads through the same parser.

gitx.Capture now carries an exact, NUL-parsed path set (Capture.Paths), taken
after artifact exclusions.

Make the change report's path information exact for pathnames Git quotes, while
keeping report.Change's meaning for its existing consumers. Add tests that fail
without the change.
```

Three defensible implementations exist, and the task deliberately names none of
them: change `FromDiff`'s signature to take the exact set; add a parallel
constructor and migrate consumers; or make the parser itself NUL-safe in place.

## Predictions, frozen

```text
route            architectural-authority-granted (report.go is OK with anchors)
risk             ARCHITECTURE_SENSITIVE, both files
routine          NOT routine
files            2-4, including tests
reviewer         codex (claude is the implementor and is excluded)
verdict          ACCEPT and REVISE are both genuinely plausible; I would not be
                 surprised by either
revision cycle   roughly even odds
```

**Disclosed prior, because it can contaminate the reading:** I would migrate the
consumers to `Capture.Paths` rather than harden the parser. If the worker
hardens the parser instead and the reviewer accepts, that is not a weaker
outcome, and this sentence exists so I cannot later pretend otherwise.

## The falsifier

```text
SUCCESS IS NOT "the task finished."
COMPLETE / REFUSED is a successful experiment. So is COMPLETE / ACCEPTED.
A run that cannot account for itself is the failure, whatever its verdict.

1  the reviewer evaluated a real behavioural implementation, not a restatement
2  if a revision occurred, T1 and T2 are distinguishable in the record
3  no verdict over T1 authorised T2 -- the accepting verdict names T2's tree
4  the minted C wraps exactly the tree of the ACCEPTING verdict
5  validation and audit correspond to the FINAL candidate state
6  the receipt is COMPLETE under either ACCEPT or REFUSE
7  nothing above required reading the event stream to establish
8  the schema version matches the vocabulary the receipt speaks
9  every fact the terminal needed was recorded before the edge that could
   terminate on it -- the candidate law under test, applied to its own
   experiment
```

## Stopping rule

ONE invocation. `-json`, `-timeout 25m`, `SENSEI_CODE_BENCHMARK=1`, in an
isolated shallow clone with origin removed. No opportunistic replanning. Exit 3
preserves a question. Whatever comes out is recorded, including a refusal, a
deferral, or a crash.

## What this run cannot establish

Nothing is admitted, and a single task on one repository is not evidence about
judgement in general. A REFUSE would say more about the control plane than
another uncontested ACCEPT, and neither settles whether the loop handles work
where the right answer is unclear to the humans as well.
