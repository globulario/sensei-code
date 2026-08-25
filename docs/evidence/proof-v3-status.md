# Proof campaign — status, and what it has actually established

Companion to the generated report in `proof-v3-report.md`. That file is derived
mechanically from committed records; this one says what a reader should take
from it.

**Verdict: INCOMPLETE. 5 of 30 designed arm slots executed (17%).**

That is not a result about Sensei-code. It is a statement about how much
evidence exists, which is the honest thing to report and the thing a campaign
is most tempted to dress up.

## What is finished

| Definition-of-done item | state |
|---|---|
| 1. harness + integrity attacks | done — 12 attacks from the brief, mutation-verified |
| 2. truthful help surface | done — 5 of 14 public commands were undiscoverable |
| 3. calibration, both shapes | done — see below |
| 4. frozen ten-task manifest | done — 10 tasks, 5 linked, 14 exclusions with reasons |
| 5. primary RAW/COLD/WARM runs | **partial — 5 of 30 arm slots** |
| 6. four linked WARM↔COLD specimens | **not started — no WARM arm has run** |
| 7. observation-depth scoring | **not started** |
| 8. machine-readable artifacts | done — `benchmark/proof-v3/` |
| 9. human-readable report with a derived verdict | done — INCOMPLETE |
| 10. build/vet/test green | done |
| 11. no production mechanism added to improve the score | held |

## The instrument works, and that is established

The #62 non-convergence specimen was reproduced from this repository's own
recorded event stream, and independently confirms what that PR reported:

```
6 review cycles · 6 inspection reports · 18 findings
reviewers: Codex Codex Codex Claude Claude Claude
49.2 minutes · terminal workflow.failed · did not converge
```

The reviewer rotation is measured rather than asserted. Three cycles under
Codex, then three under Claude — precisely the moving standard #62 named.

The #79/#80 positive is weaker and says so: **its event stream was not
retained.** Governed runs execute in candidate worktrees whose session stores
are discarded with them, so the campaign's positive calibration rests on the
landed artifact and the PR record rather than on a replayed stream. That is a
real gap in the project's own evidence retention, found by trying to use it.

## What the executed arms show — and what they do not

Five arms ran. Every one scored `INCORRECT`.

- **COLD, `internal-gitx-a4fa351`**: 25 minutes, 5,499 events, a 15,749-byte
  candidate at cycle 1, two review cycles started, terminal `workflow.timed_out`.
- **RAW, four tasks**: 3–8 minutes each, real non-empty diffs, hidden test still
  failing.

**Do not read this as "the product fails".** One governed arm, timed out rather
than concluded, is not evidence about correct closure — and the pre-registered
gates refuse to place a verdict on it, which is why the report says INCOMPLETE
rather than RED. A RED derived from arms nobody ran would be exactly as
dishonest as a GREEN derived the same way, and easier to mistake for rigour.

## What the campaign has actually produced: six harness defects

Every one was found by *running* the harness, not by reading it. None was
caught by the curated test strings written first.

1. **A commit hash read as an HTTP status.** `certified_awareness_graph_commit:
   a4034c78…` contains `403`; a bare substring match classified a governed
   failure as infrastructure. Every false match would have licensed a retry the
   rule exists to forbid.
2. **Withholding the oracle by deletion made every governed arm unrunnable.** A
   governed run correctly refuses to start in a dirty checkout. Two correct
   rules composed into an arm that died in one second. The product rule was not
   weakened; the harness now commits the withholding and records the derived run
   base beside the pinned one.
3. **Boundary readings condemned the product for the operator's commits.** The
   author committed harness fixes during a 25-minute arm, so the governed
   checkout comparison could not attribute the change. An unmeasurable boundary
   is now recorded as absent, and fails closed for autonomy rather than counting
   as a violation.
4. **The append-only rule refused an attempt after paying for it** — 25 minutes
   of provider time to learn the id was taken.
5. **RAW authenticated as somebody else and was scored for it.** An ambient
   `ANTHROPIC_API_KEY` overrode the subscription login; the arm died in 174
   seconds and was recorded `INCORRECT` — a wrong answer it never had the chance
   to give. The mirror of defect 1, and the more damaging direction: it makes the
   baseline look worse, which flatters the governed arms.
6. **RAW was denied Bash**, so it wrote code it could not compile or test. A
   handicapped baseline, corrected and recorded as a distinct provider identity
   so the two configurations cannot be silently combined.

Three benchmark versions are pinned. `proof-v1/` and `proof-v2/` each preserve
their wrong records with a README explaining why, per the brief's restart rule:
never erase the failed evidence.

## What it costs to finish

Governed arms run 22–25 minutes each. The remaining 25 slots are roughly nine
hours of wall time plus RAW, and WARM additionally requires the linked-task
sequence to run in order.

## The one thing to take from this

The campaign has not yet measured the product. It has measured the measuring
instrument, six times, and each time the instrument was wrong in a way that
would have produced a confident number.

That is worth stating plainly, because it is the argument for the campaign
rather than against it: had these runs been done informally — a few tasks, eyes
on the output, a summary written afterwards — five of those six defects would
have produced numbers nobody questioned, and one of them would have condemned
the product for the operator's own commits.
