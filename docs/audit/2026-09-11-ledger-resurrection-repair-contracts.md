# Repair contracts: the two capability-resurrection families

**Status:** repair-contract pass, revision 4 — **FROZEN**, amended after the Family A
implementation. Revision 3 fixed two stale statements; revision 4 adds §6 (A-R1, ratified
from the implementation), the `Valid` guardrail, the second-owner record, and the bounded
historical-reach statement. Family A is implementation-complete; Family B is not started.
All four open rulings have been made by the owner and are recorded in §4. The next change is
implementation against these contracts, not further architectural invention.
**Date:** 2026-09-11
**Subject:** `globulario/sensei` @ `63e25c66`. Every case below was REPRODUCED at that head
before this document was written.
**Purpose:** define, separately for each family, the exact invariant a repair must establish
and the negative cases it must reject — so the repair is written against a contract rather
than against a symptom.

Tracked: #352, #353 (Family A) · #354 (Family B) · #355, #356 (substrate hardening, out of
scope here).

---

## 0. Why two contracts and not one

A single unifying theory was proposed — *damaged history masquerades as genuine absence* —
and tested before any repair was written. It covers #352 and both members of #353. It does
**not** cover #354, where the history is complete, valid and append-only as designed.

```text
Family A   damaged history          read as   legitimate absence
Family B   INTACT history           read as   the wrong current fact
```

An integrity anchor that fully establishes history completeness would certify Family B's chain
and the resurrection would still occur. The families need different repairs, and merging them
would produce a repair that appears to close four defects while closing three.

---

## 1. FAMILY A — completeness and integrity witness

### 1.1 Invariant the repair must establish

