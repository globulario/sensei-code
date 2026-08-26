# RAW vs COLD — the first valid measurement

Frozen corpus `proof-v6`, 11 tasks with behavioural contract oracles, 22-minute
budget, scoring contract unchanged. RAW carried from proof-v5; COLD run in full
against a pinned authoritative graph.

## The paired result

| task | RAW | RAW s | COLD terminal | COLD | COLD s |
|---|---|---|---|---|---|
| `internal-agent-01f3fe1` | CORRECT | 107 | COMPLETED | **CORRECT** | 1258 |
| `internal-assist-5dfffad` | CORRECT | 110 | COMPLETED | **CORRECT** | 322 |
| `internal-gitx-6460efd` | CORRECT | 220 | COMPLETED | **CORRECT** | 690 |
| `internal-gitx-a4fa351` | CORRECT | 284 | COMPLETED | **CORRECT** | 1313 |
| `internal-architect-2e095c4` | INCORRECT | 472 | REFUSED | NOT_EVALUATED | 378 |
| `internal-setup-16ecbc3` | INCORRECT | 314 | REFUSED | NOT_EVALUATED | 332 |
| `internal-tui-be512db` | INCORRECT | 654 | REFUSED | NOT_EVALUATED | 171 |
| `internal-setup-e645669` | CORRECT | 264 | REFUSED | NOT_EVALUATED | 365 |
| `internal-tui-ea046ba` | CORRECT | 370 | REFUSED | NOT_EVALUATED | 140 |
| `internal-decision-6cf23e8` | CORRECT | 156 | TIMEOUT | NOT_EVALUATED | 1320 |
| `internal-session-4d32937` | CORRECT | 111 | INFRA_FAILURE | NOT_EVALUATED | 161 |

## Aggregate

| | RAW | COLD |
|---|---|---|
| engineering correctness | 8/11 = 73% | **4/4 = 100%** |
| end-to-end success | **8/11 = 73%** | 4/11 = 36% |
| NOT_EVALUATED | 0 | 7 |
| terminals | COMPLETED 11 | COMPLETED 4, REFUSED 5, TIMEOUT 1, INFRA 1 |
| wall, median | 264s | 365s (1.4x) |
| wall, total | 51 min | 108 min |
| review cycles | — | max 3, median 0 |
| human technical interventions | — | **0** |
| false grants | — | **0** |
| false blocks | — | **0** |
| provider cost | unknown | unknown |

## Transition matrix

```
CORRECT   -> CORRECT          4     preserved capability
CORRECT   -> NOT_EVALUATED    4     delivery loss
INCORRECT -> NOT_EVALUATED    3     no correctness observation
INCORRECT -> CORRECT          0     governance gain
CORRECT   -> INCORRECT        0     governance regression
INCORRECT -> INCORRECT        0
```

**Zero regressions and zero gains.** Every arm that delivered was correct;
nothing that RAW got right was broken by governance, and nothing RAW got wrong
was fixed, because none of those three tasks reached an evaluable candidate.

## Interpretation: C

> **COLD delivers few candidates, but delivered candidates are usually correct.
> Treat this as execution/control-plane debt, not evidence that the
> reasoning/governance model is wrong.**

The evidence fits C precisely and fits nothing else:

- **4/4 correctness when it delivers.** Not one delivered candidate was wrong.
  RAW, given the same tasks and judged by the same oracles, was wrong 3 times in
  11. On the four tasks both arms completed, both were correct.
- **4/11 delivery.** Seven arms never reached an evaluable candidate.
- **0 false grants.** Nothing incorrect passed the claimed safety boundary.
- **0 human technical interventions.** Nobody was told the answer.

It is explicitly **not D**: D requires governance to produce *fewer correct
candidates* among those it delivers, which would show as `CORRECT -> INCORRECT`
transitions. There are none.

The honest limit: **n=4 delivered arms.** 4/4 with a 95% Wilson interval of
[51%, 100%] is compatible with a wide range of true rates. It supports "no
observed correctness cost" and does not support "governance improves
correctness".

## Where the delivery cost is, ranked from the run evidence

```
5   REFUSED         governance stopped and asked a human
1   TIMEOUT         22-minute budget exhausted
1   INFRA_FAILURE   an externally attributable failure
```

**Refusal is the dominant cost, by a wide margin.** Five of eleven arms stopped
to ask a human on tasks a plain model completed unattended in 2-11 minutes.
That is the single largest observed delivery loss, and it is where effort should
go.

Worth separating carefully: a refusal is *the product working as designed*.
Three of the five refusals were on tasks RAW got **wrong** — `architect`,
`setup-16ecbc3`, `tui-be512db` — so on those the refusal may well have been the
better outcome than RAW's confident wrong answer. This benchmark cannot tell:
it did not measure whether a refusal was warranted, only that it was not
delivery.

The other two refusals were on tasks RAW got **right**, which is where the
delivery cost is real and unambiguous.

## What this campaign does and does not claim

**Claimed:** on a frozen corpus of eleven real repository tasks with
independently observable behavioural contracts, governed execution delivered a
candidate for 4 of 11 and every delivered candidate was correct, with no false
grants and no human technical answers, at 1.4x the median wall time of an
ungoverned baseline that delivered 8 of 11 and was correct on 8.

**Not claimed:** that governance improves correctness (n=4, no gains observed);
that governance harms correctness (no regressions observed); that refusals were
wrong (not measured); anything about the higher-risk architectural work Sensei
was primarily built to protect, which this corpus deliberately excludes.

## Campaign integrity

- Corpus, task statements, oracles, oracle hashes, thresholds, budget, scoring
  and schedule unchanged throughout.
- No Sensei-code behaviour was modified to improve any number.
- The graph authority was pinned before the wave and re-checked before every
  arm; it did not move.
- RAW 8/11 carried unchanged; RAW works in place, so the evaluator defect never
  touched it.
- The proof-v5 COLD wave is preserved as `VOID_MEASUREMENT`, and the single
  pre-restoration v6 arm as `PRE_RESTORATION`. Neither enters this result.
