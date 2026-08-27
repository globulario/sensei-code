# Proof campaign report — proof-v5

**Verdict: RED**

Manifest `sha256:14a74f47a427cf9ac713a29bbd3cba77b3a032c74cf243b094111a7d6976361f`. 11 primary task(s).

**Coverage: 11 of 33 designed arm slots were executed.** 22 were never run, and are listed below rather than omitted -- a report showing only the arms that happened to run would describe a smaller, better-behaved campaign than the one that was designed.

This is an engineering evidence campaign over 11 tasks, not a population estimate. Intervals are wide by construction. Where the data supports only "promising" or "inconclusive", it does not support "proven".

## Calibration — can the instrument record a known win and a known failure?

None recorded. The instrument is unvalidated.


## Per-task outcome matrix

| task | linked | RAW | COLD | WARM |
|---|---|---|---|---|
| internal-gitx-a4fa351 |  | CORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-gitx-6460efd | yes | CORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-tui-ea046ba |  | CORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-tui-be512db | yes | INCORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-assist-5dfffad |  | CORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-decision-6cf23e8 |  | CORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-agent-01f3fe1 |  | CORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-session-4d32937 |  | CORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-architect-2e095c4 |  | INCORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-setup-e645669 |  | CORRECT | NOT_EXECUTED | NOT_EXECUTED |
| internal-setup-16ecbc3 | yes | INCORRECT | NOT_EXECUTED | NOT_EXECUTED |

## The two rates

Correctness and delivery are separate axes and are never collapsed. A run that did not reach an evaluable candidate is NOT_EVALUATED for correctness -- it cannot be called wrong for code it never wrote -- and counts as a failure for end-to-end success, because a task the product could not deliver on is a product failure whatever the code would have been.

Operational budget: **22m per arm, frozen**. The system cannot improve its score by taking longer.

| arm | engineering correctness | end-to-end success | NOT_EVALUATED | terminals |
|---|---|---|---|---|
| RAW | 8/11 = 73% [43–90%] | 8/11 = 73% [43–90%] | 0 | COMPLETED 11 |
| COLD | n/a (no eligible runs) | 0/11 = 0% [0–26%] | 0 | none run |
| WARM | n/a (no eligible runs) | 0/11 = 0% [0–26%] | 0 | none run |

*Engineering correctness is CORRECT / (CORRECT + INCORRECT): when the system produced an evaluable solution, was it right? End-to-end success is delivered / all 11 scheduled arms per condition: could it be given a task and return a correct result inside the budget?*

### Every arm, both axes

| attempt | terminal | correctness | delivered | wall | cause |
|---|---|---|---|---|---|
| internal-gitx-a4fa351/RAW/1 | COMPLETED | CORRECT | yes | 284s |  |
| internal-gitx-6460efd/RAW/1 | COMPLETED | CORRECT | yes | 220s |  |
| internal-tui-ea046ba/RAW/1 | COMPLETED | CORRECT | yes | 370s |  |
| internal-tui-be512db/RAW/1 | COMPLETED | INCORRECT |  | 654s |  |
| internal-assist-5dfffad/RAW/1 | COMPLETED | CORRECT | yes | 110s |  |
| internal-decision-6cf23e8/RAW/1 | COMPLETED | CORRECT | yes | 156s |  |
| internal-agent-01f3fe1/RAW/1 | COMPLETED | CORRECT | yes | 107s |  |
| internal-session-4d32937/RAW/1 | COMPLETED | CORRECT | yes | 111s |  |
| internal-architect-2e095c4/RAW/1 | COMPLETED | INCORRECT |  | 472s |  |
| internal-setup-e645669/RAW/1 | COMPLETED | CORRECT | yes | 264s |  |
| internal-setup-16ecbc3/RAW/1 | COMPLETED | INCORRECT |  | 314s |  |

## Primary metrics

