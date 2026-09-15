# Application bridge: the custody boundary

**Status:** design/proof packet, revision 5 — FROZEN. UNCOMMITTED, for section-by-section challenge.
No implementation.
**Date:** 2026-09-11
**Subject revision:** `globulario/sensei` @ `63e25c66e1c2ce230b41a2fc3bc77617bcd946d2`
(PR #351 head, frozen, awaiting independent review) and `globulario/sensei-code` @ working tree.
**Authorization:** design and proof only. Implementation NOT authorized where it depends on
unresolved B1 or #351 semantics. Every proposition is tagged `[independent]`,
`[depends: #351]` or `[depends: B1]`.

§1-§2 are **measured**, with anchors. §3 onward are **proposed**.

Revision 2 incorporated four owner rulings: A-7 required; `IntendedEffect` binding is
provenance, never satisfaction; Q1 resolves through exclusive mutation custody rather than
unforgeable self-reporting; custody means owning the application primitive.

**Revision 5 `[R5]` — FROZEN.** Four dependency/commissioning contradictions created by
layering Q6 onto the earlier rulings are repaired: Q1 granted accountability before Q6, §8
claimed it outright, I-3 hid two owners inside one invariant, and §12's sequence contradicted
its own order. A capability ladder (§8.1) now states the three claimable rungs explicitly.
**No further architecture revision is expected**; another pass is more likely to manufacture
machinery than to expose a missing contract.

**Revision 4 `[R4]`** repaired three statements the R3 rewrite left stale — I-2 still carried
the abolished subset rule, A-9 named a field that no longer exists, and FM-7 claimed a
detection strength §3.1 had already withdrawn — corrects the dependency table directly rather
than by footnote, and records the Q6-Q8 rulings plus a new Q9.

**Revision 3 applied the section-by-section challenge of revision 2** (`0365555f…`). Five
corrections were required and are marked **[R3]** throughout: the detection claim is weakened
to final-state accountability; proposal identity is split from application identity; the crash
window gets an explicit attempt record; application cardinality is pinned; the proposal
artifact must be durably retained. It also records M5 and M6, which were not known at
revision 2 and which move one adversarial test out of the independent column.

---

## 0. The two framing corrections

**0.1 The missing step is custody, not comparison.** The comparison layer exists and is
bidirectional (§2). What is missing is the step before it:

```text
authorized  →  RECORDED APPLICATION ACT  →  observed result  →  compare  →  proof
                       ^ absent
```

**0.2 What the system proves today is ENVELOPE COMPATIBILITY, not application custody.**

```text
Today:                              Required:
authorized(a.go)                    authorized operation O
      ↓                                   ↓
[unknown actor / unknown edit]      bridge applies O
      ↓                                   ↓
a.go changed                        act A binds O + input + pre-state + post-state
      ↓                                   ↓
VerifyScope ✓                       observation corresponds to A
                                          ↓
                                    VerifyScope ✓
```

Both diagrams end in a green check. They are not the same claim. The left one says *a file
that was allowed to change, changed*. **[R3]** The right one says: *the recorded proposed
mutation, applied under the authorized operation, is the mutation that produced the observed
state.* That formulation keeps three things separate which the earlier wording ("the
authorized change is the change that happened") silently merged — the authorization, the
custody of the application, and whether the content satisfies the intent. The operation does
not authorize content semantically (M3), so custody can never claim it did. A-7 (§7) is the falsifier that separates them, and it is therefore
REQUIRED, not optional: if drift on an admitted path is acceptable, the bridge collapses into
better telemetry and should not be built.

---

## 1. Measured state of the boundary

| Stage | Exists? | Anchor |
|---|---|---|
| Admitted change plan | yes | `closureprotocol.ChangePlan` / `ChangeOperation`, `model.go:122-136` |
| Consumed capability | yes | `CapabilityConsumption`; `consumptionBinds`, `tasksession/governance.go` |
| Authorized operation IDs | yes | `admission.admittedOperationSet`, `capability.go:108` |
| **Application act** | **NO** | nothing — see M1, M4 |
| Actual filesystem mutation | outside both repos | see M4 |
| Observed change | yes, **post-hoc** | `resulttransition.ObserveChange`, `bind.go:300` |
| Compare actual ↔ authorized | yes | `admission.VerifyScope`, `scope_v2.go:107` |
| Proof / contradiction | yes | `admission.ScopeViolation` |
| Governed result | yes | result transition recording |

**M1 — no code applies a plan operation.** No path from `ChangePlan` to a filesystem write
exists. The only output at the mutation step is an instruction string:

```go
// tasksession/session.go:1524
NextAction{Action: NextPerformEdit, Reference: strings.Join(decision.Envelope.ModifyPaths, ", "),
           Summary: "edit only the admitted modify envelope"}
```

**M2 — observation is reconstruction.** `ObserveChange(repoRoot, baseRev, resultRev,
actorDigest, authorityDigest)` diffs two git revisions. `actorDigest` and `authorityDigest`
are **parameters copied in from the admission** — asserted, never observed.

**M3 — `IntendedEffect` is write-only AND free text.** Declared `IntendedEffect string`
(`model.go:129`, `omitempty`) and read nowhere in the codebase (zero non-declaration
references outside tests). Two consequences, which must not be conflated:

```text
"the act is bound to the intent that authorized it"   achievable now: digest a free-text
                                                       field for provenance and tamper-evidence
"the act satisfied that intent"                        NOT DETERMINISTICALLY machine-
                                                       verifiable under the current
                                                       free-text contract
```

**[R3]** The limit is the contract, not the ambition: a learned evaluator could form a
probabilistic judgement about free text. What cannot exist is a *deterministic mechanical
proof of semantic satisfaction from the current contract alone*. Binding `IntendedEffect`'s
digest is therefore worth doing, and closes **no semantic gap**.

**M4 — the instruction is wired to nothing.** `NextPerformEdit`, the literal string
`"perform admitted edit"`, and `NextAction` have **zero consumers in `sensei-code`**
(measured across the whole repository, not only Go). No component reads the envelope and
applies it. The real pipeline is therefore not "authorize, hand custody to an external actor
under a governed instruction" but:

```text
authorize  →  emit an instruction nobody consumes  →  an external agent edits with ordinary
filesystem tools  →  reconstruct what happened afterwards
```

This makes the gap larger and the design cleaner: **there is no half-bridge to preserve.**
The first custody boundary can be built without unwinding an embedded execution path.

**M5 `[R3]` — the observation carries no per-path content identity.** `ObservedFile` is
`{Path, ChangeType, FromPath, ToPath}` (`scope_v2.go:28-33`). It records *that* a path changed
and *how*, never *to what*. Content identity exists only at whole-tree granularity
(`BaseTreeDigestSHA256`, `ResultTreeDigestSHA256`). **Consequence: A-3 is not checkable
today** — the per-path observed content it would compare against does not exist, so A-3 moves
out of the independent column and into "requires extending an existing owner's type" (§8).
This also deepens F-A: the comparison layer does not merely fail to judge content, it never
sees it.

**M6 `[R3]` — path-escape contracts already exist; mode and type contracts do not.**
`admission.safeRelPath` (`admission.go:2288`) and `closureprotocol/validate.go:164` already
reject absolute paths, `..`, `../` prefixes and embedded `/../`. **The bridge must reuse
these, never re-implement them** — duplicated validation rules diverge silently and each copy
looks locally correct. They are pure path-STRING checks, however: file mode, file type,
executable bit and symlink identity are covered by nothing, anywhere, which is precisely the
gap the canonical-entry ruling in Q2 names.

---

## 2. What the comparison layer already proves

`VerifyScope` (`scope_v2.go:107-240`) proves, bidirectionally and **by path**: every admitted
mutation operation has a matching observed change at its `Target` with a compatible
`ChangeType`; every observed file is an admitted target, a required generated artifact, or
`scope.file.out_of_envelope`; prohibited prefixes rejected; renames fail closed; required
generated artifacts present; duplicate path observations rejected; observed base tree is the
admitted base tree.

**This is a good layer. The bridge must not duplicate, absorb or re-judge it** (§5).

### 2.1 What it cannot prove, and no post-hoc diff can

- **F-A. Location is not content.** An admitted `modify src/a.go` is satisfied by *any*
  modification of `src/a.go`. With M3, nothing anywhere compares a change to its intent.
- **F-B. Actor identity is asserted, not observed** (M2).
- **F-C. The application has no time.** Capability spent BEFORE the edit; next governed record
  is the observation. #351's G3 is the symptom: a consumed receipt cannot distinguish
  consume-before-apply from apply-before-crash. That is missing evidence, not a reader defect.
- **F-D. In-envelope drift is indistinguishable from the intended edit.** The diff is
  whole-tree over `baseRev → resultRev`; any unrelated change landing on an admitted target is
  absorbed into the proof. **This is the failure that decides the bridge's value** (§0.2).

---

## 3. Invariants

> **I-1 `[independent]` An applied change must be represented by a recorded act, not
> reconstructed solely from a later diff.**

- **I-2 `[independent]` `[R4 — corrected]`** The act names **exactly** the operation IDs spent
  by its bound consumption. **No application-time subset is permitted in v1.** The subset
  already happens at consumption — `consumptionBinds` requires `ConsumedOperationIDs` to be a
  non-empty subset of the admitted set — so subsetting again at application would create a
  second place where the same narrowing decision is made. (Revision 3 left the old subset
  wording here while §6.2 abolished it; an invariant must outrank the explanatory section
  below it, so the invariant is what changed.)
- **I-3a `[independent]` `[R5 — split]`** The application act records canonical pre-state and
  post-state **entry** digests (§6.3) for every affected path.
- **I-3b `[blocked: Q6]` `[R5 — split]`** The observation exposes canonical result-entry
  identity sufficient to compare its final state against the application's recorded
  post-state.

  Revision 4 carried both halves in a single `[independent]` invariant whose second clause —
  "so observation is checked against what was done" — is exactly what M5 says cannot happen
  today. That hid **two owners inside one invariant**: the bridge owns I-3a, the observation
  boundary owns I-3b. A-3 proves I-3b; the bridge's own tests prove I-3a.
- **I-4 `[independent]` `[R3 — SPLIT, was a design blocker]`** The act records **two distinct
  identities**, bound as admission binds an actor. Revision 2 carried a single
  `ExecutorActorBinding`, which under proposal → bridge applies (§5.1) is incoherent: the
  principal that *requested* the mutation and the principal that *wrote the bytes* are
  different by construction, and collapsing them would bake in exactly the semantic
  overloading #351 has just spent four rounds removing.

```text
ProposalActorBinding          the agent or person that proposed the mutation
                              MUST equal the consumption's ConsumerActor
ApplicationPrincipalBinding   the bridge component that actually wrote the bytes
                              MUST NOT be required to equal the consumer; it is the
                              custodian, and its identity is what makes custody attributable
```

  F-B becomes checkable against the first; custody becomes attributable through the second.
- **I-5 `[independent]`** The act is ledger-recorded strictly between consumption and
  observation; a chain with both and no act between them is an integrity failure, never an
  earlier phase.
- **I-6 `[independent]`** The act's input is a **content-addressed proposed patch**, bound by
  digest before any byte is written. The bridge's input is a typed artifact, not a side
  effect — which is what makes I-3 checkable against a *declared* input rather than a
  reconstructed one.
- **I-7 `[depends: #351]`** The lifecycle needs a position between `admitted` and
  `mutation_observed`; today `foldGovernance` goes `StatusAdmitted` → `StatusMutationObserved`
  with nothing between. Adding one widens the persisted vocabulary, moves `StateDigest`, and
  must pass through the `lifecycleaction` closed table. **Undecidable while #351 is unmerged.**

### 3.1 The custody guarantee, split `[R3 — materially corrected]`

Revision 1 asked whether the act could be forged by the actor. That is unanswerable when the
actor is the execution principal, and it was the wrong question. The decidable form:

> **Mutation without a recorded act must be mechanically UNAVAILABLE or independently
> DETECTABLE.**

**Revision 2 then overstated the right-hand branch.** It claimed "mutation without a recorded
act is observable". That is false. An external actor can do:

```text
state A  →  unauthorised edit  →  state X  →  revert  →  state A  →  ObserveChange
```

and no post-hoc comparison can discover the mutation EVENT. The same applies to a transient
modification of a path already claimed by an act, restored to the expected post-state before
observation. Content digests do not expose a transient.

What is actually detectable is narrower, and is a **state-integrity** guarantee rather than
**event custody**:

> **The observed final state over the governed mutation set must equal the state explained by
> the ordered set of recorded application acts, from the admitted starting state.**

That is precise and mechanically testable. Its v1 name is **recorded application + post-state
accountability** — deliberately not "application custody".

**A-7 alone does not establish it.** A-7 catches an *unclaimed path*; A-3 catches *same-path
content divergence*; the ordering relation (I-5) sequences them. Only together do they yield
the property above, and per M5 the A-3 half is not checkable until the observation carries
per-path content identity.

| | Guarantee | Requires | Class |
|---|---|---|---|
| **Accountability** | final-state effects not explained by recorded acts are detectable | A-7 + A-3 + ordering; **A-3 needs M5 resolved** | `[independent]` except the M5 extension |
| **Prevention** | mutation without bridge custody is impossible | the execution-model change in §5.1 | mechanism `[independent]`; trust argument `[depends: B1]` |

**What may and may not be claimed.** The independent column may be commissioned before B1 and
described as recorded application with post-state accountability. It may **NOT** be described
as "all governed mutations are under application custody" — that claim belongs after
prevention, and stating it earlier would be exactly the kind of overclaim this packet exists
to prevent.

## 4. Failure modes

| ID | Failure | Why it survives today |
|---|---|---|
| FM-1 | Apply a change nobody admitted, on an admitted path | only location compared (F-A) |
| FM-2 | Apply admitted paths with unrelated content | `IntendedEffect` unread (M3) |
| FM-3 | A second actor applies the change | actor asserted, not observed (F-B) |
| FM-4 | Crash between apply and record; replay applies twice | **`[R3]` NOT closed by the receipt alone — see §4.1** |
| FM-5 | Working-tree drift absorbed into the proof | whole-tree diff (F-D); **the decisive one** |
| FM-6 | Act recorded but no mutation performed | the act is a claim; I-3 digests make it checkable |
| FM-7 | Agent edits the tree behind the bridge's back | **`[R4 — corrected]`** behind-the-bridge edits whose final-state effects SURVIVE may be exposed by the accountability checks (A-7 + A-3 + ordering); **transient or reverted mutation is not detectable at all** (§3.1); prevention requires B1 |
| FM-8 | Act recorded out of order w.r.t. consumption/observation | I-5 |
| FM-9 | Proposal and applied bytes differ | I-6; the bridge must prove it applied what it was given |

### 4.1 The crash window `[R3 — new, FM-4 is not closed by the receipt]`

Revision 2 implied FM-4 was answered by recording the act. It is not. The sequence
`apply → record post-state → emit ApplicationAct` leaves an unavoidable window:

```text
patch successfully changes the filesystem  →  CRASH  →  no ApplicationAct exists
```

On restart the system cannot replay, and cannot distinguish: this bridge applied the patch
before crashing / another actor made the same change / application was partial / application
never began. Post-hoc accountability (§3.1) reports an unexplained final-state effect but
cannot attribute it, which is the same missing-evidence problem F-C names one layer down.

**An attempt record is therefore required before the first byte is mutated**, without yet
adding a lifecycle state:

```text
application_prepared      operation IDs, proposal digest, expected pre-state, ATTEMPT ID
        ↓ filesystem mutation
application_recorded      same attempt ID, exact resulting post-state
```

A crash between them becomes an explicit **indeterminate application attempt** rather than
"no evidence" — a named state a human or a recovery routine can act on.

**Recovery must never replay automatically from `application_prepared` alone.** It must
either prove the expected post-state was reached, prove no application happened, or **block
for reconciliation**. Automatic replay from an indeterminate attempt is precisely how FM-4
applies a change twice.

---

## 5. Ownership boundary

The bridge owns **application custody, and nothing else** — and custody means **owning the
application primitive**, not producing a receipt around somebody else's edit. A component
that only observes is a witness, not a custodian.

```text
authority resolution   → admission             (existing)
admission decision     → admission             (existing)
capability consumption → admission             (existing)
APPLICATION CUSTODY    → the bridge            (NEW, and only this)
observation            → resulttransition      (existing)
scope verification     → admission.VerifyScope (existing)
lifecycle position     → lifecycleaction       (#351)
intent evaluation      → a later owner, if IntendedEffect ever becomes checkable (M3)
```

### 5.1 Execution model `[mechanism independent; trust argument depends: B1]`

Given M4 there is no existing execution path to preserve. Two candidate models:

1. **proposal → bridge applies.** The actor returns a typed, content-addressed patch; the
   bridge alone writes to the governed tree.
2. **isolated workspace → bridge promotes.** Execution happens elsewhere; the bridge owns
   promotion of resulting mutations into the governed tree.

**(1) is proposed for v1**, because the proof is far smaller and the bridge's input becomes a
typed artifact that can be bound before anything is written.

```text
agent
  ↓ proposes a content-addressed patch for admitted operation O
bridge
  ├─ checks the consumed authority admits O
  ├─ checks every patch target is in the admitted operation set
  ├─ records pre-state digests
  ├─ applies the exact patch
  ├─ records post-state digests, and that they equal the patch result
  └─ emits the application receipt
```

### 5.2 The bridge's claim, stated narrowly `[independent]`

> I applied these exact proposed bytes, under this exact admitted operation and capability,
> to this exact pre-state, producing this exact post-state.

That is the whole claim. It is custody evidence, never a verdict.

**The bridge MAY decide (mechanical, deterministic, locally checkable):**

- the proposal parses into a typed patch;
- every target is in the admitted operation set;
- the patch applies cleanly to the recorded pre-state;
- **`[R3]` the filesystem state immediately before application STILL equals the recorded
  pre-state.** Checking that a patch *can* apply to a recorded pre-state is not enough; the
  governed tree must still be that state at the instant the bridge acts. Otherwise
  `bridge checks A → external writer changes A to B → bridge applies the patch assuming A` is
  a classic TOCTOU hole. Without prevention (§3.1) the race cannot be eliminated, but the
  bridge must **refuse whenever the precondition it actually observes has changed**;
- the resulting bytes equal the patch result;
- the pre-state is the admitted base;
- **`[R3]`** every target satisfies the EXISTING path contracts — `admission.safeRelPath` and
  `closureprotocol/validate.go:164` (M6). The bridge reuses them and adds no path machinery
  of its own.

**The bridge MUST NOT decide:**

- whether the patch implements `IntendedEffect`;
- whether the change is architecturally valid;
- whether a refactor preserves higher-level behaviour;
- whether the resulting change set conforms to the admitted envelope — **`VerifyScope` owns
  that predicate**, and a bridge that re-judged it would become a second authority over an
  owned predicate. That is precisely the condition #351 exists to end.

**`[R3]` The proposal artifact must be durably retained.** A content-addressed digest lets
you verify a proposal *if somebody still holds it*; it does not preserve the evidence. Without
retention the receipt degrades over time into "there once existed some bytes hashing to X",
and the §5.2 claim becomes unverifiable precisely when it is most needed.

> The artifact addressed by `ProposalDigestSHA256` MUST be durably retained for at least the
> lifetime of the application evidence chain it belongs to.

Downstream, unchanged: `VerifyScope` says *and those changes stayed inside the admitted
scope*; a later intent evaluator may say *and they satisfy the architectural intent*.

---

## 6. Receipts `[R3 — restructured]`

Three structural corrections from the revision-2 challenge: identities split, intent bound
per operation, cardinality pinned.

```text
ApplicationPrepared  (written BEFORE the first byte, §4.1)                 class
  AttemptID                    binds this attempt to its completion        independent
  CapabilityID, DecisionDigestSHA256, ConsumptionDigestSHA256              independent
  Operations[] { OperationID, IntendedEffectDigestSHA256 }                 independent
  ProposalDigestSHA256         the content-addressed patch           (I-6) independent
  ExpectedPreState[] { Path, PreStateEntryDigest }                   (Q2)  independent
  ProposalActorBinding         == the consumption's ConsumerActor    (I-4) independent
  ApplicationPrincipalBinding  the bridge itself                     (I-4) independent
  PreparedAt                   RFC3339

ApplicationRecorded  (written AFTER application)
  AttemptID                    SAME as the prepared record                 independent
  Files[] { Path, ChangeType, PreStateEntryDigest, PostStateEntryDigest }  independent
  BaseTreeDigestSHA256                                                     independent
  AppliedAt                    RFC3339
```

**6.1 Per-operation intent `[R3]`.** Revision 2 had plural `AppliedOperationIDs` beside a
singular `IntendedEffectDigestSHA256`. If one act covers several operations each has its own
`IntendedEffect`, so intent binds inside `Operations[]`. (Provenance only — M3.)

**6.2 Cardinality `[R3]`.** Revision 2's I-2 allowed an act to apply a *subset* of the
consumption's operations, which implies several acts per consumption and therefore needs a
uniqueness rule — otherwise a replay yields two perfectly valid receipts for the same admitted
operation. **v1 takes the smaller rule the owner prefers:**

```text
one consumption  →  one proposal  →  one application attempt  →  one committed act
```

with a multi-file patch. The existing model permits this cleanly: **the subset already happens
at consumption** — `consumptionBinds` requires `ConsumedOperationIDs` to be a non-empty subset
of the admitted set — so application need not subset again. The act covers **exactly** the
consumption's operation set, and I-2 is restated accordingly.

The uniqueness rule is retained as defence in depth, because the cardinality rule is a
convention and this is a check:

> **An operation ID MUST NOT be committed by two `ApplicationRecorded` acts for the same
> consumption.** (A-16.)

**6.3 State entry identity `[R3, Q2 ruling]`.** Per M5 no per-path content identity exists
today, and per M6 mode and type are covered nowhere. A raw byte digest would call two states
identical when their tree semantics differ. `PreStateEntryDigest` / `PostStateEntryDigest`
therefore digest a **canonical entry representation**, not bytes:

```text
content identity  |  file type  |  mode / executable bit  |  symlink target representation
|  absence distinguished from an empty file
```

Full content stays in the content-addressed proposal artifact (§5.2 retention); the ledger
carries digests only.

Ledger events `application_prepared` and `application_recorded`, ordered strictly between
`admission_consumed` and `change_observed` (I-5). The reader-side validator mirrors
`consumptionBinds` and `scopeVerificationBinds`: canonical validator first, then cross-record
relations, every failure typed as an integrity error rather than an earlier phase.

`[depends: #351]` Whether either event implies a `Status`/`Phase` pair — I-7. The receipts can
be designed now; the lifecycle position cannot.

---

## 7. Adversarial tests

Each must COMPILE and FAIL against an implementation lacking its specific guard. Per the
#351 result, **a witness that trips an earlier guard proves nothing about a later one**:
five of eight mutations survived the first pass for exactly that reason, so each guard needs
its own case.

| ID | Falsifier | Targets | Class |
|---|---|---|---|
| **A-7** | Observation contains an in-envelope path no recorded act claims | FM-5, FM-7 | **REQUIRED** `[independent]` |
| A-1 | Act claims an operation the consumption never spent | FM-1 | independent |
| A-2 | Act spends an operation admitted by a different decision | FM-1 | independent |
| A-3 | Post-state entry digest ≠ observed content at that path | FM-6 | **`[R3]` BLOCKED by M5** — the observation carries no per-path content identity; requires extending `ObservedFile` |
| A-4 | Pre-state digest ≠ the admitted base tree's content | FM-6 | independent |
| A-5 | Consumption and observation present, **no act between** | FM-4 | independent |
| A-6 | Act before its consumption, or after its observation, all digests matching so ORDER is the only defect | FM-8 | independent |
| A-8 | **`[R3 — corrected]`** Act's **ProposalActorBinding** differs from the consumption's `ConsumerActor` | FM-3 | independent |
| A-9 | **`[R4 — corrected]`** `Operations[]` is empty, **or its operation-ID set differs from the bound consumption's `ConsumedOperationIDs`** | I-2, §6.2 | independent |
| A-11 | Applied bytes ≠ the bound proposal | FM-9 | independent |
| A-12 | Patch targets a path outside the admitted operation set | FM-1 | independent |
| A-13 | Patch does not apply cleanly to the recorded pre-state | FM-6 | independent |
| A-14 | **`[R3]`** Crash after the bytes change but before `application_recorded`; restart must NOT blindly replay | FM-4, §4.1 | independent |
| A-15 | **`[R3]`** The tree changes after pre-state validation but before application | TOCTOU §5.2 | independent |
| A-16 | **`[R3]`** The same operation ID appears in two committed acts | §6.2 | independent |
| A-17 | **`[R3]`** Receipt carries a proposal digest but the proposal artifact is unavailable | §5.2 retention | independent |
| A-18 | **`[R3]`** Application principal and proposal/consumption principal are deliberately DIFFERENT, and each is checked under its own role | I-4 | independent |
| A-10 | Act forged with every digest internally consistent | FM-7 | **`[depends: B1]`** — undecidable without the prevention guarantee; recorded so the gap is visible rather than absent |

**A-7 is required because it is the only falsifier that fails today and cannot be made to
pass by any post-hoc rule.** **`[R3]`** It is not, however, sufficient on its own: A-7 catches
an unclaimed path, A-3 catches same-path content divergence, and only together with the
ordering relation do they establish §3.1's accountability property. A-3 is currently blocked
by M5.

**`[R3]` Path escape is NOT a new bridge test family.** Symlinks, `../`, absolute paths and
embedded `/../` are already contracted by `admission.safeRelPath` and
`closureprotocol/validate.go:164` (M6); the bridge reuses them, and a falsifier belongs to
those owners, not here. File **mode**, **type** and **symlink identity** are covered by
nothing today — they enter this packet only through the canonical entry representation
(§6.3), and a falsifier for them is owed once that representation exists.

---

## 8. Dependency summary

| Proposition | Class |
|---|---|
| I-1, I-2, I-3a, I-4, I-5, I-6, FM-1, FM-2, FM-3, FM-5, FM-6, FM-8, FM-9, §4.1 crash protocol, §5 boundary, §5.2 claim, §6 receipts, A-1, A-2, A-4…A-9, A-11…A-18 | `independent` |
| **I-3b, and A-3 which proves it** | **`blocked: Q6`** — needs per-path observed entry identity (M5) |
| I-7 (lifecycle position), receipt implying a Status/Phase pair | `depends: #351` |
| Prevention guarantee (§3.1), FM-7 prevention, A-10 | `depends: B1` |
| §5.1 mechanism | mechanism `independent`; trust argument `depends: B1` |

**`[R5 — corrected]`** The independent bridge machinery, **when composed with the Q6
observation prerequisite**, provides final-state accountability against recorded application
acts. **Without Q6 it provides recorded application evidence only.** In neither case does it
prove exclusive custody, and in neither case does it detect mutations whose effects disappear
before observation (§3.1). Revision 4 asserted the composed claim unconditionally two
paragraphs above the sentence that withdrew it.

### 8.1 The capability ladder `[R5]`

Three separately claimable rungs, each with its own evidence. Nothing may borrow a rung it has
not earned:

```text
Unit 1 alone                       →  recorded application evidence
Unit 0 + Unit 1                    →  recorded application + post-state accountability
Unit 0 + Unit 1 + B1 prevention    →  exclusive application custody
```

**`[R4]`** A-3's dependency is now a row in the table above rather than a corrective
paragraph beneath it. Until Q6 closes, §3.1's accountability property is only
half-established, and the commissioning rule in §11 follows from that.

---

## 9. Questions and rulings

1. **Q1 — RULED.** Custody resolves through exclusive mutation authority, not unforgeable
   self-reporting; prevention and detection stay distinct (§3.1). **`[R3]`** And: detection-only
   IS sufficient for a bridge v1 milestone — but is NOT sufficient to call the V3 custody
   substrate complete. **`[R5]` The milestone is gated on Q6, not only on B1:** before Unit 0
   closes A-3 the commissionable claim is **recorded application evidence**; only *once Q6 has
   closed A-3* may it be commissioned, still before B1, as **recorded application + post-state
   accountability**. Implementation of the independent portion is not blocked on B1; the claim "all
   governed mutations are under application custody" is.
2. **Q2 — RULED `[R3]`.** Digests in the receipt; actual content retained in the
   content-addressed proposal artifact; never duplicated into the ledger. Digest a **canonical
   tree entry** (content, type, mode/executable bit, symlink target, absence vs empty file),
   not raw bytes — §6.3. Per M5/M6 no such representation exists today, so this is new
   construction rather than reuse.
3. **Q3 — RULED.** A-7 is a required falsifier (§0.2), and `[R3]` not sufficient alone (§7).
4. **Q4 — RULED `[R3]`.** `IntendedEffect` is NOT redesigned as part of the bridge. The
   narrative field stays as narrative. If deterministic intent evaluation is later needed, it
   arrives as a SEPARATE structured contract beside the prose:

```text
ExpectedEffects[] { kind, subject, predicate, expectedValue / evidence }
```

   The prose says *why*; the structured predicates say *what can be mechanically falsified*.
   Making one string serve humans, models and deterministic proof would create another
   overloaded owner — the failure this packet's ownership section exists to avoid.
5. **Q5 — RULED `[R3]`.** The bridge should become sensei-code's first real consumer of a
   sensei-emitted action, but **not of the current instruction string**. M4 shows
   `NextPerformEdit` is presentation, not protocol; it may remain a human-readable projection
   and must not become the machine interface. A typed path crosses the boundary instead:

```text
Sensei  --typed ApplyRequest / application authority-->  Sensei Code bridge
                                                              ↓ proposed patch
                                                         deterministic application
```

   The standing **"required Sensei Code consumer"** condition counts as satisfied only when
   that typed path is **exercised end to end**, never when the data type merely exists.

---

## 10. Unit-boundary and retention rulings `[R4]`

- **Q6 — RULED. `ObservedFile` extension is a PREREQUISITE SUB-UNIT, not bridge-owned work.**
  The reason is ownership, not size: `ObservedFile` belongs to the observation/comparison
  pipeline, and the bridge has no business redefining what an observation *means* merely
  because it needs richer evidence. Unit 0 does only enough to expose canonical observed entry
  identity — a `ResultEntryDigest` on `ObservedFile`, and base identity only if its owner
  naturally derives it. **The observation type must not be made to mirror `ApplicationAct` for
  the bridge's convenience.**

  **Commissioning rule.** Until Q6 closes, the bridge may be commissioned as **recorded
  application evidence** only. It may **NOT** be commissioned as *recorded application +
  post-state accountability*, because §3.1 requires A-7 + A-3 + ordering and A-3 is not
  executable without Unit 0.

- **Q7 — RULED. Retention is REFERENCE lifetime, not calendar time and not automatically
  indefinite.** Task lifetime is too short — a completed task may still be audited, challenged,
  cited by a later regression, or referenced by another receipt. "Indefinite" is a storage
  policy wearing an invariant's clothes.

  > **A proposal artifact MUST remain retrievable while any retained governed record can
  > transitively rely on its digest as evidence.**

  Collection is lawful only when no retained `ApplicationPrepared`, `ApplicationRecorded`,
  verification, result, proof, audit or custody record transitively references the proposal.
  At that point it becomes eligible for retention-policy GC. **This packet deliberately sets no
  calendar duration**; that is operations policy, and writing one here would convert a
  referential invariant into a number nobody can defend.

- **Q8 — DIRECTION RECORDED, not ruled (B1 is unresolved).** v1 proceeds as planned with
  proposal → bridge applies. For the eventual B1-backed prevention guarantee the stronger
  candidate is **isolated workspace + bridge-owned promotion**, rather than making ordinary
  filesystem writes impossible inside a shared workspace.

  The argument is proof surface. Confinement-based prevention makes the guarantee depend on
  permissions, UIDs, inherited descriptors, helper processes, editors, shell tools, race
  windows and platform differences — it can be made strong, but it turns the bridge into an
  operating-system security project. Isolation moves the claim to an authority boundary:

```text
agent workspace                     typed, content-addressed        governed tree
arbitrary experimentation   ──────>  proposal  ──> bridge-owned  ──>
NO authority over the                              promotion
governed tree
```

  The agent keeps ordinary filesystem tools; it simply has no ordinary filesystem **authority
  over the governed tree**. This matches the existing philosophy: do not try to prove the actor
  behaved well inside its sandbox, prove that only a governed promotion crosses the boundary.

  > **Recorded direction:** B1 should prefer structural separation of writable experimentation
  > from governed state, with bridge-owned promotion, unless later measurement demonstrates an
  > equivalently strong but substantially simpler prevention mechanism.

---

## 11. Remaining open questions

- **Q9 `[R4, new]` `[depends: B1?]` — what makes `ApplicationPrincipalBinding` meaningful?**
  Splitting the identities (I-4) was necessary but is not sufficient. A receipt asserting
  `ApplicationPrincipalBinding = "sensei-code bridge"` proves nothing unless that identity is
  anchored to something outside the receipt — service or process identity, binary identity,
  instance/session identity, or a B1 principal.

  **This is deliberately not solved here.** It is recorded because otherwise
  `ApplicationPrincipalBinding` becomes the new M2: an identity *passed into* a record rather
  than *observed and bound*, which is the precise defect this packet criticised in
  `ObservedChangeSet.ActorBindingDigestSHA256`. Repeating it one layer up, in the record whose
  whole purpose is custody attribution, would be worse than the original.

  > What evidence binds `ApplicationPrincipalBinding` to the principal that actually possessed
  > the application primitive, rather than to a caller-supplied identifier?

---

## 12. Commissioning sequence `[R5 — corrected]`

Revision 4 put Unit 0 first *and* annotated Unit 1 with "evidence only, until Unit 0 lands",
which cannot both hold in one ordered sequence. The prerequisite ruling is preserved; the
claim is expressed as composition instead.

```text
Unit 0   observation identity extension                    owner: observation boundary
         ObservedFile gains canonical per-path result identity
         → A-3 becomes executable, I-3b becomes provable          (Q6)

Unit 1   application bridge                                owner: the bridge
         typed ApplyRequest · content-addressed proposal
         ApplicationPrepared · deterministic application
         ApplicationRecorded · crash reconciliation · retention
         → ALONE:              recorded application evidence
         → COMPOSED with a landed Unit 0:
                               recorded application + post-state accountability

#351     supplies the lifecycle position (I-7), once independently accepted
B1       establishes the trustworthy application principal (Q9) and prevention
later    isolated workspace + bridge-owned promotion (Q8)  → exclusive custody
```

**Falsifier ownership `[R5]`.** "A-1…A-18, owner: the bridge" was too broad: **A-3 crosses the
observation boundary.** The bridge may own the integration falsifier, but it does not own the
predicate or the input that makes that falsifier possible.

```text
A-1, A-2, A-4…A-18   owner: the bridge
A-3                  cross-owner integration falsifier, enabled by Unit 0
```

**The load-bearing conclusion:** B1 no longer blocks most of the application bridge. It blocks
the strongest custody *claim* — exclusive custody — not the deterministic evidence machinery
underneath it. Once Q6 closes, that machinery can be built and falsified independently of both
B1 and #351.

---

## 13. Freeze `[R5]`

This packet is frozen for architecture. The remaining uncertainties are deliberately
externalised, each sitting at a named boundary rather than leaking into implementation:

```text
Q6     known prerequisite, owner known (observation boundary)
#351   lifecycle semantics, frozen at 63e25c66 pending independent review
Q9/B1  application-principal identity trust, explicitly unresolved
Q8     prevention architecture, direction recorded, deliberately deferred
```

What began as a vague missing box has decomposed into three separately claimable rungs —
recorded act → post-state accountability → exclusive custody — with the exact evidence required
for each. The missing execution substrate is no longer an architectural unknown: it is bounded
implementation work plus two already-named external dependencies.
