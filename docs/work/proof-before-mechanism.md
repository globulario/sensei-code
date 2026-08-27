# Implementation brief: prove Sensei-code before adding more mechanism

## Why this exists

Sensei-code has crossed an important line: the question is no longer whether the architecture can work at all.

The self-repair recorded in #79/#80 is a real existence proof. Sensei-code observed a defect in its own router, opened a separate governed repair task, planned and implemented the repair in an isolated candidate, passed independent review, and stopped at `retained` for a human landing decision. No human supplied the technical solution.

But the evidence base is still too small to support a product-level claim. The counterexample is #62: six review cycles, roughly fifty minutes and roughly ten dollars, with no convergence on a question partly answerable from repository source.

The current problem is therefore not missing mechanism. It is missing measurement.

This PR supersedes the separate measurement drafts #76, #77, and #78. Their questions remain useful, but they must be answered inside one controlled campaign rather than becoming three more places where mechanism can outrun use.

The governing question is:

> On real engineering tasks, does Sensei-code produce correct autonomous outcomes often enough, safely enough, and with enough reuse of accumulated project knowledge to justify its additional machinery and cost over a plain coding agent?

A result of `NO`, `MIXED`, or `INCONCLUSIVE` is valid. The campaign must be capable of disproving the project claim.

## Rule zero: mechanism freeze

Until the first proof report is attached to this PR:

- do not add a new governance stage;
- do not add a new authority class;
- do not add a new derivation family merely to improve benchmark reach;
- do not add a permanent second observation worker;
- do not change reviewer policy merely to make convergence look better;
- do not seed project-specific answers into benchmark knowledge;
- do not change prompts between benchmark arms after results are visible.

Only changes required to **measure existing behavior faithfully** are in scope.

If a run exposes a real product defect, preserve the run, open a separate issue/repair task, and keep the defect out of this PR unless it makes the harness itself incapable of recording the experiment. If a harness-blocking defect must be fixed here, record the pre-fix failure and restart the affected campaign slice from a newly pinned benchmark version. Never erase the failed evidence.

## Claims under test

The report must separate these claims. Do not collapse them into one score.

### C1 — autonomous correct closure

Sensei-code can complete ordinary engineering tasks correctly without a human supplying the technical answer.

A task counts as **autonomous-correct** only when:

1. the candidate reaches a defined terminal outcome;
2. the independent task oracle passes;
3. no human supplied a technical premise, diagnosis, implementation choice, or fix during the run;
4. the governed checkout was not mutated outside the allowed candidate/landing boundary.

A human choosing whether to land a retained candidate does not invalidate autonomy. A human telling the system what the technical answer is does.

### C2 — governance safety adds value rather than ceremony

Sensei-code must not gain its result by silently accepting bad candidates or weakening review.

Measure:

- false grants / incorrect candidates that reached an acceptance-equivalent terminal state;
- bad candidates stopped before landing;
- correct candidates stopped by governance despite a passing oracle;
- canonical-checkout or authority-boundary violations;
- defects discovered in the reasoning/control path even when the eventual output happened to be correct.

A safety catch counts only when an independent oracle confirms that the blocked move was actually wrong, unsafe, outside authority, or contradicted by repository evidence. A refusal is not automatically a success.

### C3 — convergence is bounded and intelligible

The #62 failure mode must be measured directly.

A review loop must either:

- converge to a stable accepted/rejected candidate; or
- terminate with a structured reason that identifies why the remaining blocker cannot be resolved inside the candidate/task authority.

Repeated prose changes to the same unresolved objection do not count as new progress.

### C4 — governed experience compounds

Knowledge created by an earlier task must make a later **different** task measurably better.

Merely retrieving an old artifact into a prompt does not count. The artifact must change structured behavior: route, required investigation, forbidden move, selected verification, scope, or another machine-observable decision.

### C5 — deeper observation earns its cost

The question from #78 becomes a secondary endpoint rather than a new architecture project.

When a first observation is naturally depth-limited, a second read-only pass is scored for **verified novel yield**, additional relevant scope, cost, and duplication. Do not add a permanent observation stage unless repeated evidence supports it.

## Harness, not product mechanism

Implement a benchmark harness outside the production control plane. Prefer a small Go command such as:

```text
go run ./cmd/proofbench validate --manifest benchmark/proof-v1/manifest.yaml
go run ./cmd/proofbench run      --manifest benchmark/proof-v1/manifest.yaml
go run ./cmd/proofbench report   --manifest benchmark/proof-v1/manifest.yaml
```

