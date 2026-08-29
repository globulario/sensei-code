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
  Inside a stream, exactly two non-JSON line shapes are permitted, because
  the run itself writes them: the header `task <id>  session <id>`, and
  the CLI's terminal message `sensei-code <command>: …` (kept in
  `cli_lines`). Any other non-event line is an error.
- **Attributed by task.** A receipt beside a stream is this encounter's only
  if its `origin_task` is this encounter's task id; others are counted in
  `receipts_other_tasks` and never merged.
- **Discovered by rule.** Every `*.log` / `*.jsonl` under a directory named
  `runs`, at any depth beneath an experiment, is an encounter (the run name
  keeps its depth); `*.receipts.jsonl` is excluded; nothing outside `runs`
  is read.
- **Fresh or wrong.** `go run ./cmd/corpus -check` regenerates in memory and
  fails unless the committed `encounters.jsonl` is byte-identical;
  `TestCommittedCorpusIsFresh` does the same under `go test ./...`, which CI
  runs, so a log or overlay changed without regeneration fails CI.
- **Nothing inferred.** A field the run did not write and the overlay does
  not supply is `"unrecorded"`. `review_findings` and `review_provenance` are
  only ever overlay text. (The field was first named `human_review`, then
  corrected to "model reviews, human mediated"; both were wrong. The actors
  are distinct and are named: `reasoning_author` GPT-5.6 Sol,
  `independent_automated_reviewer` Codex, `account_principal` and
  `project_owner` davecourtois, `ledger_recorder` Claude. An account that
  executes a merge does not author the reasoning behind it.)
- **One representation model.** What a physical file IS — a stream, a piece
  of one, a run artifact, or nothing this corpus reads — is decided once, by
  `BuildEvidenceIndex` (`cmd/corpus/evidence.go`). Discovery, piece
  collection and opening consume that index; none of them parses names.
  Two laws:

  > **Partition.** Every physical name in a directory has exactly one role
  > and at most one owner. Ambiguity is an error, never precedence.
  >
  > **Certification.** A stream's bytes become evidence only when the
  > observed physical representation exactly satisfies a complete declared
  > representation. *Failure to observe an alternative representation is not
  > evidence that no alternative exists.*

  Their consequences, each with falsifiers in `cmd/corpus/`: recognition
  (one definition of a stream), resolution (a piece belongs to the longest
  prefix that is itself a valid stream), exclusivity (stream, piece or
  artifact — never two), ownership (an artifact belongs to a stream that
  exists, recognised structurally rather than by a suffix list),
  completeness (pieces declare their total and all are present), uniqueness
  (whole or split, never both), single authority (every consumer reads this
  one classification). Resolution governs artifacts as well as pieces — the
  longest matching base owns — and a genuine tie (`A.log` and `A.jsonl`
  share the base `A`, so `A.run` belongs to both) refuses both streams
  rather than choosing one.

  A stream too large to transit a tool call is committed as
  `<stream>.part-<nnn>-of-<mmm>`, same bytes, same order. **Known limit:** a
  declaration carried in filenames cannot notice a stream whose every piece
  was deleted — nothing survives to carry the claim. Closing that needs a
  declaration living outside the files it describes; pinned by
  `TestDeletingEveryPieceIsUndetectableFromNamesAlone`.

- **Appended, never rewritten.** `review_findings`, `review_provenance` and
  `merge_provenance` are the observation as first committed. A correction or
  a later event (a review that came back clean, a merge) is appended to
  `history` as an entry naming what it supersedes; nothing above it is
  edited. `review_findings` holds findings only -- a clean review is a
  `history` entry, not a finding.

## Record (exactly the fields emitted)

```text
encounter             experiment/run                                         from the log's path
source_log            path relative to the repository                        (so regeneration is root-independent)
receipts_other_tasks  receipt lines beside the stream that named another task and were not merged
cli_lines             the CLI's own terminal messages found in the stream    evidence of the exit's stated reason
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
review_findings       OVERLAY only; else "unrecorded"          findings of the independent review of the evidence/repair
review_provenance     OVERLAY only; else "unrecorded"          {reasoning_author, independent_automated_reviewer, implementation_and_reconstruction,
                                                               account_principal, project_owner, ledger_recorder, note}
merge_provenance      OVERLAY only; else "unrecorded"          {merge_executed_by, authorized_account, ultimate_authority}
history               OVERLAY only; else "unrecorded"          append-only [{recorded, event, supersedes, ...facts}]; corrections and later events, never edits
terminal              kind, summary, question, first_event, last_event, start, end, exit, world
note                  OVERLAY only
```

## Overlay

`experiments/<name>/corpus-overlay.json`, hand-written, keyed by run name.
Permitted sources: exact facts committed in that experiment's own files
(manifest header lines naming a SHA, a port, a reader commit) and the PR
reviews of the evidence, with who produced them stated. Anything reconstructed from memory is left
`"unrecorded"`.

```json
{"E3": {"sensei_sha": "f79f96f9…", "sensei_code_sha": "d6fcd11c…", "fixture": "github.com/golang/mod",
        "review_findings": ["…"], "review_provenance": {"reasoning_author": "…", "account_principal": "…", "project_owner": "…", "ledger_recorder": "…"},
        "merge_provenance": {"merge_executed_by": "…", "authorized_account": "…", "ultimate_authority": "…"},
        "history": [{"recorded": "…", "event": "…", "supersedes": "…"}], "note": "…"}}
```
