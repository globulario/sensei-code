# Proof campaign — status, and what it has actually established

Companion to the generated report in `proof-v3-report.md`. That file is derived
mechanically from committed records; this one says what a reader should take
from it.

**Verdict: INCOMPLETE. 7 of 30 designed arm slots executed (23%).**

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
| 5. primary RAW/COLD/WARM runs | **partial — 7 of 30 arm slots, and halted: see the oracle finding below** |
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

## The finding that stops the campaign: the oracle measures the wrong thing

Every executed arm scored `INCORRECT`. The reason is not that the tasks were
not solved. It is that the oracle cannot tell.

The withheld tests fail to **compile**, on undefined identifiers:

```
internal/tui/scroll_test.go:28:4: m.scrollUp undefined (type Model has no field or method scrollUp)
internal/tui/scroll_test.go:51:25: m.scroll  undefined (type Model has no field or method scroll)

internal/behavioral/behavioral_test.go:15:36: undefined: Config
internal/behavioral/behavioral_test.go:21:13: undefined: New
internal/behavioral/behavioral_test.go:21:96: undefined: ErrNotConfigured
```

These are the private and exported names the **accepted fix happened to
choose**. A worker that solved the problem correctly with different names fails
identically to one that did nothing. The oracle is testing implementation
shape, not behaviour, and it cannot distinguish

    solved it differently        from        did not solve it

The brief anticipated exactly this line and drew it: *"the oracle may know the
expected behavior; the worker may not know the implementation."* This oracle
knows the implementation and requires it.

**Consequence: the current corpus cannot support C1 (autonomous correct
closure) at all**, for any arm. The correctness column measures whether a worker
guessed an API surface, which is not the claim under test.

It is not a defect that favours one arm: RAW, COLD and WARM are penalised
identically, so a *comparison* between them retains some meaning. The absolute
rates have none.

Running the remaining 23 arm slots — roughly nine hours — would have produced a
larger table of the same uninterpretable number. Stopping here is the campaign
working: this is precisely the thing it exists to find before the budget is
spent, and it was found after twelve arms.

### What would fix it

The brief ranks three oracle kinds and this corpus used the first. The repair is
to move down the list, per task:

1. **behavioural probes built from the historical contract** — assert what the
   change must make true, in terms that do not name the implementation;
2. **independent review against a frozen rubric**, by a provider that authored
   no candidate.

Either is a new benchmark version with a new manifest hash, because it changes
the oracle. Neither is a change to the product.

## What the executed arms show — and what they do not

Twelve attempts across seven task/arm slots. Every one scored `INCORRECT`, and
the section above says why that number cannot be read as capability.

- **COLD, `internal-gitx-a4fa351`**: 25 minutes, 5,499 events, a 15,749-byte
  candidate at cycle 1, two review cycles started, terminal `workflow.timed_out`.
- **RAW, six tasks**: 3–8 minutes each, real non-empty diffs, withheld tests
  failing to compile against names the worker had no way to know.

**Do not read this as "the product fails".** One governed arm that timed out
rather than concluded is not evidence about correct closure, and the oracle
behind every other row cannot decide correctness at all. The pre-registered
gates refuse to place a verdict, which is why the report says INCOMPLETE rather
than RED. A RED derived from arms nobody ran, scored by an oracle that cannot
measure, would be exactly as dishonest as a GREEN derived the same way — and
easier to mistake for rigour.

## What the campaign has actually produced: eight defects in its own instrument

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
7. **The instrument blinded itself.** proofbench writes its records into
   `benchmark/` inside the governed checkout, and the boundary check requires a
   quiescent checkout — so after the first arm every later boundary reading was
   discarded as unmeasurable. The campaign would have run to completion
   reporting that it could not measure the one safety property it exists to
   check, because of its own bookkeeping.
8. **The oracle cannot measure correctness**, above. The largest of the eight,
   and the only one that invalidates a claim rather than a number.

Three benchmark versions are pinned. `proof-v1/` and `proof-v2/` each preserve
their wrong records with a README explaining why, per the brief's restart rule:
never erase the failed evidence.

## What it costs to finish

Not nine hours of runs — a new oracle first.

The remaining 23 arm slots are roughly nine hours of wall time, and running them
against the present oracle would buy a larger table of the same uninterpretable
number. The corpus needs behavioural probes or an independent-review oracle
(a new benchmark version, new manifest hash), and only then is the run budget
worth spending.

## The one thing to take from this

The campaign has not yet measured the product. It has measured the measuring
instrument, six times, and each time the instrument was wrong in a way that
would have produced a confident number.

That is worth stating plainly, because it is the argument for the campaign
rather than against it: had these runs been done informally — a few tasks, eyes
on the output, a summary written afterwards — seven of those eight defects would
have produced numbers nobody questioned. One would have condemned the product
for the operator's own commits. And the eighth would have reported a confident
0% correct-closure rate for a model that may well have solved several of the
tasks.
