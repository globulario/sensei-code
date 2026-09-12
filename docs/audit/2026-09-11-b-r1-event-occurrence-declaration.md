# B-R1 — the per-event-type occurrence declaration

**Status:** ratified declaration, revision 1. **FROZEN before any Family B implementation.**
**Date:** 2026-09-11
**Derived at:** `globulario/sensei` @ `739133dc` (main), from the canonical declarations —
`closureprotocol/vocabulary.go` `LedgerEventTypes` (19 members), `ledger/event.go`
`ValidateTaskEventPayload`, the recorded writers, and the artifact keys readers actually
decode. **Not from contract prose or recollection.**
**Governs:** Family B (#354) of the capability-resurrection cluster.

---

## 0. Why this exists before code

The frozen contract requires that **"latest" be a DECLARED property of an event class, never
an emergent property of iterating backward.** Today the protocol semantics of all nineteen
types are encoded, accidentally, in the direction of a `for` loop — which is why an appended
artifact-less `admission_consumed` hides a real one (#354).

B-P1 and B-N4 together forbid a single rule: `admission_decided` may legitimately recur and
supersede, while a second `admission_consumed` is an integrity failure in itself. Cardinality
therefore cannot be a consequence of scan direction, and it cannot be one bit.

---

## 1. The axes

Three occurrence classes. **A fourth was proposed and rejected**: `change_observed`'s
"idempotent replay, conflicting replacement refused" is not a new historical cardinality, it is
SINGLETON on a semantic key with idempotent command replay. Adding a class would have hidden
the missing axis.

```text
occurrence_semantics   SINGLETON | SUPERSEDING | ACCUMULATING | UNRATIFIED
cardinality_key        the scope the semantics apply WITHIN (task, epoch, question_id, ...)
replay_rule            exact-idempotent | append | refuse
artifact_rule          required(kind...) | none
binding_rule           whether cross-record/task binding must validate on read
reader_rule            the count predicate, the declared latest, or fold-all
producer_status        ACTIVE | RESERVED_UNPRODUCED
```

**`producer_status` is orthogonal to cardinality**, not a class of it. A token the vocabulary
recognises but no producer emits is not *unknown*; what it lacks is a ratified contract.

---

## 2. The declaration — all 19 types

### 2.1 Governed admission path

```text
authority_resolved              SUPERSEDING   key: admission epoch      ACTIVE
  within one epoch      zero or one resolution; exact replay returns the existing one;
                        a CONFLICTING second resolution = INTEGRITY FAILURE
  across epochs         a new resolution may supersede the prior CURRENT authority ONLY
                        because a separately governed event established a new epoch
  artifact_rule         required: authority_resolution, actor_binding, change_plan,
                        base_binding   — RATIFIED, not inferred from decoder behaviour
  binding_rule          yes — actor/base/plan must bind the task
  reader_rule           the resolution CURRENT for the epoch under evaluation

  WHY ALL FOUR ARE REQUIRED (ratified). They are not artifacts the current reader happens to
  decode. Together they CONSTITUTE the recorded authority from which the admission request is
  deterministically reconstructed: ActorBinding, BaseBinding, ChangePlan and the resolution
  digest all feed the semantic digest that must exactly equal the recorded decision's
  RequestDigestSHA256. Remove any one and it is no longer provable that the decision binds the
  authority state that produced it.

  Each also carries distinct authority meaning:
    actor_binding        the authority-bearing principal; the typed binding that replaced a
                         display-only requested_by, and which must participate in the digest
    base_binding         the repository/task/session/policy world authority was granted in,
                         and later what binds a consumption back to the same task and session
    change_plan          the bounded operation set; it participates in admission binding and
                         then determines the modification targets the fold grants
    authority_resolution the authority-resolution digest admission must bind, and which
                         admission must fail closed on when missing, stale or mismatched

  This requirement is therefore part of the authority/admission binding contract. A later
  "simplification" that stops decoding change_plan must not be readable as B-R1 permitting its
  absence.

  NOTE ON SCOPE OF THIS UPGRADE: it applies to THIS ROW ONLY. Every other artifact_rule in
  this table is derived from current writer/reader practice and remains exactly that. A reader
  decoding an artifact is not, on its own, evidence that the contract requires it.

  RULING: authority_resolved CANNOT MANUFACTURE ITS OWN RIGHT TO SUPERSEDE. Making "later"
  sufficient would turn it into an authority-WIDENING primitive: re-recording it with wider
  targets would re-widen the envelope of an already-minted capability. Superseding is legal
  only downstream of a separately established admission epoch. Earlier resolutions remain
  historical evidence.

admission_decided               SUPERSEDING   key: admission epoch      ACTIVE
  artifact_rule  required: admission_decision        binding_rule  yes (request digest, plan)
  reader_rule    the decision current for the epoch          [RULED — B-P1]

admission_consumed              SINGLETON     key: capability id        ACTIVE
  count == 0                          genuinely unconsumed
  count == 1 && valid && binding      consumed
  count >  1                          INTEGRITY FAILURE
  count == 1 && malformed/nonbinding  INTEGRITY FAILURE
  replay_rule    refuse      artifact_rule  required: capability_consumption
  binding_rule   yes — decision digest, capability id, task, session, operation subset, expiry
  reader_rule    THE COUNT PREDICATE. There is no selection algorithm.   [RULED — B-N4]

change_observed                 SINGLETON     key: governed admission/change epoch   ACTIVE
  first binding observation       accepted
  exact replay                    reuses the existing fact; NO new semantic occurrence
  conflicting second observation  INTEGRITY FAILURE
  replay_rule    exact-idempotent   artifact_rule  required: observed_change_set
  binding_rule   yes             reader_rule  the observation for the epoch under evaluation

  NOTE: NOT superseding. A conflicting observation cannot replace an established fact. The
  divergence guard in verify-admission already treats it as immutable once established. If the
  implementation appends a byte-identical second event today, that is to be EXAMINED, not used
  to justify a new protocol class.

scope_verified                  ACCUMULATING  key: subject / result revision   ACTIVE
  artifact_rule  required: scope_verification      binding_rule  yes
  reader_rule    the verification BOUND TO THE CURRENT SUBJECT — never merely "the latest"

  RULING: deliberately NOT superseding. A failed verification against digest X followed by a
  passing one against digest Y after a real repair is a legitimate sequence, and B does not
  make A false. Both are evidence. Selecting "latest" would normalise away exactly the
  directional evidence Family A was repaired to preserve.
```

### 2.2 Task lifecycle

```text
legacy_import           SINGLETON      key: task                ACTIVE
  It establishes provenance for entry into the governed history. Re-import is a NEW task or
  import identity, never a second origin event in one history.
  artifact_rule  none declared          replay_rule  refuse

task_prepared           SINGLETON      key: task                ACTIVE
  Measured: 18 occurrences across 18 real ledgers, 0 tasks with more than one.
  artifact_rule  required: closure_request, graph_input_snapshot     replay_rule  refuse

convergence_advanced    ACCUMULATING   key: iteration           ACTIVE
  Measured: 43 across 18 tasks, 11 tasks with more than one. Each occurrence IS an iteration;
  latest-wins would discard the others.       artifact_rule  none declared

closure_assessed        ACCUMULATING   key: iteration           ACTIVE
  Paired 1:1 with convergence_advanced (43/18, 11 with >1). Each assessment belongs to its
  iteration and is evidence, not a stale projection to erase.

task_control_projected  SUPERSEDING    key: task                ACTIVE
  43 across 18, 11 with >1. A projection: only the current one is meaningful.

result_transition_...   ACCUMULATING   key: result identity / transition   ACTIVE
  artifact_rule  required: result_transition_receipt   [ALREADY ENFORCED writer-side]
  The name describes historical transitions, not a replaceable projection.

question_disposition_.. ACCUMULATING   key: question_id         ACTIVE
  artifact_rule  required: question_disposition_receipt  [ALREADY ENFORCED writer-side]
  Singleton per QUESTION, accumulating across the task — which is why the key axis exists.

certified               SINGLETON      key: task lifecycle      ACTIVE
completed               SINGLETON      key: task lifecycle      ACTIVE
revoked                 SINGLETON      key: task lifecycle      ACTIVE
  Governed monotonic lifecycle facts. Retry is idempotent; it must not append a second
  certification/completion/revocation. Multiple occurrences have no legitimate selection
  semantics.        replay_rule  exact-idempotent

  OUT OF SCOPE HERE: whether `revoked` may follow `completed`. That is CROSS-TYPE LIFECYCLE
  LEGALITY, governed by the transition graph, not occurrence semantics. If the transition is
  permitted both records remain valid history and the fold may end at `revoked`; if forbidden,
  the PAIR is illegal. Encoding it here would grow teeth in the wrong mouth.
```

### 2.3 Declared but unproduced — 4 of 19

Measured: **zero non-test, non-declaration references.** No producer exists.

```text
evidence_recorded     proof_discharged     migration_executed     task_marked_stale

  producer_status       RESERVED_UNPRODUCED
  occurrence_semantics  UNRATIFIED

  native writer   MUST NOT emit
  reader          an occurrence MUST NOT participate in authority reduction, and yields a
                  TYPED refusal: declared_but_unratified_event
  activation      requires an explicit contract change assigning cardinality, payload,
                  binding and authority semantics BEFORE a producer is written
```

The typed refusal is deliberately narrower than "corruption": the vocabulary RECOGNISES the
token, so it is not unknown. What is absent is a ratified producer contract, and the refusal
should say which of the two it is.

```text
task_marked_stale
  semantic_role  durable NON-TERMINAL observation   [already ruled in prior work]
  Annotated, not assigned cardinality. More prior architectural intent exists for this one
  than for the other three, whose disposition earlier work explicitly left undecided.
```

---

## 3. Writer-side consequence (B-Q2)

`ValidateTaskEventPayload` requires an artifact for **2 of 19** types today —
`result_transition_recorded` and `question_disposition_recorded`. Every `artifact_rule:
required` row above is currently unenforced writer-side, which is the direct cause of #354:
an `admission_consumed` with no `capability_consumption` is a WELL-FORMED entry, so the reader
is not being fooled — it is handed a record the writer's own validator declared legal.

B-Q2 is ruled BOTH. Writer-side narrows the intake; **reader-side is load-bearing**, because
imported, older-version, hand-appended and alternate-writer histories never pass through this
validator at all.

---

## 4. Frozen

This declaration is frozen. Family B proceeds: **B-N1…B-N4 and B-P1/B-P2 contract tests
first, then implementation, then one mutation per guard.** No implementation may amend this
table; an amendment is a separate ratified act, as §6.1 of the repair contracts had to be.
