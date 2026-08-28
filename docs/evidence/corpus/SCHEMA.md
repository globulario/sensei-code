# Phase B evidence corpus — schema

One record per encounter (one governed invocation of `sensei-code run`),
produced by `go run ./cmd/corpus` from what the run itself wrote — the event
stream, `.receipts.jsonl`, `.recipes-after.json`, the `.run` stamp — and an
optional per-experiment overlay for what no event carries. The ugly runs are
in on purpose: a void arm, a future-only exit, a structural stop, a
refutation, an operator error, and a review-discovered hole are evidence of
the same standing as an accepted candidate. Nothing is scored here; this is
what Phase C will rank.

## Contract

- **Fail closed.** A log that cannot be read, parsed, or encoded is an
  error, never a skipped record (`generate` returns it; the command exits 1).
- **Fresh or wrong.** `go run ./cmd/corpus -check` regenerates in memory and
  fails unless the committed `encounters.jsonl` is byte-identical;
  `TestCommittedCorpusIsFresh` does the same under `go test ./...`, which CI
  runs, so a log or overlay changed without regeneration fails CI.
- **Nothing inferred.** A field the run did not write and the overlay does
  not supply is `"unrecorded"`. `human_review` is only ever overlay text.
- **Appended, never rewritten.** A correction is a new record naming the
  old one in `supersedes`.

## Record (exactly the fields emitted)

```text
encounter             experiment/run                                         from the log's path
source_log            path relative to the repository                        (so regeneration is root-independent)
instrument            sensei_sha, sensei_code_sha, fixture                   OVERLAY; world from `candidate … from base` or the .run stamp
graph                 domain, build, address                                 the `graph binding` status line
                      audit_graph_commit                                     candidate.audited payload
                      input_graph_digest                                     the closure receipt (sha256 of the graph the investigator saw)
                      authority                                              the workspace-status result: live store digest, triple count,
                                                                             freshness, seed state, provenance stamp
                      -- two encounters at one source SHA against two graph worlds are distinct here
task                  id, text, provenance (mode.selected), plan {files, mode, plan_source, plan_digest,
                      prospective_surfaces, claim_sources}                   task.created / mode.selected / plan.proposed
question_origin       per closure round: round, outcome, identity, gap, region   .receipts.jsonl
recipes_at_start      recipes_after minus the identities this run's receipts RECORDED   derived, by identity
recipes_after         kind, dir, type, field, command, owner, search_paths   .recipes-after.json
derivation            per routing round: file, requirement, outcome         the anchor lines of the coverage status
                      (only DERIVED anchors reach routing; REFUTED/UNRESOLVED
                      are in the receipts' outcomes and in the manifests)
coverage              per routing round: anchors, planned_files, route, anchor_lines, operational_authority
gap_identity          per closure round: receipt, identity, condition       the closure status payload
route                 per routing round
authority_to_worker   prospective (ProspectiveGranted payload), test_edit (TestEditGranted payload)
candidate             per cycle: time, bytes, cycle, digest                 candidate.changed + the validation that bound the digest
validation            per run: diff_digest, checks                          validation.run payload
audit                 per run: decision, digest, findings, reason_codes     candidate.audited payload
review                per run: provider, candidate_digest, decision, summary, findings   review.completed
human_review          OVERLAY only; else "unrecorded"
terminal              kind, summary, question, first_event, last_event, start, end, exit, world
note                  OVERLAY only
```

## Overlay

`experiments/<name>/corpus-overlay.json`, hand-written, keyed by run name.
Permitted sources: exact facts committed in that experiment's own files
(manifest header lines naming a SHA, a port, a reader commit) and the PR
reviews of the evidence. Anything reconstructed from memory is left
`"unrecorded"`.

```json
{"E3": {"sensei_sha": "f79f96f9…", "sensei_code_sha": "d6fcd11c…", "fixture": "github.com/golang/mod",
        "human_review": ["…"], "note": "…"}}
```