| metric | RAW | COLD | WARM |
|---|---|---|---|
| runs recorded | 11 | 0 | 0 |
| NO_RESULT | 0 | 0 | 0 |
| correct closure | 8/11 = 73% [43–90%] | n/a (no eligible runs) | n/a (no eligible runs) |
| autonomous-correct | 8/11 = 73% [43–90%] | n/a (no eligible runs) | n/a (no eligible runs) |
| human technical intervention | 0/11 = 0% [0–26%] | n/a (no eligible runs) | n/a (no eligible runs) |
| false grant | 0/11 = 0% [0–26%] | n/a (no eligible runs) | n/a (no eligible runs) |
| false block (correct, stopped) | 8/11 = 73% [43–90%] | n/a (no eligible runs) | n/a (no eligible runs) |
| non-convergent | 0/11 = 0% [0–26%] | n/a (no eligible runs) | n/a (no eligible runs) |
| verification failure | 3/11 = 27% [10–57%] | n/a (no eligible runs) | n/a (no eligible runs) |
| closure yield | n/a (no eligible runs) | n/a (no eligible runs) | n/a (no eligible runs) |
| durable knowledge reuse | 0/11 = 0% [0–26%] | n/a (no eligible runs) | n/a (no eligible runs) |
| review cycles | median 0.0 (range 0.0–0.0, n=11) | n/a | n/a |
| wall time | median 4.4 min (range 1.8–10.9, n=11) | n/a | n/a |
| provider cost | unknown (11 observation(s) with no data) | n/a | n/a |
| rediscovery | median 0.0 (range 0.0–0.0, n=11) | n/a | n/a |

## Compounding — COLD vs WARM on linked specimens

| task | comparable | COLD | WARM | rediscovery | human tech | cycles | improved |
|---|---|---|---|---|---|---|---|
| internal-gitx-6460efd | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |
| internal-tui-be512db | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |
| internal-setup-16ecbc3 | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |

## Every human technical intervention

None recorded.

## False grants and false blocks

- arm slots never executed (22): internal-gitx-a4fa351/COLD, internal-gitx-a4fa351/WARM, internal-gitx-6460efd/COLD, internal-gitx-6460efd/WARM, internal-tui-ea046ba/COLD, internal-tui-ea046ba/WARM, internal-tui-be512db/COLD, internal-tui-be512db/WARM, internal-assist-5dfffad/COLD, internal-assist-5dfffad/WARM, internal-decision-6cf23e8/COLD, internal-decision-6cf23e8/WARM, internal-agent-01f3fe1/COLD, internal-agent-01f3fe1/WARM, internal-session-4d32937/COLD, internal-session-4d32937/WARM, internal-architect-2e095c4/COLD, internal-architect-2e095c4/WARM, internal-setup-e645669/COLD, internal-setup-e645669/WARM, internal-setup-16ecbc3/COLD, internal-setup-16ecbc3/WARM
- provider-configuration mismatch (0): none
- false grants: none
- false blocks: internal-agent-01f3fe1/RAW/1, internal-assist-5dfffad/RAW/1, internal-decision-6cf23e8/RAW/1, internal-gitx-6460efd/RAW/1, internal-gitx-a4fa351/RAW/1, internal-session-4d32937/RAW/1, internal-setup-e645669/RAW/1, internal-tui-ea046ba/RAW/1
- NO_RESULT attempts: none
- boundary unmeasurable (governed checkout not quiescent at arm start): none

## Pre-registered gates

Thresholds were frozen in `docs/work/proof-before-mechanism.md` before any result existed and are transcribed as constants in `gates.go`. This verdict is a function of the committed run records.

| gate | kind | claim | result | detail |
|---|---|---|---|---|
| R6 | RED | the harness reproduces the known #79/#80 positive and #62 negative shapes | **fail** | positive=false negative=false — an unvalidated instrument invalidates the campaign regardless of coverage |
| C0 | COVERAGE | at least 67% of designed arm slots executed | **fail** | 11 of 33 executed (33%) — the count-based gates presuppose that the arms ran, so they are not evaluated. A campaign that did not gather the evidence has not shown the product to be anything. |

**Verdict: RED**