> **A-INV (owner's formulation, ratified). No authority-bearing reduction may interpret event
> ABSENCE until history completeness has been positively established against a monotonic
> witness that is not reconstructible from that history.**

The earlier phrasing — *absence may be concluded only from an intact history* — is preserved as
the informal reading, but the formulation above is the one the repair is written against,
because it names the two things a repair must supply: WHEN absence may be interpreted, and WHAT
establishes that it may.

The precise failure today is narrower than "no integrity checking". The chain's cryptographic
consistency IS verified. What is being mistaken is **consistency of what remains** for **proof
that nothing is missing** — and `foldGovernance` then converts absence of `admission_consumed`
into `GrantModify: true`.

Note the shape: the repair does not need to make destruction impossible. It needs to make
destruction **distinguishable from absence**. The fold's existing rule — *only genuine absence
may move the task to an earlier phase* — is correct and stays; what is missing is any way to
establish that an absence is genuine.

### 1.2 THE HARD PART — RULED

**A length witness cannot be derived from the thing whose length it witnesses.**

`HEAD.yaml` is today the only record of how long the chain is. But `reconcile.go` explicitly
treats HEAD as non-authoritative and *rewrites it from the recomputed chain* when they
disagree — so after truncation the documented recovery path destroys the last trace. A witness
that is regenerated from the artifact it guards witnesses nothing.

Any repair must therefore answer: **what establishes chain length that is not itself derived
from the chain?** Candidate directions, none chosen here:

```text
(a) an out-of-band anchor        the witness lives outside the task dir; truncating the task
                                 no longer truncates the witness
(b) a monotonic external counter the repo (not the task) records the highest sequence it has
                                 ever seen for that task
(c) accept in-band and downgrade  truncation becomes undetectable-but-refused: any chain whose
                                 HEAD disagrees is BLOCKED rather than reconciled
```

**RULING (A-Q1): (b) — an authority OUTSIDE the reconstructed chain.**

`HEAD.yaml` remains a recoverable projection / cache. It must NOT become the integrity root: a
witness derived from the chain cannot prove that chain's completeness. The repair introduces a
monotonic external fact of the form *"this task has reached at least sequence N / digest D"*,
which reconstruction may VALIDATE AGAINST but may never **lower**, and may never **regenerate
from fewer entries**.

Those two prohibitions are the operative part. Today `reconcile.go` does exactly what is now
forbidden: on disagreement it rewrites HEAD from the recomputed (shorter) chain.

**RULING (the HEAD asymmetry, ratified as its own rule).**

```text
HEAD LAGGING the entries (the permitted in-flight case)   NOT an integrity failure
HEAD LEADING the entries                                   IS an integrity failure
```

> Observed history may legitimately be newer than its projection. A projection claiming history
> that no longer exists means evidence was lost.

That sentence is the discriminator the whole Family A repair turns on, and it preserves A-P2
without permitting A-N1.

**RULING (A-Q2) — resolved as a CONSEQUENCE, not an independent decision.** With the authority
moved outside the chain, losing `HEAD.yaml` alone is the loss of a cache and is recoverable;
the external witness still detects truncation. A-Q2 was load-bearing only while HEAD was the
sole record of length. It must be re-opened if A-Q1's ruling is ever reversed.

### 1.3 Negative cases the repair MUST reject

Each is a reproducer that exists today and currently passes.

| # | input | current behaviour | required |
|---|---|---|---|
| A-N1 | delete the highest-sequence entry | `Valid=true`, one `ledger.head_stale` **warning**; fold re-grants `GrantModify=true` | refused: not a valid chain, and no reconstruction of an earlier phase |
| A-N2 | delete the entire `ledger/` directory | `Valid=true entries=0`; `scope_verified` terminal erased; re-seeds from genesis | refused: a task with prior state must not read as a task with no chain |
| A-N3 | delete a payload-referenced artifact | `Valid=true`; the record becomes unreadable; a guard keyed on `err == nil` stops refusing | refused: artifacts referenced by a verified entry are inside the integrity boundary |
| A-N4 | delete a MIDDLE entry | already refused (`ledger.sequence_gap`) | must STAY refused — the repair must not regress the one case that works |
| A-N5 | delete `HEAD.yaml` only | nothing reported; state unchanged | **RULED (A-Q2): not an integrity failure.** With the authority external, HEAD is a recoverable cache; the external witness still detects truncation. The repair must REBUILD it, and must not treat its absence as evidence about the history's length |

### 1.4 Positive cases the repair MUST NOT break

| # | case | why it matters |
|---|---|---|
| A-P1 | a task with genuinely no chain yet initializes normally | this is the legitimate absence A-INV protects |
| A-P2 | HEAD lagging by exactly one in-flight entry still recovers | pinned by the existing `TestVerifyRecoversWhenEntryExistsButHeadIsStale`; the repair must distinguish HEAD *behind* from HEAD *ahead* |
| A-P3 | a healthy governed chain still folds to its correct phase | the six #351 fold outcomes must be unchanged |

A-P2 is the sharp one: the existing test pins the inverse of A-N1. HEAD may legitimately lag
the entries by one; it may never legitimately lead them. A repair that refuses both breaks a
working recovery path, and one that permits both closes nothing.

---

## 2. FAMILY B — semantic selection

### 2.1 Invariant the repair must establish

> **B-INV (strengthened by owner ruling). For a SINGLETON event type, every occurrence must
> satisfy that type's payload contract, and the occurrence COUNT must satisfy that type's
> protocol rule. There is no selection algorithm for a single-use fact.**

The revision-1 phrasing ("fold, don't read the latest") was too weak: a rule of the form *scan
further back when the newest event lacks an artifact* would treat a malformed AUTHORITATIVE
event as transparent, which is its own defect. Consumption is a **uniqueness predicate**, not a
replaceable projection, so the reader does not choose between candidates at all:

```text
count == 0                          genuinely unconsumed
count == 1 && valid && binding      consumed
count >  1                          INTEGRITY FAILURE
count == 1 && malformed/nonbinding  INTEGRITY FAILURE
```

"Latest valid consumption" is eliminated as a concept. The dangerous move was never choosing
the wrong candidate — it was having candidates.

Two conflations are packed into today's behaviour and both must be separated:

```text
"the latest event of type T"   is not   "the current value of the fact T carries"
"a record that failed to read" is not   "a record that does not exist"
```

### 2.2 Negative cases the repair MUST reject

| # | input | current behaviour | required |
|---|---|---|---|
| B-N1 | append a second `admission_consumed` carrying **no artifact** | the real consumption becomes invisible; loader errors; a guard keyed on `err == nil` stops refusing | the consumption remains visible, OR a typed integrity error — never "absent" |
| B-N2 | append a well-formed but non-binding record of the same type | not tested today | must not shadow the binding record |
| B-N3 | a record present but undecodable | `latestArtifactFromChain` returns `(false, nil)` → caller converts to "not found" | typed integrity error, as `tasksession.decodeGovernedArtifact` already does |
| B-N4 | two `admission_consumed` events for one single-use capability | accepted | **RULED (B-Q1): integrity failure in itself.** Once one binding spend exists a second cannot supersede it; the lifecycle already models a consumed capability as a distinct single state, not a replaceable projection |

### 2.3 Positive cases the repair MUST NOT break

| # | case |
|---|---|
| B-P1 | a legitimately re-recorded `admission_decided` (re-admission) still supersedes the earlier one — **not every event type is single-occurrence** |
| B-P2 | a task with genuinely no consumption still reads as unconsumed |

B-P1 and B-N4 together are the reason this contract cannot simply say "the earliest record
wins". Supersession is legal for some event types and illegal for others, and **the repair must
make that per-type rule explicit** rather than inferring it from scan direction.

> **"Latest" must be a DECLARED property of an event class, never an emergent property of
> iterating backward.**

Today the protocol semantics of every event type are encoded, accidentally, in the direction of
a `for` loop. That is the deeper defect B-N1 is a symptom of.

### 2.4 The adjacent contract gap this exposes

`ValidateTaskEventPayload` requires an artifact for **2 of 19** event types; none of the five
admission-v2 events is among them. So "an `admission_consumed` event without a
`capability_consumption` artifact" is currently a *well-formed* ledger entry. That is the root
of B-N1: the reader is not being fooled, it is being handed a record the writer's own
validator declared legal.

Two candidate levels of repair, and the choice belongs to the owner:

```text
writer-side   every event type that CARRIES an artifact must REQUIRE it   → B-N1 unappendable
reader-side   fold, and treat a malformed record as an integrity error    → B-N1 appendable
                                                                             but harmless
```

**RULING (B-Q2): BOTH.** Writer-side validation stops Sensei producing bad histories;
reader-side validation stops imported, older-version, hand-appended, corrupted and
alternate-writer histories from being trusted. They are not alternatives, and the precedent is
already in the codebase: `consumptionBinds` deliberately re-validates canonical receipt
structure AND cross-record relations on read, precisely because the producer is not the whole
trust boundary.

The reader-side change is the load-bearing one; the writer-side change narrows the intake.

---

## 3. What this contract pass deliberately does not do

- **No implementation.** Neither family has a repair written. All four rulings are made (§4);
  what remains is implementation against them, not further architectural invention.
- **No unification.** Family A and Family B have separate invariants, separate negative sets,
  and will have separate repairs. The single-root-cause hypothesis was tested and partly
  falsified; this document records the falsification rather than routing around it.
- **No coverage of #355 or #356.** Path containment and verifier totality belong to substrate
  hardening, not to the resurrection families. Forcing them in would muddy both repairs.

---

## 4. Rulings — ALL FOUR MADE

| # | question | ruling |
|---|---|---|
| **A-Q1** | what establishes chain length, not derived from the chain? | **A monotonic external witness.** `HEAD.yaml` stays a recoverable projection and must not become the integrity root. The witness may be validated against, never lowered, never regenerated from fewer entries. |
| **A-Q2** | is losing `HEAD.yaml` alone an integrity failure? | **No — resolved as a consequence of A-Q1.** With the authority external, HEAD is a cache and its loss is recoverable. Re-open only if A-Q1 is reversed. |
| **B-Q1** | is a second single-use event an integrity violation in itself? | **Yes.** Consumption is a uniqueness predicate; a second spend cannot supersede the first. |
| **B-Q2** | writer-side, reader-side, or both? | **Both.** Writer-side narrows intake; reader-side is load-bearing, per the `consumptionBinds` precedent. |

Plus one rule ratified in its own right: **HEAD lagging is legitimate, HEAD leading is an
integrity failure** (§1.2).

---

## 5. Freeze

These contracts are frozen. The next change is IMPLEMENTATION against them, not further
architectural invention.

What the repair must satisfy is now a predicate set rather than a description:

```text
Family A   A-INV  +  reject A-N1, A-N2, A-N3  +  keep A-N4 rejected  +  preserve A-P1..A-P3
Family B   B-INV  +  reject B-N1..B-N4        +  preserve B-P1, B-P2
```

Out of scope here and deliberately not folded in: #355 (path containment) and #356 (verifier
totality). They belong to substrate hardening; including them would turn two bounded repairs
into a governance mega-patch.

