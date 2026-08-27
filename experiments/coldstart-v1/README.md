# Cold-start experiment — selection ran, and there is no valid subject here

Recorded **before any encounter was run**. The selection stage produced a
result, and the result is that this repository cannot host the experiment.

## The predicate, as frozen

> Select the first genuinely uncovered package outside the 11-task corpus, in
> lexicographic order, for which static inspection shows at least one
> mechanically evaluable relation from the *already frozen* vocabulary —
> `field_access_under_lock` or `command_invocation_confined_to`.

It ran. It selected **`cmd/proofbench`**, with `internal/proofbench` as the only
other eligible package.

## Two defects in the predicate, both about setup and neither about a result

**1. Granularity.** The refusal under study triggers per **file** — *"only 1 of 2
requested file(s) are examined in the graph"*. The predicate selects per
**package**. The observed case, `internal/setup/checks.go`, is an unknown file
inside a *covered* package, which a package-level predicate can never select.

```
internal/workflow   2/14 files unknown   (package covered)
internal/derived    2/3
internal/sensei     1/3
cmd/sensei-code     1/10
```

**2. The instrument.** Both eligible packages were first committed 2026-08-24 —
by this campaign, for this campaign. They are uncovered because they are **new**,
not because they are an un-indexed part of the architecture. Investigating them
measures the measuring instrument.

Neither correction was chosen for the subject it would produce. Both refuse a
subject that cannot reproduce the condition under study.

## The corrected selection

File granularity; outside the corpus; not the instrument; not authored during
this campaign; expressible in the frozen vocabulary.

```
uncovered files outside the corpus : 26
  the measuring instrument          : 17
  authored during this campaign     :  9
VIABLE SUBJECTS                     :  0
```

Every uncovered file in this repository is the benchmark corpus, the benchmark
harness, or code written in the last four days.

The coverage proxy was verified against the live graph before concluding: files
it calls covered are genuinely known to the graph, so it is not hiding
candidates.

## Why this is not a disappointment

**sensei-code is too well-governed to host a cold-start experiment.** 71 of 97
non-test files are known to the graph, and the unknown remainder is new code.
Cold start is by definition about a repository the graph does not know, and
this one it does.

Running the experiment here would require either:

- **using corpus packages** — training the mechanism on the tasks being
  measured, which was explicitly forbidden; or
- **using campaign-authored code** — measuring the instrument with itself, the
  circularity §8.3 exists to prevent.

## Consequence: the order changes

The planned order was **receipts → cold-start → promote evidence integrity →
external fixture**. Step 2 has no valid subject, so:

> **receipts (done) → external fixture → cold-start ON that fixture → promote
> evidence integrity**

§8.3 moves from *"required before claiming validation"* to *"required before the
next experiment can be run at all"*. That is a stronger statement than the
document makes, and it is forced by measurement rather than chosen.

The `promote` evidence-integrity work (§5.1) is unaffected and can proceed in
parallel — it is a substrate defect demonstrable on this repository, needing no
uncovered subject.

## Fixture requirements, from §8.3 and from this selection

- not designed by the Sensei architect; maintainers unfamiliar with the ontology
- real architecture, non-trivial tests, genuine history
- large enough to contain hidden assumptions, small enough for independent
  adjudication
- **at least one file expressible in the frozen recipe vocabulary** — or the
  experiment measures the vocabulary's narrowness, which is a different finding
  and must be reported as such rather than fixed by adding vocabulary

Deterministic Sensei runs first to establish the §8.1 baseline: what v1 knows
without the investigator. Only then is the investigator introduced.

## Artifacts

- `selection.py` — the predicate and its correction, both executable
- `selection.json` — the package-granularity run, every candidate with a verdict
- `selection_corrected.json` — the file-granularity run, all 26 with reasons
