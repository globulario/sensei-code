# Proof campaign report — proof-v4

**Verdict: RED**

Manifest `sha256:2e4f11645fc212027739a02adc7da8c40c00553833df7ee5c4581fe0d78c6ca3`. 2 primary task(s).

**Coverage: 4 of 6 designed arm slots were executed.** 2 were never run, and are listed below rather than omitted -- a report showing only the arms that happened to run would describe a smaller, better-behaved campaign than the one that was designed.

This is an engineering evidence campaign over 2 tasks, not a population estimate. Intervals are wide by construction. Where the data supports only "promising" or "inconclusive", it does not support "proven".

## Calibration — can the instrument record a known win and a known failure?

None recorded. The instrument is unvalidated.


## Per-task outcome matrix

| task | linked | RAW | COLD | WARM |
|---|---|---|---|---|
| internal-gitx-a4fa351 |  | CORRECT | INCORRECT | NOT_EXECUTED |
| internal-gitx-6460efd | yes | CORRECT | INCORRECT | NOT_EXECUTED |

## The two rates

Correctness and delivery are separate axes and are never collapsed. A run that did not reach an evaluable candidate is NOT_EVALUATED for correctness -- it cannot be called wrong for code it never wrote -- and counts as a failure for end-to-end success, because a task the product could not deliver on is a product failure whatever the code would have been.

Operational budget: **22m per arm, frozen**. The system cannot improve its score by taking longer.

| arm | engineering correctness | end-to-end success | NOT_EVALUATED | terminals |
|---|---|---|---|---|
| RAW | 2/2 = 100% [34–100%] | 2/2 = 100% [34–100%] | 0 | COMPLETED 2 |
| COLD | n/a (no eligible runs) | 0/2 = 0% [0–66%] | 2 | INFRA_FAILURE 1, TIMEOUT 1 |
| WARM | n/a (no eligible runs) | 0/2 = 0% [0–66%] | 0 | none run |

*Engineering correctness is CORRECT / (CORRECT + INCORRECT): when the system produced an evaluable solution, was it right? End-to-end success is delivered / all 2 scheduled arms per condition: could it be given a task and return a correct result inside the budget?*

### Every arm, both axes

| attempt | terminal | correctness | delivered | wall | cause |
|---|---|---|---|---|---|
| internal-gitx-a4fa351/RAW/1 | COMPLETED | CORRECT | yes | 272s |  |
| internal-gitx-6460efd/RAW/1 | COMPLETED | CORRECT | yes | 199s |  |
| internal-gitx-a4fa351/COLD/1 | INFRA_FAILURE | NOT_EVALUATED |  | 137s | infrastructure: backend is unreachable [recorded INCORRECT — the record stands as written; the run did not reach a point where its code could be judged] |
| internal-gitx-6460efd/COLD/1 | TIMEOUT | NOT_EVALUATED |  | 1320s | the 22m operational budget ran out before an evaluable candidate existed [recorded INCORRECT — the record stands as written; the run did not reach a point where its code could be judged] |

## Primary metrics

| metric | RAW | COLD | WARM |
|---|---|---|---|
| runs recorded | 2 | 2 | 0 |
| NO_RESULT | 0 | 0 | 0 |
| correct closure | 2/2 = 100% [34–100%] | 0/2 = 0% [0–66%] | n/a (no eligible runs) |
| autonomous-correct | 1/2 = 50% [9–91%] | 0/2 = 0% [0–66%] | n/a (no eligible runs) |
| human technical intervention | 0/2 = 0% [0–66%] | 0/2 = 0% [0–66%] | n/a (no eligible runs) |
| false grant | 0/2 = 0% [0–66%] | 0/2 = 0% [0–66%] | n/a (no eligible runs) |
| false block (correct, stopped) | 2/2 = 100% [34–100%] | 0/2 = 0% [0–66%] | n/a (no eligible runs) |
| non-convergent | 0/2 = 0% [0–66%] | 0/2 = 0% [0–66%] | n/a (no eligible runs) |
| verification failure | 0/2 = 0% [0–66%] | 2/2 = 100% [34–100%] | n/a (no eligible runs) |
| closure yield | n/a (no eligible runs) | n/a (no eligible runs) | n/a (no eligible runs) |
| durable knowledge reuse | 0/2 = 0% [0–66%] | 0/2 = 0% [0–66%] | n/a (no eligible runs) |
| review cycles | median 0.0 (range 0.0–0.0, n=2) | median 1.0 (range 0.0–2.0, n=2) | n/a |
| wall time | median 3.9 min (range 3.3–4.5, n=2) | median 12.1 min (range 2.3–22.0, n=2) | n/a |
| provider cost | unknown (2 observation(s) with no data) | unknown (2 observation(s) with no data) | n/a |
| rediscovery | median 0.0 (range 0.0–0.0, n=2) | median 0.0 (range 0.0–0.0, n=2) | n/a |

## Compounding — COLD vs WARM on linked specimens

| task | comparable | COLD | WARM | rediscovery | human tech | cycles | improved |
|---|---|---|---|---|---|---|---|
| internal-gitx-6460efd | no — one arm has no recorded attempt |  |  | 0 → 0 | 0 → 0 | 0 → 0 | no |

## Every human technical intervention

None recorded.

## False grants and false blocks

- arm slots never executed (2): internal-gitx-a4fa351/WARM, internal-gitx-6460efd/WARM
- provider-configuration mismatch (0): none
- false grants: none
- false blocks: internal-gitx-6460efd/RAW/1, internal-gitx-a4fa351/RAW/1
- NO_RESULT attempts: none
- boundary unmeasurable (governed checkout not quiescent at arm start): none

## Pre-registered gates

Thresholds were frozen in `docs/work/proof-before-mechanism.md` before any result existed and are transcribed as constants in `gates.go`. This verdict is a function of the committed run records.

| gate | kind | claim | result | detail |
|---|---|---|---|---|
| G1 | GREEN | at least 9/2 tasks CORRECT under governed execution | **fail** | 0/2 |
| G2 | GREEN | at least 8/2 autonomous-correct | **fail** | 0/2 |
| G3 | GREEN | zero false grants and zero governed-checkout boundary violations | **fail** | 0 false grant(s), 2 boundary violation(s) |
| G4 | GREEN | no unclassified runaway review loop | pass | 0 |
| G5 | GREEN | governed correct closure trails RAW by at most 1 task(s) | **fail** | governed 0 vs RAW 2 |
| G6 | GREEN | WARM improves at least 3/4 linked specimens with no correctness regression | **fail** | 0/1 improved, regression=false |
| G7 | GREEN | aggregate WARM rediscovery at least 30% below COLD | **not testable** | COLD rediscovery denominator is zero; the compounding claim is not testable on this corpus rather than passing |
| G8 | GREEN | median governed cost at most 3x RAW, or premium independently justified | **fail** | no provider cost data; ratio unknown (never reported as zero) |
| R1 | RED | at least 6/2 governed primary tasks correct | **fail** | 0/2 correct under COLD or WARM |
| R2 | RED | no false grant let an independently incorrect candidate through | pass | 0 false grant(s) |
| R3 | RED | no repeated unclassified review runaway after the #62 calibration | pass | 0 unclassified runaway loop(s) |
| R4 | RED | WARM shows structured reuse benefit on at least one linked specimen | **fail** | 0/1 linked specimens improved |
| R5 | RED | median governed cost within 3x RAW, or the premium is justified | pass | no provider cost data; ratio unknown (never reported as zero) |
| R6 | RED | the harness reproduces the known #79/#80 positive and #62 negative shapes | **fail** | positive=false negative=false |

**Verdict: RED**
