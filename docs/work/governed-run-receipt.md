# The governed-run receipt — slice 1: the type and a total reader

This is **not C5b**. C5 remains VOID at `d0c8c20`, untouched. This is product
architecture extracted from what C5 taught, and it grants nothing.

## Why it exists

A governed run already knows who governed it, what it was based on, what
candidate it produced, who reviewed it and how it ended. Until now an external
witness reconstructed all of that afterwards from a general-purpose event
stream. That reconstruction was 673 lines of laboratory apparatus, and it
destroyed an expensive run when a 62-line ad-hoc parser met one event whose
nested field was a string where its author had assumed an object — a shape that
already existed in the previous run's preserved log, unexamined.

> **Never fabricate a specimen when a measured specimen already exists.**

## What slice 1 contains

`internal/runreceipt`:

- **`Completeness`** (`COMPLETE` / `INCOMPLETE`) and **`Outcome`**
  (`ACCEPTED` / `REFUSED` / `FAILED` / `UNREVIEWED` / `UNKNOWN`) as separate
  types. `VOID` is not an outcome — it is what a reader concludes about an
  incomplete receipt, and this package cannot emit it.
- **`Value.Source`** on every fact: a `KNOWN` value with no stated source is
  invalid, wherever it appears. `MeasuredValue(text, source)` refuses to build
  one and `Completeness()` rejects a hand-written one. This does not make lying
  impossible; it removes *casual* promotion of a string to knowledge, which is
  the real risk. For re-derivable fields the source is not proof anyway —
  `Verify` recomputes those.
- **`Outcome` membership is read by enumeration**, never by exclusion.
  `Valid()` accepts only the five defined values, so `"VOID"` — or any other
  string — cannot survive validation, and `UNKNOWN` is representable but never
  `SufficientForComplete()`. Omitting a constant is not preventing it.
- **`Knownness`** (`KNOWN` / `UNKNOWN` / `MALFORMED` / `UNSUPPORTED`) on every
  fact, so no input shape can crash extraction. The C5 defect is a `MALFORMED`
  classification here, not a panic. An empty string is `UNKNOWN`, not a
  measurement: "label present, value empty" has no hiding place.
- **`Derivation`** (`RE_DERIVABLE` / `OBSERVED`) on every field. `Fields()` and
  `Rederivable()` let a verifier iterate without knowing the struct, and
  `Verify` recomputes each re-derivable fact — reporting a field it *could not*
  recompute, because silence from a verifier is not agreement.
- **`MismatchKind`** (`DISAGREEMENT` / `UNRECOMPUTABLE` / `RECORDED_UNKNOWN` /
  `RECORDED_MALFORMED`) so consumers switch on epistemic state rather than
  parse English prose.
- **`internal/runreceipt/legacy.FromEvents`** builds a receipt from a run's
  JSONL stream, in its own package. The governed loop imports the core and
  never the adapter: a package boundary is a stronger statement than a comment,
  and documentation did not stop a parser from being tested against invented
  specimens. The adapter also **never infers** — an earlier draft set
  `GovernorCommit` from the same field as `BaseCommit`, one measured fact
  becoming two claims, and `G == B` is true of C5 by construction rather than
  by architecture. It now reports `UNKNOWN` with that reason. This is the
  migration path, not the destination: the governor holds these facts when it
  acts and should emit them. Building from the stream lets the reader be tested
  **today against the real logs C4 and C5 left behind**.

## What it deliberately does not contain

- **No admission.** The receipt reports evidence; it may not grant authority to
  itself. `TestAReceiptCannotAdmitAnything` holds that boundary mechanically.
- **No isolation.** Start and end isolation stay external and independent. A
  governed process cannot be the sole witness that its own environment was
  isolated.
- **No engine wiring yet.** First prove the schema faithfully represents a run.

## The tests are the specification

```text
pinned baseline C4.log+C5.log  -> present AND sha256-unchanged, or FAIL; pure
                                  discovery could lose one while a newer log
                                  kept the count up
discovered experiments/*/runs  -> every other preserved log joins automatically
adapter-built receipt          -> can never be COMPLETE: the historical stream
                                  cannot supply governor identity, binary
                                  digest or serving producer
sourceless KNOWN               -> INCOMPLETE
outcome "VOID" / any unknown   -> INCOMPLETE, invalid membership
outcome UNKNOWN                -> representable, never COMPLETE
payload.provenance as a string -> MALFORMED, and the well-formed verdict beside
                                  it is still measured
every JSON type in every       -> KNOWN / UNKNOWN / MALFORMED / UNSUPPORTED,
nested position                   never a panic
missing required field         -> INCOMPLETE, named, never defaulted
complete + UNREVIEWED          -> COMPLETE / UNREVIEWED, not VOID
complete + failure             -> COMPLETE / FAILED
re-derivable field             -> recomputed; mismatch and unmeasurability both
                                  visible
unmodelled kind                -> reported in diagnostics, never dropped
reviewer fallback              -> the failed attempt kept beside the delivering
                                  one
```

## One discipline note

Running the reader over `C5.log` derives a run outcome as a side effect. C5 is
a void witness and its semantic content is inadmissible as evidence about that
run, so the corpus test deliberately does **not** print it: a passing test's
output is exactly where an inadmissible fact would acquire a citable home. The
corpus is here to prove the reader is total, not to say what those runs did.

## Next

Exact candidate admission → graph/governed-revision binding → C6
(`capture.go --numstat -z`, untouched) → reviewer executable identity → the
next self-hosting campaign, measured against the 673-line baseline in
`external-witness-baseline.md`.