---

## 6. Amendments ratified from the Family A implementation `[R4]`

Implementation surfaced one protocol question the contract did not answer, and one limit worth
stating precisely. Both are ruled here rather than left in code.

### 6.1 A-R1 — structural validity and referential completeness are distinct facts

A-N3 requires the system to REFUSE authority-bearing interpretation when a referenced artifact
is missing. It does not require structural chain verification to own that refusal. Refusing at
chain level flattened `completion`'s `event_without_valid_receipt` -> `broken_completion` into
generic chain invalidity — a vaguer refusal, not a safer one.

> **A-R1.** Structural validity and referential completeness are distinct facts.
>
> Structural verification establishes the ledger sequence, hashes, witness relation, and other
> properties intrinsic to the recorded chain.
>
> Referential verification establishes that artifacts required by verified entries exist and
> satisfy the contract needed by the consuming authority.
>
> A structurally valid chain MAY therefore be referentially incomplete.
>
> No authority-bearing reduction may interpret absence, restore an earlier phase, grant
> authority, or report completion unless the required referential completeness has also been
> established.
>
> A missing or invalid referenced artifact MUST be refused at or before the authority-bearing
> consumer, but the refusal SHOULD preserve the most specific available diagnosis rather than
> being collapsed into generic chain corruption.

