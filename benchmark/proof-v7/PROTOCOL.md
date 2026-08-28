# proof-v7 — UNFROZEN DRAFT

Status: **UNFROZEN**. This file freezes when the product and graph identity
below are filled in from the post-#111 `main` and the commit is labelled
`freeze`. Until then nothing here binds, no task is labelled, no arm runs.

Snapshot (filled at freeze):
- product: `main` @ `<sha>` (contains #108, #109, #111)
- graph: domain `github.com/globulario/sensei-code`, live digest `<sha256>`,
  published slice exposes FUTURE_ONLY and REPEATED_RESUME_CANNOT_MINT with
  their exact required tests (verified by `sensei preflight`, output kept)
- harness: `cmd/proofbench` @ the same `<sha>`

## Two hypotheses, scored separately

**H1 — boundary-omission interception.** On preregistered unseen tasks whose
statements contain negative conditions, ownership/authority boundaries,
exceptions, or multi-state completeness requirements, locally plausible
incomplete solutions disproportionately produce a mechanically identifiable
coverage/authority contradiction at routing or inspection, before reviewer
judgment.

Control prediction: on LOCAL_POSITIVE tasks with no such boundary, COLD has
little or no systematic correctness advantage over RAW.

**H2 — contradiction persistence.** For identical candidate bytes and
materially identical governed evidence, an unresolved non-accepting review
finding survives reviewer substitution. Reviewer identity alone cannot turn a
blocked candidate into an accepted one.

H1 asks whether Sensei catches code incompleteness mechanically. H2 asks
whether it preserves decision consistency once a problem is found. A result
on one says nothing about the other.

## Labels — from task text only, before any outcome exists

Closed vocabulary (`proofbench.TaskFlags`, read by membership):
`LOCAL_POSITIVE`, `MULTI_STATE`, `OWNERSHIP_BOUNDARY`, `NEGATIVE_CONDITION`,
`EXCEPTION_CASE`. A task is labelled from its `statement` alone by the
labelling rule below, the labels are committed in `manifest.json`, and the
manifest hash is recorded before the first arm runs. A label changed after a
run is a result, not a label, and voids the task.

Labelling rule: a flag is assigned iff the statement text itself states the
condition — "must not", "refuse", "never" → NEGATIVE_CONDITION; names an
owner, authority, caller/callee boundary or who-may-write → OWNERSHIP_BOUNDARY;
"except", "unless", "only when" → EXCEPTION_CASE; requires the same property
across two or more named states/branches/paths → MULTI_STATE; none of the
above → LOCAL_POSITIVE. Statements are not rewritten to fit a flag.

## Scoring

Per encounter (recorded on `proofbench.Attempt`, absent = not observed):
`flags`, `route`, `gap_identity`, `contradiction_kind`,
`contradiction_stage`, `refusal_cause`, `closure_attempted`,
`closure_succeeded`, `review_attempts`, `candidate_digest`,
`evidence_identity`, `oracle_verdict`.

H1 score: among INCORRECT-by-oracle COLD candidates, the fraction whose
`contradiction_stage` ∈ {route, inspection} (before review), split by
flagged vs LOCAL_POSITIVE. Prediction: flagged ≫ LOCAL_POSITIVE. Control:
RAW vs COLD oracle correctness on LOCAL_POSITIVE, prediction ≈ equal.

H2 score: every encounter with ≥2 review attempts on the same
`candidate_digest` and `evidence_identity`. Prediction: zero transitions
non-accept → accept without a `review.contradiction` event and an
`architect.reconciliation` naming both reviewers. One silent transition
falsifies H2 on this product.

## Exclusions (discovery instances, never confirmation)

- The three RAW failures that generated H1.
- B3 N1b and the #109 normalization edge (sharpened H1).
- B3 N2b (created H2).
- Any task whose labels were assigned after its first run.

## Not in v7

- The `no governing invariant` decision-record message: semantic/audit
  vocabulary debt (`tc.Invariants` is architect-plan provenance; recipe-derived
  authority is a different vocabulary). Recorded, not repaired, not measured.
- N3 (prospective.go negative control): held; decided separately after the
  freeze.
- Any growth of proofbench beyond the fields above.