Names may vary to fit the repository, but the separation must remain clear: this is measurement infrastructure, not another runtime authority layer.

The harness may consume existing event streams, receipts, run artifacts, candidate metadata, provider usage, and git state. Add measurement-only fields/events only when existing evidence cannot answer a required metric. Such additions must not change routing, authorization, review, admission, or candidate semantics.

The harness must be able to run without editing the benchmark manifest.

## Public-surface sanity check

Claude's criticism also identified a smaller symptom: capabilities can exist in the command switch while remaining absent from normal help.

Before running the campaign, make the public help surface truthful for commands the campaign relies on. At minimum, if `observe` and `audit-repair` are supported public commands, they must be discoverable from the ordinary CLI help path. Do not redesign the CLI. This is a usage-surface correction required so the benchmark exercises a tool a user can actually discover.

Pin this with a test that compares the supported public command set to the help-visible set, or another equally direct invariant. Hidden/internal commands may remain hidden if they are explicitly classified as such.

## Experimental arms

For every primary task, run isolated fresh sessions from the same repository base and task statement.

### Arm A — RAW

The same primary author provider/model works directly in an isolated worktree without Sensei-code governance or project knowledge.

It may read repository source, run tests, and use the ordinary tools available to that provider. It does not receive Sensei graph output, Sensei durable knowledge, prior benchmark transcripts, or the hidden oracle.

This is the baseline for the question: **what did the control plane buy over the coding model itself?**

### Arm B — COLD

Sensei-code runs normally, but project-specific durable benchmark knowledge begins from the state available at the task's pinned base. It may use generic built-in machinery and repository evidence, but it must not inherit facts learned from later benchmark tasks or from the solution being replayed.

### Arm C — WARM

Sensei-code runs with only the durable knowledge produced by earlier benchmark tasks in the same declared sequence, revalidated against the current task base where required.

WARM must never receive the answer to the target task. Its advantage may come only from experience created by earlier **different** tasks.

## Isolation requirements

Every arm/run gets:

- its own worktree or checkout;
- its own candidate/workspace state;
- its own project knowledge store or snapshot where applicable;
- a fresh provider session with no conversation carry-over;
- pinned provider/model identity and provider role mapping;
- the same task text and base commit;
- the same environment variables except arm-specific Sensei plumbing;
- no access to another arm's transcript or candidate;
- no hidden-oracle content in prompts or graph state.

Randomize or counterbalance RAW/COLD/WARM execution order per task so provider service drift does not always favor one arm. Record the order.

If a provider/model/version changes inside a paired comparison, the pair is invalid for comparative claims and must be reported as such rather than silently combined.

## Corpus

### Calibration specimens — do not count toward the primary n

The harness must first prove it can faithfully record both a known win and a known failure.

1. **#79/#80 self-repair** — record the already-demonstrated successful loop as a positive calibration specimen.
2. **#62 non-convergence** — replay or reduce the six-cycle specimen while preserving the failure shape: independent review, handoff, blocking objections, and at least one blocker outside candidate authority.

Calibration exists to validate the measuring instrument. Do not use these two known outcomes to inflate the primary success rate.

### Primary corpus — 10 real tasks minimum

Pre-register at least ten real repository tasks before running the first primary arm.

Prefer historical defects or accepted changes that have:

- a pinned pre-fix base commit;
- a task statement available without exposing the eventual implementation;
- a deterministic or independently reviewable oracle;
- meaningful code work rather than docs-only edits;
- no requirement for a human to invent product intent that was absent from repository authority.

Historical tasks are useful because the accepted regression test or behavior can act as a hidden oracle while the agent works from the pre-fix repository state.

Do **not** choose only tasks already known to align with current Sensei strengths. Define the eligibility rule first, then choose deterministically from the eligible set, for example chronological order within a pinned PR/issue range. Record excluded candidates and the exclusion reason.

At least four of the ten tasks must form **linked later-task specimens**: an earlier benchmark task may create durable knowledge relevant to a later, different task. These later tasks are the WARM vs COLD compounding comparison.

The manifest must be committed before primary runs begin and include a hash/version. Changing task text, base SHA, oracle, arm configuration, or selection rules creates a new benchmark version.

## Hidden task oracle

The worker must not score itself.

Each task declares an oracle unavailable to the worker during implementation. Prefer, in order:

