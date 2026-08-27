# FINAL VERDICT — proof-v6

Frozen corpus of **11 real repository tasks with independently observable
behavioural contracts**. 22-minute budget, scoring contract and oracle hashes
frozen before execution. No Sensei-code behaviour was modified at any point to
improve any number.

---

## The measurement

| | RAW | COLD |
|---|---|---|
| **engineering correctness** | 8/11 = 73% | **4/4 = 100%** |
| **end-to-end success** | **8/11 = 73%** | 4/11 = 36% |
| NOT_EVALUATED | 0 | 7 |
| terminals | COMPLETED 11 | COMPLETED 4 · REFUSED 5 · TIMEOUT 1 · INFRA 1 |
| wall, median | 264s | 365s (**1.4×**) |
| wall, total | 51 min | 108 min |
| review cycles | — | max 3, median 0 |
| human technical interventions | — | **0** |
| false grants | — | **0** |
| false blocks | — | **0** |
| provider cost | unknown | unknown |

### Transition matrix

```
CORRECT   -> CORRECT          4     preserved capability
CORRECT   -> NOT_EVALUATED    4     delivery loss
INCORRECT -> NOT_EVALUATED    3     no correctness observation
INCORRECT -> CORRECT          0     governance gain
CORRECT   -> INCORRECT        0     governance regression
INCORRECT -> INCORRECT        0
```

### WARM case study

`NO_RESULT` — two consecutive provider usage limits, both attempts preserved
under the frozen retry rule.

A construct limitation established **before** the arm ran, and independent of
its failure: K already exists at the later task's pinned base, and the awareness
graph is a live service rather than one pinned per task. COLD therefore had K on
both counts, and nothing in this harness gives WARM knowledge COLD lacks. Even a
completed arm could not have isolated compounding.

**Compounding remains NOT TESTED**, as frozen.

---

## Verdict: **interpretation C**

> COLD delivers few candidates, but delivered candidates are usually correct.
> Execution and control-plane debt — not evidence that the reasoning or
> governance model is wrong.

**Explicitly not D.** D requires governance to produce fewer *correct* candidates
among those it delivers, which would appear as `CORRECT → INCORRECT`. There are
none. Nothing RAW got right was broken by governance.

**Explicitly not E.** No `INCORRECT → CORRECT` transition was observed either.
Governance neither gained nor lost correctness on this corpus.

### The narrow claim, stated exactly

On eleven real repository tasks with independently observable behavioural
contracts, governed execution delivered a candidate for 4 of 11, and **every
delivered candidate was correct** — with zero false grants, zero false blocks,
and zero human technical answers — at 1.4× the median wall time of an ungoverned
baseline that delivered 8 of 11 and was correct on 8.

### The honest limit

**n = 4 delivered arms.** 4/4 carries a 95% Wilson interval of **[51%, 100%]**.
That supports *"no observed correctness cost"*. It does **not** support
*"governance improves correctness"*.

### What is not claimed

- Nothing about the higher-risk architectural work Sensei was primarily built to
  protect. Criterion (6) selected this corpus for **measurability**, not for that
  class of change.
- Nothing about compounding.
- Not that the refusals were wrong — see the diagnostic, which finds three of
  five justified under Sensei's own doctrine.

---

## Delivery failure classes, ranked

```
1.  REFUSAL          5/11    two distinct causes, two distinct repairs
2.  TIMEOUT          1/11    single observation
3.  INFRASTRUCTURE   1/11    single observation, externally attributable
```

Full analysis in `proof-v6-refusal-diagnostic.md`. In summary, and **diagnostic
rather than scoring**:

- **3 JUSTIFIED_REFUSAL** — unclosed knowledge gaps. A closure round ran and
  failed on all three, because the mechanism that could close a coverage gap
  autonomously (`Engine.derivedCoverage` → `relevance.go`) **exists and is not
  connected to `routePlan`**. Pinned by `TestTheGovernedRunDoesNotYetSupplyDerivedCoverage`
  before any of these runs.
- **2 UNNECESSARY_REFUSAL** — a security approval gate fired on two *scrolling*
  changes, because `sensei_code.provider.credentials_remain_provider_owned`
  anchors `internal/tui/model.go`, whose only credential content is a help
  string. The router was right to honour the gate; the anchor is over-broad.

**Rule Zero holds.** Both repairs are wiring and data. Neither is a new
governance mechanism, and neither weakens the safety standard.

---

## Campaign integrity

- Corpus, statements, oracles, oracle hashes, thresholds, budget, scoring and
  schedule unchanged throughout.
- Graph authority pinned before the wave and re-checked before every arm; it did
  not move.
- RAW carried unchanged from proof-v5 — RAW works in place, so the evaluator
  defect never touched it.
- Preserved and excluded from the result: the proof-v5 COLD wave
  (`VOID_MEASUREMENT`), the single pre-restoration v6 arm (`PRE_RESTORATION`),
  and both WARM attempts.

### What the campaign cost to make trustworthy

Twelve instrument defects, every one found by running the harness rather than
reading it. The two that would have done real damage:

- **The evaluator judged the wrong directory.** Eleven governed arms recorded the
  empty-tree hash and were scored on code that was never there. One recorded
  `INCORRECT` candidate passes the frozen oracle when the oracle is pointed at
  its real tree. Read at face value that wave was interpretation D — the most
  serious possible finding — and it was an artefact.
- **The oracles required the reference patch's identifiers.** Under those, every
  arm scored `INCORRECT` and the benchmark could not say why.

Had either shipped, this report would have condemned the product on evidence
about the instrument.
