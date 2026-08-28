# Phase B evidence corpus — schema

Phase B's runs, frozen as records, one per encounter (one governed
invocation of `sensei-code run`). Produced by `go run ./cmd/corpus`, which
reads only what the run itself wrote — the event stream, receipts, recipes,
the `.run` stamp — and an optional per-experiment overlay for what no event
carries. The ugly runs are in it on purpose: a void arm, a future-only exit,
a structural stop, a refutation, and a review-discovered hole are evidence
of the same standing as an accepted candidate. Nothing is scored here; this
is what Phase C will rank.

## Record

```text
encounter            experiment/run                          mechanical   the log file's identity
instrument           sensei_sha, sensei_code_sha, fixture,
                     world (base)                            overlay + event   base from `candidate … from base`; SHAs from the experiment's overlay
task                 id, text, provenance (mode.selected)    mechanical
question_origin      per closure round: recipe identity,
                     outcome (RECORDED / …), gap, round      receipts     E*.receipts.jsonl beside the log
recipes              at start (recipes-after minus this run's
                     receipts) and after                     files        E*.recipes-after.json beside the log
derivation           (in the receipts' closure outcome; the
                     per-recipe DERIVED/REFUTED/UNRESOLVED is
                     carried by the coverage anchors)         mechanical
coverage             per routing round: anchors, planned files,
                     anchor descriptions, operational authority mechanical `derived coverage:` status payload
gap_identity         per closure round: receipt id, gap_identity mechanical  closure status payload
route                per routing round                        mechanical
authority_to_worker  prospective grants, test-edit grants     mechanical   prospective.granted / testedit.granted payloads
candidate            per cycle: digest, bytes                 mechanical   candidate.changed
validation           per run: checks with outcome            mechanical   validation.run payload
audit                per run: decision, digest, findings      mechanical   candidate.audited payload
review               per run: provider, decision, summary,
                     findings                                 mechanical   review.completed payload
human_review         findings from the PR review of the
                     evidence / repair                        overlay      never inferred; absent = "unrecorded"
terminal             kind, summary, exit code, timestamps     mechanical   workflow.* + the .run stamp
```

## Overlay

`experiments/<name>/corpus-overlay.json` — optional, hand-written, one
object keyed by run name:

```json
{"E3": {"sensei_sha": "f79f96f9…", "sensei_code_sha": "d6fcd11c…", "fixture": "github.com/golang/mod",
        "human_review": ["…"], "note": "…"}}
```

A field the overlay does not supply is emitted as `"unrecorded"`, never
guessed from the manifest prose.

## Invariants of the corpus

- Every record names its source log; a record that cannot be regenerated
  from the log and the overlay is a claim, not a record.
- Trimmed logs are the source (worker `output` events are dropped from the
  repository); the untrimmed sha256 lives in the experiment's `.run` files.
- `human_review` is only ever overlay text; the extractor never derives it.
- Records are appended, never rewritten: a correction is a new record with
  `supersedes` naming the old one.
