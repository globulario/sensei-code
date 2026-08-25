# Proof campaign report — proof-v3

**Verdict: INCOMPLETE**

Manifest `sha256:bd8ac1395f697f79f922f04ef87d6e8b79bbeaf7dbec8f141500a910ef3059b3`. 10 primary task(s).

**Coverage: 5 of 30 designed arm slots were executed.** 25 were never run, and are listed below rather than omitted -- a report showing only the arms that happened to run would describe a smaller, better-behaved campaign than the one that was designed.

This is an engineering evidence campaign over 10 tasks, not a population estimate. Intervals are wide by construction. Where the data supports only "promising" or "inconclusive", it does not support "proven".

## Calibration — can the instrument record a known win and a known failure?

- **task-1787365451558361320** — expected `negative / non_convergence`, observed `non-convergence with a rotating reviewer`: **reproduced**. 6 cycles, 6 reports, 18 findings, reviewers [Codex Codex Codex Claude Claude Claude], 49 min, terminal workflow.failed
- **self-repair-79-80** — expected `positive`, observed `landed repair with its pinning tests`: **reproduced**. 2f5ee40e42b4: 2 file(s), 176 insertion(s), tests=true, event stream retained=false — the run's own event stream was not retained: governed runs execute in candidate worktrees whose session stores are discarded with them, so this rests on the landed artifact and the PR record rather than on a replayed stream

## Per-task outcome matrix

| task | linked | RAW | COLD | WARM |
|---|---|---|---|---|
| internal-tui-ea046ba |  | INCORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-tui-be512db | yes | NOT_EXECUTED | NOT_EXECUTED | NOT_EXECUTED |
| internal-mcpconfig-110678d |  | NOT_EXECUTED | NOT_EXECUTED | NOT_EXECUTED |
| internal-mcpconfig-21c5a6b | yes | NOT_EXECUTED | NOT_EXECUTED | NOT_EXECUTED |
| internal-behavioral-a453be8 |  | INCORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-behavioral-055fe6b | yes | NOT_EXECUTED | NOT_EXECUTED | NOT_EXECUTED |
| internal-doctor-853fbe3 |  | INCORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-doctor-7a56cd2 | yes | NOT_EXECUTED | NOT_EXECUTED | NOT_EXECUTED |
| internal-gitx-a4fa351 |  | INCORRECT | INCORRECT | NOT_EXECUTED |
| internal-gitx-6460efd | yes | NOT_EXECUTED | NOT_EXECUTED | NOT_EXECUTED |

## Primary metrics

| metric | RAW | COLD | WARM |
|---|---|---|---|
| runs recorded | 4 | 1 | 0 |
| NO_RESULT | 0 | 0 | 0 |
| correct closure | 0/4 = 0% [0–49%] | 0/1 = 0% [0–79%] | n/a (no eligible runs) |
| autonomous-correct | 0/4 = 0% [0–49%] | 0/1 = 0% [0–79%] | n/a (no eligible runs) |
| human technical intervention | 0/4 = 0% [0–49%] | 0/1 = 0% [0–79%] | n/a (no eligible runs) |
| false grant | 0/4 = 0% [0–49%] | 0/1 = 0% [0–79%] | n/a (no eligible runs) |
| false block (correct, stopped) | 0/4 = 0% [0–49%] | 0/1 = 0% [0–79%] | n/a (no eligible runs) |
| non-convergent | 0/4 = 0% [0–49%] | 0/1 = 0% [0–79%] | n/a (no eligible runs) |
| verification failure | 4/4 = 100% [51–100%] | 1/1 = 100% [21–100%] | n/a (no eligible runs) |
| closure yield | n/a (no eligible runs) | n/a (no eligible runs) | n/a (no eligible runs) |
| durable knowledge reuse | 0/4 = 0% [0–49%] | 0/1 = 0% [0–79%] | n/a (no eligible runs) |
| review cycles | median 0.0 (range 0.0–0.0, n=4) | median 2.0 (range 2.0–2.0, n=1) | n/a |
| wall time | median 5.8 min (range 2.9–8.4, n=4) | median 25.0 min (range 25.0–25.0, n=1) | n/a |
| provider cost | unknown (4 observation(s) with no data) | unknown (1 observation(s) with no data) | n/a |
| rediscovery | median 0.0 (range 0.0–0.0, n=4) | median 0.0 (range 0.0–0.0, n=1) | n/a |

## Compounding — COLD vs WARM on linked specimens

| task | comparable | COLD | WARM | rediscovery | human tech | cycles | improved |
|---|---|---|---|---|---|---|---|
| internal-tui-be512db | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |
| internal-mcpconfig-21c5a6b | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |
| internal-behavioral-055fe6b | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |
| internal-doctor-7a56cd2 | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |
| internal-gitx-6460efd | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |

## Every human technical intervention

None recorded.

## False grants and false blocks

- arm slots never executed (25): internal-tui-ea046ba/COLD, internal-tui-ea046ba/WARM, internal-tui-be512db/RAW, internal-tui-be512db/COLD, internal-tui-be512db/WARM, internal-mcpconfig-110678d/RAW, internal-mcpconfig-110678d/COLD, internal-mcpconfig-110678d/WARM, internal-mcpconfig-21c5a6b/RAW, internal-mcpconfig-21c5a6b/COLD, internal-mcpconfig-21c5a6b/WARM, internal-behavioral-a453be8/COLD, internal-behavioral-a453be8/WARM, internal-behavioral-055fe6b/RAW, internal-behavioral-055fe6b/COLD, internal-behavioral-055fe6b/WARM, internal-doctor-853fbe3/COLD, internal-doctor-853fbe3/WARM, internal-doctor-7a56cd2/RAW, internal-doctor-7a56cd2/COLD, internal-doctor-7a56cd2/WARM, internal-gitx-a4fa351/WARM, internal-gitx-6460efd/RAW, internal-gitx-6460efd/COLD, internal-gitx-6460efd/WARM
- false grants: none
- false blocks: none
- NO_RESULT attempts: none
- boundary unmeasurable (governed checkout not quiescent at arm start): internal-gitx-a4fa351/COLD/1

## Pre-registered gates

Thresholds were frozen in `docs/work/proof-before-mechanism.md` before any result existed and are transcribed as constants in `gates.go`. This verdict is a function of the committed run records.

| gate | kind | claim | result | detail |
|---|---|---|---|---|
| C0 | COVERAGE | at least 67% of designed arm slots executed | **fail** | 5 of 30 executed (17%) — the count-based gates presuppose that the arms ran, so they are not evaluated. A campaign that did not gather the evidence has not shown the product to be anything. |

**Verdict: INCOMPLETE**
