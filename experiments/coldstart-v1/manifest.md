# Cold-start A/B — frozen manifest

Everything here is fixed **before** either encounter is run.

## Fixture

```
repository  github.com/golang/sync
pinned SHA  3ffd83cb522e5ef49bd2fa50f0c0d63dc152ad1f   (2026-08-19)
files       4 non-test .go
graph       NONE — the subject of the experiment is a repository Sensei has never seen
```

External in **provenance** — not designed by the Sensei architect, no ontology
knowledge, no project-specific agent instructions. It is **not external
validation**: we are still designing and adjudicating this ourselves. §8.3's
unfamiliar maintainers and independent review remain outstanding. This fixture
gets Phase B out of the circularity trap; it does not close §8.3.

## Subject, mechanically selected

Predicate frozen beforehand: first file in stable path order expressible in the
already-frozen vocabulary. Full run in `fixture.json`.

```
NOT_EXPRESSIBLE     errgroup/errgroup.go            relations=0
SELECTED            semaphore/semaphore.go          relations=1
ELIGIBLE_NOT_FIRST  singleflight/singleflight.go    relations=1
NOT_EXPRESSIBLE     syncmap/map.go                  relations=0
```

**Subject: `semaphore/semaphore.go`** — `Weighted.mu` with candidate fields
`size`, `cur`, `waiters`.

Worth noting: `singleflight.go` was the file named in advance as obviously
qualifying. The predicate did not choose it. The subject is the one the rule
produced, not the one that would have read well.

## The two encounters

Both are legitimate, bounded maintenance requests against the pinned tree.
Neither requires the recipe to be solvable, and neither mentions locks,
invariants, coverage or Sensei. They are written from the code as it stands.

**Encounter 1** — the region is cold.

> `semaphore.Weighted` reports nothing about its current state. Add a way for a
> caller to observe how much weight is currently held and how many waiters are
> queued, without changing acquisition or release behaviour.

**Encounter 2** — same region, different task, run after Encounter 1 in both arms.

> `semaphore.Weighted.TryAcquire` and `Acquire` duplicate the decision about
> whether a request can proceed immediately. Factor that decision into one place
> so the two paths cannot diverge, with no change to observable behaviour.

## Arms

| | Control | Treatment |
|---|---|---|
| fixture, SHA, tasks, model, config | identical | identical |
| existing recipes visible | **empty view** | normal view |
| investigator runs | yes | yes |
| inference receipt written | **yes** | yes |
| proposal becomes a durable recipe | **no** | yes |
| new recipe usable in Encounter 1 | no | no *(future-only)* |
| new recipe usable in Encounter 2 | **no** | **yes** |
| `derive` semantics | identical | identical |

The control is an **empty recipes file**, not a disable switch: no production or
configuration surface is created for the experiment, and the semantic condition
is the one we care about — the proposal cannot reach Encounter 2.

Encounter 1 is equivalent in both arms with respect to the new proposal, because
the future-only rule already prevents a run from consuming its own output.

**The control still records the investigator's proposal in its receipt.**
Otherwise we would be comparing *"investigator runs normally"* against
*"investigator behaves differently"*. The withholding is recorded in the campaign
record, **not** as a fifth production outcome: the four
(`RECORDED`/`DUPLICATE`/`REFUSED`/`NO_PROPOSAL`) are contract, and V2 requires
inference history to stay intact even when output is not canonicalised.

## The measured chain

Scoring only *"did the escalation disappear?"* would let a bogus recipe count as
success. Every link is recorded separately, and a failure names its stage:

```
1  gap identified                      ... did the region actually read as cold?
2  question proposed                   ... did the investigator formulate one?
3  question admitted                   ... valid, in-region, not duplicate?
4  question executed later             ... did Encounter 2 attempt the derivation?
5  DERIVED                             ... did the relationship actually hold?
6  coverage relevant to the ORIGINAL gap ... derivationClosesGap, not merely true
7  routing changed                     ... did Encounter 2 proceed where 1 stopped?
```

A treatment run reaching 5 but not 6 has produced a **true and useless** fact —
the failure mode this project has already named as the real autonomy exploit,
and it must be reported as a failure of the loop rather than a partial success.

## Predictions, recorded before running

- **Control**: Encounter 2 reads as cold exactly as Encounter 1 did — same
  condition, same route.
- **Treatment**: link 7 changes only if links 1–6 all hold. Any other pattern is
  a negative result naming its stage.
- **Both arms**: Encounter 1 stops. The future-only rule makes this structural,
  so an Encounter 1 that proceeds in treatment is a **defect**, not a success.

## Not permitted

- adding recipe vocabulary after seeing what the investigator proposed
- hand-authoring a recipe for `semaphore/semaphore.go`
- rewording an encounter after observing a run
- re-selecting the subject

If the investigator cannot formulate a question the frozen vocabulary *can*
express, that is a failure of the loop. If no file had qualified, that would
instead have been evidence the vocabulary is too narrow — a different finding,
to be reported rather than fixed.

## Setup, resolved

Onboarding `golang/sync` immediately failed: `sensei import` writes extracted
candidates under a governed top-level key, domain closure required them to
publish, they correctly did not, and the graph was **not authoritative** — so the
start gate would have refused before any investigator ran. See
[`upstream-candidate-closure-defect.md`](../../docs/evidence/upstream-candidate-closure-defect.md).

**Fixed upstream, not worked around.** `globulario/sensei@80392aaf`. The fixture
was rebuilt from scratch with candidates in the corpus.

```
fixture      golang/sync @ 3ffd83cb522e5ef49bd2fa50f0c0d63dc152ad1f
store        isolated, 127.0.0.1:7890, built from empty
graph        127.0.0.1:10190, domain github.com/golang/sync
closure      PROVEN — 0/0 projected, 0 missing, 0 foreign
authority    authoritative
triples      1498          (sensei-code, for scale: 158,349)
failure modes 0 · source files 4
```

### What `0/0 PROVEN` means, and does not

It means **everything claiming canonical authority is accounted for**. It does
**not** mean Sensei knows this repository. Both statements are true at once and
must stay distinguishable:

| | |
|---|---|
| graph integrity / authority | **authoritative** |
| canonical architectural governance | **essentially empty** |
| architectural coverage | **cold / unknown** |
| deterministic extracted observations | 1,498 triples |

Nothing may read the absence of missing identities as safety. Law 1 —
*absence is not safety* — and the regression test
`TestColdStartProvesClosureWithoutClaimingKnowledge` pins it.

This is the state the investigator is meant to operate on: the floor plan
generated from source structure, and none of the handwritten signs saying which
door is load-bearing.