This makes the implemented `inspect` (diagnosis) versus `Complete` (authority) distinction
intentional protocol rather than accidental implementation.

### 6.2 The `Valid` guardrail — mandatory, not advisory

`Valid` now means STRUCTURAL validity. It must never silently come to mean "safe for
authority-bearing use".

> `CompletenessEstablished` is MANDATORY at every authority-bearing boundary. A consumer that
> reads `Valid == true` and skips the second predicate reconstructs Family A exactly.

The two predicates are separate fields for that reason; collapsing them back into one is the
regression to watch for.

### 6.3 Second-owner integration — authorized, and bounded

```text
completion/{inspect.go, integration.go}

Authorized because these are authority-bearing CONSUMERS of Family A's new completeness
fact. They consume the new semantics; they do not define or widen them.
```

A new integrity fact that no authority-bearing reduction may ignore necessarily reaches the
consumers that make completion decisions — A-N1/A-N2 forbid reconstruction into an earlier
authority state, and A-N3 requires missing referenced evidence to stay refused. **No broader
`completion/` sweep is authorized by this ruling.**

### 6.4 Historical reach — bounded by A-Q1, and deliberately so

> **Family A is implementation-complete. New history is protected by the external completeness
> witness. Existing unwitnessed history is never retroactively certified: it remains
> `completeness_unwitnessed` until a subsequent append establishes a witnessed forward
> boundary, recorded as `Bootstrapped: true`. Damage after that boundary is detectable.
> Pre-bootstrap completeness is not asserted.**

This is a consequence of A-Q1, not a shortcut. The witness is a monotonic fact external to the
reconstructed history and can never be regenerated from fewer surviving entries — so certifying
an unknown historical prefix because the new mechanism has arrived would violate the very
invariant being repaired.

### 6.5 The implementation recreated the defect it repaired `[preserved deliberately]`

`witness.go` documents that disagreement has direction and that normalisation destroys
evidence. Forty lines away, verification read the entry list and then read HEAD and the witness
— two observations from different moments, compared as though simultaneous. A concurrent
append landing between them was reported as `ledger.history_truncated` /
`ledger.head_leads_entries`: damage where there was only concurrency. Measured at 89 and 70
spurious reads in ~1.2s.

Three instruments failed before it was found — isolation runs (which remove the contention),
a baseline that silently measured a build failure, and a lock-contention hypothesis that did
not fit the facts. Each tested a STORY about the mechanism. The instrument that worked tested
the observable invariant instead.

The fix is ordering, not more checking: read the lower bounds FIRST, so a concurrent append can
only add entries afterwards and the comparison errs toward "complete", never toward "lost".