1. deterministic regression tests/assertions that existed in the accepted fix but are withheld from the worker checkout until evaluation;
2. deterministic behavioral probes built from the historical contract;
3. independent evaluation by a provider that was not the candidate author, using a frozen rubric and the repository evidence.

The oracle returns one of:

```text
CORRECT
INCORRECT
INCONCLUSIVE
```

`INCONCLUSIVE` must not be converted to success because the candidate looks plausible.

For historical replay, do not expose the accepted patch to any arm. The oracle may know the expected behavior; the worker may not know the implementation.

## Attempt ledger: no survivor bias

Every attempt is append-only evidence.

Record:

- task id;
- benchmark version and manifest hash;
- arm;
- attempt number;
- repository/base SHA;
- provider/model identities and roles;
- start/end timestamps;
- terminal status;
- oracle result;
- human interventions with classification;
- observation rounds;
- knowledge gaps and closure sources;
- review cycles;
- objections, with stable identity when possible;
- candidate diff hash;
- verification commands/results;
- wall time;
- token/usage/cost data when available;
- new durable artifacts;
- durable artifacts consumed/revalidated;
- git cleanliness / mutation evidence;
- infrastructure failures;
- artifact hashes for supporting logs/receipts.

A failed run remains in the ledger even if an infrastructure retry is allowed.

One retry is allowed only for an externally attributable infrastructure failure such as provider outage/quota/auth failure. Both attempts remain visible. A second infrastructure failure yields `NO_RESULT` for operational-reliability reporting.

Do not retry semantic failures until one happens to pass.

## Primary metrics

For RAW, COLD, and WARM report at minimum:

1. **correct closure rate** = `CORRECT terminal outcomes / eligible runs`;
2. **autonomous-correct rate** = correct outcomes with zero human technical answers;
3. **human technical intervention rate**;
4. **false-grant rate**;
5. **correct-candidate false-block rate**;
6. **review cycles to terminal**;
7. **non-convergent/runaway rate**;
8. **wall time**;
9. **provider cost** when available;
10. **verification/regression failure rate**.

For COLD vs WARM additionally report:

11. **rediscovery count/rate**;
12. **durable knowledge reuse/revalidation rate**;
13. **closure yield** = autonomously closed technical gaps / technical gaps encountered;
14. **repeat-failure avoidance** attributable to a prior scar/forbidden fix/test/contract artifact;
15. **structured behavior changes caused by prior knowledge**.

For observation-depth specimens report:

16. verified novel F2 findings / total F2 findings;
17. duplicate/rephrased F1 findings;
18. additional relevant scope inspected;
19. incremental cost/time.

Do not use graph triple count, prompt length, or number of stored artifacts as a primary success metric.

## Statistical/reporting rules

The report must show raw counts, not only percentages.

For binary rates include a 95% Wilson interval. For paired RAW↔COLD/WARM correctness comparisons report the per-task outcome matrix and an exact paired test when the sample supports it. For continuous metrics show median and distribution/range; do not hide long-tail runs behind a mean.

With ten tasks this remains an engineering evidence campaign, not a universal population estimate. Say so explicitly. Do not write `proven` where the data supports only `promising` or `inconclusive`.

## Pre-registered decision gates

These gates are intentionally defined before results exist.

### GREEN — enough evidence to continue treating Sensei-code as a working tool

All must hold on the primary corpus:

1. at least **9/10** tasks end `CORRECT` under governed COLD or WARM execution;
2. at least **8/10** are autonomous-correct with no human technical answer;
3. **zero false grants** and zero governed-checkout authority/mutation violations;
4. no unclassified runaway review loop; #62 reaches a stable terminal result or explicit outside-candidate blocker without weakening reviewer independence;
5. governed correct completion is not worse than RAW by more than one task;
6. on the four linked later-task specimens, WARM measurably improves at least **3/4** over COLD in rediscovery, human intervention, required investigation, review cycles, or prevented repeat failure, with no correctness regression;
7. aggregate rediscovery across the linked WARM tasks is at least **30% lower** than their COLD counterparts, unless the COLD rediscovery denominator is zero, in which case report the compounding claim as not testable rather than passing it;
8. median governed provider cost is no more than **3× RAW**, **or** any larger premium is accompanied by an independently verified increase in correct/autonomous closure or prevention of a bad candidate. Report the ratio either way.

GREEN does not mean finished. It means the project has crossed from existence proof to repeatable engineering evidence.

### RED — evidence says the current tool is not ready to claim reliability

Any one of these is sufficient:

