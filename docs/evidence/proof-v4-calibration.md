# proof-v4 — the two-task calibration

The mandate's step 4: run a tiny slice first, and unlock the full campaign only
if the scores are interpretable.

**They are.** That is the finding, and it is a change of state rather than a
result about Sensei-code.

## The four arms

| task | arm | terminal | oracle | wall |
|---|---|---|---|---|
| `internal-gitx-a4fa351` | RAW | `raw.completed` | **CORRECT** | 272s |
| `internal-gitx-a4fa351` | COLD | `workflow.failed` | INCORRECT | 137s |
| `internal-gitx-6460efd` | RAW | `raw.completed` | **CORRECT** | 199s |
| `internal-gitx-6460efd` | COLD | `workflow.timed_out` | INCORRECT | 1320s |

## What is established

**The oracle recognises correct solutions it did not write.** Twice. Under
proof-v3's withheld-test oracles the entire campaign scored `INCORRECT` and
could not say why; under behavioural contract oracles, two solutions written
from the task statement alone were recognised as correct.

That is the whole purpose of this benchmark version, and it is the first
`CORRECT` in the project's measurement history.

The gate behind it: both oracles had already separated REFERENCE, WRONG and
ALTERNATE before either arm ran. The ALTERNATE specimens were deliberately
unlike the accepted fix — `ls-files` + `--no-index` instead of
`--intent-to-add`; `GIT_OPTIONAL_LOCKS=0` instead of `--no-optional-locks` —
and the oracles recognised them anyway.

## What is NOT established

**COLD 0/2 is not evidence that governance is worse than the baseline.** Both
failures are operational and neither reached a correctness judgement:

- `internal-gitx-a4fa351` failed in 137 seconds because the awareness-graph
  backend was unreachable — `transport failed on all configured addresses`,
  `RST_STREAM`, `context deadline exceeded`. Transient: the graph answered
  `PREFLIGHT_STATUS_OK` minutes later. This is a service outage, and the record
  says `INCORRECT` only because the classifier did not recognise it at the time.
  Preserved as written; the classifier is fixed and the specimen is pinned.
- `internal-gitx-6460efd` ran the full 22 minutes and timed out. A run that did
  not conclude is not a wrong answer.

So the honest reading of this table is: **RAW is measurable and scored 2/2;
COLD is not yet measurable at all.** Two arms, two different operational
failures, zero governance signal.

That asymmetry is itself worth carrying into the full campaign: if governed arms
routinely exhaust 22 minutes while RAW finishes in 3–5, the campaign will
measure timeout behaviour rather than correctness, and the timeout will need
raising or the tasks shrinking — decided before the runs, not after.

## Instrument defects found by this slice

9. **An unreachable graph scored as a wrong answer.** Third instance of the same
   class, always in the direction that makes the product look worse. The
   distinction now pinned: a graph that is *down* is infrastructure; a graph
   that *answers* "I have nothing" is the product working.
10. **The boundary was measured with two different rulers.** `GovernedState()`
    filtered the harness's own output; `Evidence()` read the after-state raw. So
    the record this very call was about to write appeared on one side of the
    comparison only, and every arm reported a mutation it had not made.

Ten defects now, every one found by running the instrument rather than reading
it.

## Recommendation

The unlock condition is met: scores are interpretable. Before the full 30-arm
campaign, two things are worth settling first, because both change what the
numbers mean and neither should be decided once results exist.

1. **The remaining eight tasks need contract oracles and three specimens each.**
   Three of the five packages had v3 oracles bound to private identifiers, so
   those tasks need genuinely new probes, not translations.
2. **The governed timeout.** One of two COLD arms exhausted it. Running the
   campaign at 22 minutes would measure how often governance finishes in 22
   minutes, which is a real question but not the one being asked.