- fewer than **6/10** governed primary tasks are correct;
- any false grant that would allow an independently incorrect candidate through the claimed safety boundary;
- repeated unclassified review runaway after the #62 calibration;
- WARM shows no structured reuse benefit on any linked specimen while adding cost;
- median governed cost exceeds **3× RAW** with no correctness, autonomy, safety, or reuse benefit;
- the harness cannot reproduce the known #79/#80 positive or #62 negative shape faithfully.

### AMBER / INCONCLUSIVE

Everything between GREEN and RED is reported as mixed/inconclusive with the blocking metrics named. Do not tune thresholds or add mechanism inside this PR to turn AMBER into GREEN.

## Reviewer-convergence endpoint from #76

Do not preselect a new reviewer policy.

For the #62 calibration and any naturally multi-cycle primary run, record:

- reviewer identity per cycle;
- worker identity per cycle;
- stable objection ids;
- objection status: new / repeated / resolved / outside-candidate-authority;
- candidate delta per cycle;
- time/cost per cycle.

If current behavior already converges after the intervening Sensei-code changes, record that and stop. If it does not, the report may recommend a follow-up experiment, but this PR must not silently implement reviewer pinning, requirement carry-forward, or termination semantics merely to make the benchmark green.

## Observation-depth endpoint from #78

Select the existing depth-limited `testname` audit and, if it produces material verified novel yield, two additional naturally depth-limited observations.

Round 2 receives F1 and the instruction to continue without repeating F1. Score F2 separately. Permission to find nothing remains explicit.

A permanent second observation stage is justified only by repeated substantial **verified novel** yield at acceptable precision. Otherwise the result is evidence not to build it.

## Required artifacts

Commit non-secret evidence under a stable layout such as:

```text
benchmark/proof-v1/manifest.yaml
benchmark/proof-v1/runs/<task>/<arm>/<attempt>.json
benchmark/proof-v1/report.json
docs/evidence/proof-v1-report.md
```

Exact paths may vary, but the machine-readable and human-readable layers must both exist.

Large raw transcripts may remain outside git if necessary, but every committed run record must contain hashes/identifiers sufficient to bind the summary to the supporting receipts. Never commit provider secrets or auth material.

The report must include:

- the frozen task manifest and selection rule;
- excluded-task list with reasons;
- all arm outcomes;
- calibration results;
- primary metrics and confidence intervals;
- RAW vs governed comparison;
- COLD vs WARM compounding comparison;
- #62 convergence evidence;
- observation-depth evidence;
- cost/time distributions;
- every human technical intervention;
- every false grant/false block;
- defects discovered during the campaign;
- final verdict: `GREEN`, `AMBER`, or `RED`, derived mechanically from the pre-registered gates.

## Harness tests / attacks

At minimum pin these:

- changing a manifest after a run invalidates or versions the result;
- a run from the wrong base SHA is refused;
- provider/model mismatch is visible and excludes an invalid paired comparison;
- duplicate attempt ids cannot overwrite earlier evidence;
- missing cost data remains `unknown`, never zero;
- a failed semantic run cannot be replaced by a successful retry under the same attempt id;
- hidden-oracle content is not placed in the worker prompt/context by the harness;
- RAW cannot accidentally receive Sensei project knowledge;
- COLD cannot accidentally receive WARM state;
- candidate diff hash and git-cleanliness evidence are recorded;
- report aggregation includes failed and `NO_RESULT` attempts;
- report gate evaluation is deterministic from the committed run records.

## Definition of done

This PR is **not done when the harness compiles**.

It is done only when:

1. the harness and its integrity tests are implemented;
2. the public help surface is truthful for supported commands used by the campaign;
3. #79/#80 positive calibration and #62 negative/convergence calibration are recorded;
4. the ten-task manifest is frozen before primary execution;
5. all primary RAW/COLD/WARM runs required by the manifest are executed or explicitly recorded `NO_RESULT` under the retry rule;
6. at least four linked WARM↔COLD specimens are scored;
7. observation-depth is scored per the rule above;
8. machine-readable evidence is committed;
9. the markdown report derives a `GREEN`, `AMBER`, or `RED` verdict from the pre-registered gates;
10. build, vet, ordinary tests, and benchmark-harness tests are green;
11. no production mechanism was added merely to improve the score.

Do not merge a brief-only PR. Do not merge an empty harness. Do not merge with the result section blank.

The deliverable is not another promise about Sensei-code.

**The deliverable is evidence that can tell us we are wrong.**
