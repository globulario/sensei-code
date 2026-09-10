# Unit 1 — Lifecycle Authority: Decision Packet

**Date:** 2026-09-10
**Repository under design:** `globulario/sensei`
**Baseline revision measured:** `42475333dd22165c12f144491fcb1bc22760096c` (PR #350 head, parked)
**Status:** DESIGN ONLY — no code written, no PR touched. Requires owner approval
and independent review before any implementation begins. **Committed 2026-09-10**
on branch `docs/unit1-lifecycle-authority-packet`, off the sensei-code#167 branch
head, so the measurements it records bind to a revision that contains them. The
companion assessment (`2026-09-10-t01-abandonment-transition-assessment.md`)
remains uncommitted, so references to it in §1 and §5 currently dangle.
**Revision:** round 7, 2026-09-10. Records the demonstrated G3 result and
replaces the ambiguous `permission.modify` predicate with an owner-specified
**operation-state decision table** derived from the verified ledger (§4.5), with
`GrantModify` split into four separately-typed, ledger-owned capabilities. Traces
`synthesis-run` / `synthesis-admit` / `synthesis-apply` against that table
(§5.5.11): four missing conjunctions recorded as **G3.1–G3.4**, plus a third
authority for the single-use guarantee and a fail-open gate that accepts
`waiting`. Fixtures reclassified. **No repair implemented.** Round 6,
2026-09-10. **G3 is demonstrated** (§5.5.10): on disposable
fixtures with zero binding-verification errors, the public permission surfaces
disagree with the verified ledger in *both* directions — over-reporting
`modify=admitted` where governance grants nothing (F1) and under-reporting
`waiting` where governance grants (F2) — and an execution boundary that accepts
the ungoverned answer as authority is traced in source. Not presently exploitable
on live records; the guard standing in the way is binding health, an L5 property.
Round-5 scoping corrected: "no reachable bypass" applies to the eight stale tasks
observed, not to fresh bindings. **Round 5, 2026-09-10.** Falsifies the seven cached permissions by
read-only execution (§5.5.9): **no presently reachable authorization bypass** —
all eight tasks are refused at every public surface, suppressed by staleness. The
conflicting-authority design (G3) is recorded as latent. Category 2 renamed
*same-directory migration candidates*; G1 restated as a missing bridge *contract*;
G4 split into G4a (missing production invocation) and G4b (missing ledger
integration). Unit 1's likely design rule recorded at §4.4. **Round 4,
2026-09-10.** Completes the five-lifecycle authority mapping
(§4.3) and classifies all 124 admission-decision records (§5.5.8). C-3 is
reclassified: not "an unread reader" but **G3, two authorities for one
predicate**, with one consuming path that omits the governance reducer. Four gaps
result, none of them a missing phase. `scope_verified` ownership reopened as
*establishes vs projects*; `revoked` held until its subject is explicit. No
implementation proposal. **Round 3, 2026-09-10.** The owner's 929-file source audit corrected
the framing this packet was built on: absence of a `task_phase` assignment is not
absence of a durable lifecycle fact. The 10/8 approval is **withdrawn**; §4 is
demoted from settled partition to hypothesis; §5.5's deletion recommendations are
restated as integration gaps; and the packet is reorganised around the owner's
four-question mapping rather than around one transition table. Read §0.1 first.
**Round 2, 2026-09-10.** The owner approved the 10/8 partition and
refused the edge set, the transition table and M2–M7 as previously written. This
round corrects the seven named defects, records the read-only measurement of
`evidence_recorded`, `proof_discharged`, `migration_executed` and
`task_marked_stale` (§5.5), and replaces the M3 hand-wave with a comparison of
three era-boundary mechanisms (§7.2). No code, PR, thread, publication or F5
mutation accompanies it.

## 0. Scope and boundaries

This packet decides a **model**, not an implementation. It contains no code, no
patch, no test, and no repair.

Explicitly not done and not authorized by this document:

- No code change in either repository.
- No modification to PR #350 (parked at `42475333`) or sensei-code#167.
- No reply to or resolution of any review thread.
- No awareness-graph publication.
- No change to the 493-line assessment
  (`2026-09-10-t01-abandonment-transition-assessment.md`), which stays
  uncommitted alongside this.

Implementation begins only after the decisions in §5 and §6 are ruled on.

## 0.1 Round-3 correction — read this before §2 and §4

**The owner's audit of 929 non-test, non-generated Go files at `42475333`
corrects a framing error that runs through rounds 1 and 2 of this packet.** The
error is load-bearing, so it is recorded here rather than only where it occurs.

**The error.** Rounds 1–2 measured "does this site assign
`ledger.TaskEventPayload.TaskPhase`?" and read a negative answer as *no durable
lifecycle fact exists*. Those are different claims. `TaskPhase` is
`omitempty` (`ledger/event.go:22`), and the payload carries
`Artifacts map[string]LedgerPayloadRef` alongside it. An event may establish a
lifecycle fact by referencing a **content-addressed typed receipt** and leave the
phase field empty. Several do.

`tasksession/governance.go:101-175` is the proof. `foldGovernance` reconstructs
the entire admission-side lifecycle — `waiting_governance`,
`ready_for_admission`, `admitted`, `mutation_observed`, `scope_verified`,
`waiting_mechanical_repair`, `refused` — by indexing the latest verified entry
per event type and **decoding the typed artifacts those entries reference**:
`authority_resolution`, `admission_decision`, `capability_consumption`,
`scope_verification`. It never reads a `task_phase` field. The phase it returns
is *output of a reconstruction*, not the stored authority.

**Consequences, each applied below:**

| Round-1/2 claim | Corrected |
|---|---|
| "Producible phases: 5 of 18" (§2.1) | **Phase *values explicitly emitted in the `task_phase` field*: 5 of 18.** Not a count of durable lifecycle facts. |
| "The other 13 phases are never written to any ledger payload" (§2.1) | True of the field; false as a statement about the facts. At least seven are reconstructed from durable receipts by `foldGovernance`. |
| "13 events do not write a phase ⇒ they must be made to" (§2.3) | Wrong direction. Most already establish their fact by receipt. The question is integration and agreement, not field population. |
| "`ready_for_admission`/`waiting_*` fail clause (a) — nothing causes them" (§4.2) | **Wrong.** `waiting_mechanical_repair` is caused by a durable `ScopeVerification` whose outcome is not verified (`governance.go:134-136`); `ready_for_admission` by a durable `authority_resolved` with no `admission_decided` after it (`governance.go:139-142`). They are *derived*, deterministically, from durable causes. That is not the same as uncaused. |
| "The 10/8 partition is APPROVED" (§4) | **WITHDRAWN by the owner.** It is a useful hypothesis, not a validated final design. §4 is demoted accordingly. |
| "Delete `evidence_recorded`/`proof_discharged`/`migration_executed`" (§5.5) | Overstated. The correct finding is **missing built-in event integration**, and source absence of a producer is not proof no external caller ever wrote one. §5.5 is restated. |

**What survives unchanged.** The corpus census (§5.5.5, §11.2) and the round-2
code citations. They were measurements of what they measured; the error was in
what was inferred from them. §5.5.5 is *strengthened* by the correction — see
§5.5.8, which is new.

**The revised target,** in the owner's words: *consistent interpretation and
enforcement of existing authoritative facts, not forcing every status into one
larger transition table.* Before any lifecycle claim is implemented it must
answer four questions (§4.0):

> What object does it describe? Which event or receipt establishes it? Which
> reader reconstructs it? Which write boundary enforces its consequences?

## 1. What Unit 1 must settle

Observation A-1 of the assessment established that
`closureprotocol.AllowedTaskTransitions` does not govern production, because the
phases it constrains are never persisted. §2 below extends that measurement from
three phases to the whole vocabulary, and the result is worse than A-1 alone
suggested.

**Corrected in round 3 (§0.1).** The dichotomy below is too coarse. There are not
two authorities but **five lifecycles** (§4.0.1), and `closureprotocol` is not the
sole owner of the law: `tasksession/governance.go` enforces the operation
lifecycle from typed receipts, correctly, today. What follows is retained as the
round-1 framing that motivated the measurement, with that correction attached.

The system was described in round 1 as having **two lifecycle authorities that
never meet**:

- **`closureprotocol`** declares a closed set of 18 durable task phases, a
  transition table over them, and terminal states with no outgoing edges. It owns
  the *law*.
- **`tasksession`** computes and reports similarly-named states — from live
  filesystem verification, from time-dependent decision binding, from convergence
  status — into in-memory structures, using its own untyped constants. It owns
  the *observed reality*, and produces none of the durable facts the law is
  written over.

Neither is wrong in isolation. The defect is that no boundary converts one into
the other, and the law is enforced at one call site over facts that mostly cannot
exist.

Round 1 concluded: *Unit 1 must decide, for every phase, is it an authoritative
durable fact or a derived assessment, and then make the closed vocabulary contain
only the former.*

**Round 3 replaces that goal.** The dichotomy is false — a fact can be durable
*and* reconstructed, which is what every state `foldGovernance` returns actually
is — and the goal it implies (curate a vocabulary) is not the goal that fixes
anything. The replacement is §4.0: map each lifecycle claim to its object, its
establishing event or receipt, its reader, and its enforcing write boundary, then
test agreement across those boundaries.

## 2. Measured baseline

All facts below measured at `42475333`. Method in §11.

### 2.1 Phase values explicitly emitted in the `task_phase` field: 5 of 18

**Corrected in round 3 (§0.1).** This section measured one thing and round 1
described it as another. What follows is an exhaustive census of the sites that
assign `ledger.TaskEventPayload.TaskPhase`. It is **not** a census of durable
lifecycle facts: `TaskPhase` is `omitempty`, and an event may establish its fact
by referencing a typed content-addressed receipt instead — which
`tasksession/governance.go:101-175` then decodes to reconstruct the phase. Read
the table as *field population*, nothing more.

The sites that assign the field are exhaustively:

| Phase | Producer | Event type |
|---|---|---|
| `scope_verified` | `resultrecording/classify.go:92` (`classifyBlocked`) | `result_transition_recorded` |
| `proving` | `resultrecording/classify.go:48` (`ProvingReady`) | `result_transition_recorded` |
| `certified` | `certification/ledger.go:161` | `certified` |
| `completed` | `completion/complete.go:233` | `completed` |
| `abandoned` | `completion/abandon.go:356` | `abandoned` |

**The other 13 phase values never appear in that field**: `prepared`,
`converging`, `ready_for_admission`, `admitted`, `mutation_observed`,
`waiting_architect`, `waiting_evidence`, `waiting_governance`,
`waiting_mechanical_repair`, `refused`, `stale`, `uncertifiable`, `revoked`.

**Seven of those thirteen are nonetheless reconstructed from durable receipts.**
`foldGovernance` returns `waiting_governance`, `ready_for_admission`, `admitted`,
`mutation_observed`, `scope_verified`, `waiting_mechanical_repair` and `refused`
by decoding `authority_resolution`, `admission_decision`,
`capability_consumption` and `scope_verification` artifacts bound to ledger
entries. Their absence from the field is a fact about serialization, not about
whether the system knows them.

### 2.2 The reachable transition graph

This subsection records what was measured at `42475333`. It is superseded as a
*proposal* by §4.1.1, and it is retained as the baseline the proposal repairs.
§5.5.5 adds the further measurement that none of these five phases has ever been
persisted in either repository.

Restricting `AllowedTaskTransitions` to edges between *producible* phases leaves:

```
scope_verified → proving → certified → completed
       ↓            ↓          ↓
   abandoned    abandoned  (revoked: unproducible)
```

`AllowedTaskTransitions` declares 18 states and roughly 40 edges. Five states and
six edges can occur. `scope_verified` and `proving` have no producible
predecessor, so the reachable graph has no entry point — every real task's first
phase-bearing event is a `result_transition_recorded`, regardless of everything
that happened before it.

### 2.3 Event producer classification — all 20 types

**Corrected in round 3 (§0.1).** The "Writes a phase" column is a fact about the
`task_phase` field. The "⇒ must write X" dispositions in the last column were
derived from the mistaken reading and are **withdrawn**: an event that
establishes its fact by typed receipt does not need to be made to populate a
field, and doing so would add a second representation of the same fact — the
failure mode this repository already has a name for. The column is retained
unaltered as the round-1 record; §4.0 replaces its reasoning.

| Event type | Producer site(s) | Writes a phase | Disposition |
|---|---|---|---|
| `legacy_import` | `ledger/legacy.go:60,68` | no | exception, contained (§8) |
| `task_prepared` | `tasksession/session.go:946` | no | ⇒ advancing: `prepared` |
| `convergence_advanced` | `tasksession/control.go:853`, `session.go:950` | no | ⇒ advancing once: `converging`; same-phase after (§4.1.4) |
| `closure_assessed` | `tasksession/control.go:857`, `session.go:954` | no | annotation — stays exempt |
| `admission_decided` | `admission/admission_ledger.go:82` | no | ⇒ advancing: `admitted` **only when the decision admits** (§4.1.5) |
| `authority_resolved` | `admission/admission_ledger.go:76` | no | annotation — stays exempt |
| `admission_consumed` | `admission/admission_ledger.go:88` | no | ⇒ same-phase (§4.1.4) |
| `change_observed` | `admission/admission_ledger.go:97` | no | ⇒ advancing: `mutation_observed` |
| `scope_verified` | `admission/admission_ledger.go:103` | no | ⇒ must write `scope_verified` (sole owner, ruled) |
| `result_transition_recorded` | `resultrecording/record.go:130,151` | **yes** | ⇒ `proving` only (ruled) |
| `question_disposition_recorded` | `questiondisposition/record.go:107,127` | no | exception, contained (§8) |
| `evidence_recorded` | **NONE** | — | ⇒ measured §5.5.2: delete the event, keep the record |
| `proof_discharged` | **NONE** | — | ⇒ measured §5.5.2: delete the event, keep the record |
| `certified` | `certification/ledger.go:155,158` | **yes** | ⇒ via gateway |
| `completed` | `completion/complete.go:227,230` | **yes** | ⇒ via gateway |
| `abandoned` | `completion/abandon.go:350,353` | **yes** | ⇒ via gateway |
| `revoked` | **NONE** — 2 consumers | — | ⇒ Unit 1 builds producer (ruled) |
| `migration_executed` | **NONE** | — | ⇒ measured §5.5.3: delete, or repurpose as the §7.2(b) era event |
| `task_control_projected` | `tasksession/control.go:861`, `session.go:971` | no | annotation — stays exempt |
| `task_marked_stale` | **NONE** | — | ⇒ retained (ruled); producer specified in §5.5.4, or delete |

**15 of 20 produced, 5 unproduced.** `revoked` is the only unproduced type with
*consumers*: `completion/complete.go:401` and `completion/load.go:222` both switch
on it and count revocations no code can create.

### 2.4 The parallel vocabulary

`tasksession/session.go:52-70` declares its own untyped constants. The
correspondence to `closureprotocol.TaskPhase` is near-total and undeclared:

| `tasksession` constant | Value | `closureprotocol` counterpart |
|---|---|---|
| `PhasePrepared`, `PhaseAdmitted`, `PhaseStale`, `PhaseUncertifiable` | `prepared`, `admitted`, `stale`, `uncertifiable` | identical strings, different type |
| `PhaseWaiting` | `waiting` | **no counterpart** |
| `StatusReadyForAdmission`, `StatusAdmitted`, `StatusMutationObserved`, `StatusScopeVerified`, `StatusWaitingArchitect`, `StatusWaitingEvidence`, `StatusWaitingGovernance`, `StatusWaitingMechanical`, `StatusRefused`, `StatusStale`, `StatusUncertifiable` | same strings as the phases | `TaskPhase` values, carried here as *statuses* |

Eleven `closureprotocol` **phase** names exist in `tasksession` as **status**
strings. `resultrecording/payload.go:14-20` compounds this: its
`validTransitionPhaseStatus` table pairs `PhaseScopeVerified` with statuses
`waiting_architect`/`waiting_governance`/`waiting_mechanical_repair` — i.e. the
same names are a phase in one authority and a status *under a different phase* in
the other. Three of that table's five phase keys
(`PhaseWaitingArchitect`, `PhaseWaitingGovernance`,
`PhaseWaitingMechanicalRepair`) are themselves unreachable, since
`ClassifyNextState` yields only `PhaseProving` and `PhaseScopeVerified`.

## 3. The classification rule

Proposed, and the single thing §4 applies mechanically.

**Corrected in round 3.** The rule below is sound; round 1 *applied* clause (a)
as "does a site write this into the `task_phase` field", which is not what it
says. Applied correctly — "does a specific durable event cause it" — several
states round 1 classified as failing (a) pass it: `waiting_mechanical_repair` is
caused by a durable `ScopeVerification` whose outcome is not verified, and
`ready_for_admission` by a durable `authority_resolved` with no subsequent
`admission_decided`. They are derived deterministically *from durable causes*,
which the rule does not forbid. §4.2's "(a) — nothing causes it" entries are
withdrawn.

> A phase belongs in the **closed durable vocabulary** if and only if all three
> hold:
>
> **(a) Caused.** A specific durable event causes it. Some actor or operation
> brings it about; it is not an opinion about the record. *(Causation is by
> event-or-receipt, not by field assignment.)*
>
> **(b) Stable.** Recomputing it later from durable inputs alone yields the same
> answer. A determination that depends on wall-clock time, on the working tree,
> or on any state outside the ledger is not stable.
>
> **(c) Load-bearing.** Some authority decision branches on it. If nothing refuses
> or permits an action because of it, it is a report, not a phase.
>
> A phase failing any clause is a **derived assessment**: recomputable, reportable,
> never a transition, and never terminal.

The rule's purpose is to make the failure in §2 unrepresentable. A state that
fails (a) cannot be recorded, so it must not be declared. A state that fails (b)
must not be terminal, because irreversibility cannot be asserted over a
determination that can flip.

## 4. Per-phase classification

**OWNER RULING 2026-09-10 (round 3) — THE 10/8 PARTITION IS WITHDRAWN AS
APPROVED.** It contains useful concepts and remains the leading hypothesis, but
it is **not** a validated final design and no implementation may assume it. The
round-2 text below is retained, corrected in place, and demoted: §4.1's canonical
edge set is a *candidate* for one lifecycle, not the task's single state machine.

The reason is §0.1's correction plus three source facts (§4.0.2). Sensei does not
have one lifecycle with eighteen states. It has several lifecycles that must
agree, whose states are reconstructed from receipts, and whose words — including
"terminal" — are scoped to the lifecycle that uses them.

### 4.0 The mapping obligation, which precedes any transition table

**This supersedes §3 as the gating test.** Before a lifecycle claim may be
implemented it must answer four questions:

1. **What object does it describe?** A task, an operation, a result, a binding, a
   receipt — these are different objects and a state of one is not a state of
   another.
2. **Which event or receipt establishes it?** By event *or receipt*. A typed
   content-addressed artifact bound to an entry is an establishment; so is a
   phase field; the two are alternatives, not a hierarchy.
3. **Which reader reconstructs it?** A fact with no reader is not load-bearing,
   whatever it is stored as.
4. **Which write boundary enforces its consequences?** The point at which
   believing the fact refuses or permits something.

A claim missing any of the four is not implementable. A claim answering all four
does not need to be a phase.

#### 4.0.1 The five lifecycles, their primary truth, and the bridges between them

**The target is not one lifecycle with all correct phases. It is five coordinated
lifecycles with explicit bridges** (owner, 2026-09-10).

| Lifecycle | Primary truth |
|---|---|
| Sensei Code workflow | Local orchestration events |
| Task | Durable task disposition / history |
| Authorized operation | Admission, consumption and scope receipts |
| Result | Result binding, proof and certification |
| Current validity | Re-evaluated binding, freshness and revocation assessments |

Sensei Code already makes this distinction on its own side:
`internal/event/event.go:124-127` documents `workflow.completed` as meaning **a
change was admitted** — not that Sensei recorded durable task completion — and
adds `workflow.observed` precisely because "a control plane whose only successful
ending is an admitted change" cannot report a read-only audit honestly.

**The bridges are where the dangerous bugs are**, not inside any one enumeration.
Measured state of the four that matter (evidence in §4.3):

| Bridge | Mechanism | State |
|---|---|---|
| operation → result | `advance_result.go:182-183` reads `disp.Terminal` from `foldGovernance` and enters `advanceAtScopeVerified` | **enforced** |
| result → task terminal | certification gates on verdict (`certification/ledger.go:131-135`); abandonment refuses when a result transition exists (`abandon.go:204-218`) | **enforced in both directions**, though the table did not say so until §4.1.1 |
| local workflow → governed task | none | **no enforced bridge measured.** The two vocabularies are documented as distinct and nothing checks that they agree |
| operation → local projection | `control.go:575-577` serves a cached control state without re-consulting the governance reducer | **UNGOVERNED — see §5.5.8** |

#### 4.0.2 Three source facts that forbid one flat table

**(i) `admitted` has two meanings for what is permitted, and the phase carries
neither.** `governance.go:162-175` returns `Phase: PhaseAdmitted` on both
branches. What differs is everything consequential:

| Condition | Phase | Status | Grant |
|---|---|---|---|
| `admission_consumed` recorded | `admitted` | `StatusAdmitted` | none |
| no consumption recorded | `admitted` | `StatusReadyForMutation` | `GrantModify: true`, `ModifyPaths` |

The single-use capability is spent by the *consumption receipt*, and the comment
there is emphatic that an unreadable one must never be mistaken for "unconsumed"
and "resurrect a mutation grant." Round 2 classified `admission_consumed` as
"same-phase" (§4.1.4) — correct about the phase and beside the point. **Making
the phase the enforcement object would lose the distinction that prevents a
second mutation.**

**(ii) `scope_verified` is terminal for the *operation* and the entry point to the
*result* lifecycle.** `governance.go:128-137` calls it "the non-mutable terminal"
and returns `Terminal: true`. `advance_result.go:182-183` then reads exactly that:
`case disp.Terminal: return advanceAtScopeVerified(...)`. The same fact ends one
lifecycle and begins the next. Round 2's §4.1.3 split terminality into
*absorbing* and *closed* — a real improvement, and still one global axis.
**Terminality is per-lifecycle**, and a third kind exists that neither round-2
name covers.

**(iii) A blocked result stays at `scope_verified` while reporting
`waiting_governance`, and that is not a conflict.** One names the established
milestone, the other the current blocker. Round 1 read the pair as two authorities
fighting over one slot (§2.4) and round 2 "resolved" it by deleting one
(§7 ruling). The correct reading is that they describe different things and both
are right. `governance.go:134-136` shows the shape: a `ScopeVerification` that is
present but not verified yields `waiting_mechanical_repair` — a durable receipt
outcome producing a blocker report, with no competing phase writer anywhere.

#### 4.0.3 What this changes about the work

Do **not** add phase writers to make a count come out even. That was the round-2
plan (M3, Group B) and it would have added a second representation of facts the
receipts already carry, then required the two to be kept in agreement forever.

Do instead, per lifecycle: name the object, name the establishing event or
receipt, name the reader, name the enforcing write boundary — then **test
agreement across the boundaries**, especially:

- terminal exclusion (a task cannot be both completed and abandoned; an operation
  cannot mutate after consumption),
- consumed authority (§4.0.2(i)),
- result identity (the same observed change is bound by scope verification and by
  the result transition),
- recovery (what an unreadable or absent record means, at every boundary — the
  A-1 mechanism generalised).

§9's D1–D9 are re-scoped to this in §9.0.
### 4.1 CANDIDATE — a task-lifecycle edge set (10) *(approval withdrawn)*

**Status after round 3: hypothesis.** What follows is a candidate edge set for
the **task** lifecycle only — object (1) of §4.0.1 — and it is not the operation
lifecycle, the result lifecycle, or a union of them. Its internal corrections
(reciprocal views, explicit predecessor sets, removal of the two contradictory
abandonment edges) stand as corrections. Its *status* does not: it may not be
implemented until §4.0's four questions are answered for each of its ten states,
and §4.0.2 shows at least three of them (`admitted`, `scope_verified`, and the
`waiting_*` family it excludes) do not survive that test as written.

Candidate phases: `prepared`, `converging`, `admitted`, `mutation_observed`,
`scope_verified`, `proving`, `certified`, `completed`, `abandoned`, `revoked`.

#### 4.1.1 The canonical edge list — the single source

Every other view in this packet is generated from this list. It is the object
the implementation declares; predecessor and successor tables are projections of
it and are never hand-maintained.

```
# from              -> to                 class
prepared            -> converging         ordinary
prepared            -> abandoned          ordinary
converging          -> admitted           ordinary
converging          -> abandoned          ordinary
admitted            -> mutation_observed  ordinary
admitted            -> abandoned          ordinary
mutation_observed   -> scope_verified     ordinary
mutation_observed   -> abandoned          ordinary
scope_verified      -> proving            ordinary
scope_verified      -> abandoned          ordinary
proving             -> certified          ordinary
certified           -> completed          ordinary
certified           -> revoked            governed
completed           -> revoked            governed
```

14 edges: 12 **ordinary** (available to ordinary task work) and 2 **governed**
(available only to the revocation authority of §5.4).

Changes from the refused version, each traceable to a ruling:

| Change | Reason |
|---|---|
| `proving → abandoned` **removed** | Contradicts abandonment's `no_result_produced` claim. Also aligns the table with code that already refuses it — `completion/abandon.go:204-218` refuses abandonment whenever `resultTransitionCount > 0`, and `result_transition_recorded` is the *only* producer of `proving` (`resultrecording/classify.go:48`). The edge was already unreachable; the table now says so. |
| `certified → abandoned` **removed** | Same contradiction. This edge was never in `AllowedTaskTransitions` (`vocabulary.go:230` gives `PhaseCertified: {PhaseCompleted, PhaseRevoked}`); the refused §4.1 table introduced it in error. |
| `converging → scope_verified` **removed** | It existed only to accommodate `resultrecording/classify.go:92` manufacturing `scope_verified` on the blocked path. The §7 ownership ruling ends that, leaving `mutation_observed` as the sole predecessor. |
| `admitted → converging`, `mutation_observed → converging` **removed** | These were the reciprocal inconsistency the owner flagged: `converging`'s predecessor list claimed them, `admitted`'s and `mutation_observed`'s successor lists did not. Rather than pick a side, §4.1.4 answers the underlying question — what a repeated `convergence_advanced` means — and the answer requires no back-edge. |

#### 4.1.2 Generated views

Generated from §4.1.1; reciprocity checked mechanically (see §11).

| Phase | Predecessors (generated) | Successors (generated) |
|---|---|---|
| `prepared` | *(none)* | `converging`, `abandoned` |
| `converging` | `prepared` | `admitted`, `abandoned` |
| `admitted` | `converging` | `mutation_observed`, `abandoned` |
| `mutation_observed` | `admitted` | `scope_verified`, `abandoned` |
| `scope_verified` | `mutation_observed` | `proving`, `abandoned` |
| `proving` | `scope_verified` | `certified` |
| `certified` | `proving` | `completed`, `revoked` *(governed)* |
| `completed` | `certified` | `revoked` *(governed)* |
| `abandoned` | `prepared`, `converging`, `admitted`, `mutation_observed`, `scope_verified` | *(none)* |
| `revoked` | `certified` *(governed)*, `completed` *(governed)* | *(none)* |

**Explicit predecessor set for `abandoned`**, replacing "any non-terminal":
exactly `{prepared, converging, admitted, mutation_observed, scope_verified}` —
five phases, enumerated, and provably the ones with no recorded result.
`proving`, `certified` and `completed` are excluded because a result exists;
`abandoned` and `revoked` are excluded because they are absorbing.

`prepared` is the sole origin. No other phase lacks a predecessor, so the
reachable graph has an entry point — the property §2.2 measured as missing.

#### 4.1.3 Terminality has two kinds — *and, per round 3, a third axis*

**Corrected.** The absorbing/closed split below is right and incomplete.
§4.0.2(ii) measured a third case: `scope_verified` is `Terminal: true` in
`foldGovernance` and is simultaneously the entry point `advance_result.go` reads
to begin result recording. That is terminality **scoped to a lifecycle** — the
operation ends, the task does not. Any predicate must therefore answer "terminal
*for which lifecycle*", and a single global `isTerminal` is wrong in a second way
beyond the one this subsection repairs.

The refused version called `completed` absolutely terminal while giving it an
outgoing edge. It is neither an error of the edge nor of the label but of using
one word for two things:

| Kind | Meaning | Phases |
|---|---|---|
| **absorbing** | No outgoing edge under any authority. The chain ends. | `abandoned`, `revoked` |
| **closed** | No outgoing edge under ordinary work. Supersedable by the governed revocation authority alone, and by no other actor or operation. | `completed` |

`certified` is neither: `certified → completed` is ordinary work.

The gateway therefore asks two questions, not one. "Is this phase absorbing?"
refuses everything. "Is this phase closed?" refuses ordinary work and admits a
`governed` edge only when the caller carries revocation authority. A single
`isTerminal` predicate cannot express the second, and collapsing them is what
produced the contradiction.

#### 4.1.4 Repeated-phase behavior — three event dispositions

An event's relationship to the phase is a *declared property of the producer*,
not something inferred from which payload fields happen to be set (§8's
containment principle applied to phases):

| Disposition | Effect on the chain | Gateway rule |
|---|---|---|
| **advancing** | Records a new phase. | Must be an edge in §4.1.1, of a class the caller has authority for. |
| **same-phase** | Durable event, carries the phase the task is already in, appends no transition. | The declared phase must equal the current phase, or it is refused. Not a self-edge: §4.1.1 has none and needs none. |
| **non-phase-bearing** | Annotation. Writes no `task_phase` at all. | Structurally unable to set the field (§8). |

Applied to the two cases the owner named:

**`convergence_advanced` occurs more than once.** Measured: it is the second most
common persisted event — 54 of 187 entries across 25 real ledgers, up to 9 times
in a single chain (§5.5.5). Its disposition is **advancing exactly once**
(`prepared → converging`) and **same-phase** on every later occurrence. A task
that has advanced past `converging` and then converges again stays at its
furthest durable phase; the fact that convergence work is happening is a derived
assessment, not a durable move backwards. This is what makes the removed
back-edges unnecessary rather than merely unwanted.

**`admission_decided` and `admission_consumed` cannot both blindly produce
`admitted`.** With §4.1.5 applied, `admission_decided` is **advancing** only when
the decision positively admits, and **non-phase-bearing** otherwise;
`admission_consumed` can only follow a positive decision, so the task is already
`admitted` and it is **same-phase**.

**Corrected in round 3.** That answer is true and answers the wrong question.
`governance.go:162-175` already returns `admitted` for both the consumed and
unconsumed cases; what the consumption changes is `Status` and `GrantModify`, not
the phase (§4.0.2(i)). So the phase was never going to collide — and a design
that enforces mutation permission from the phase would drop the single-use
guarantee the consumption receipt exists to provide. The enforcement object is
the receipt; the phase is a label.

#### 4.1.5 Phase production is conditional on payload meaning

A phase is produced by *what an event says*, never by *which event it is*.

This is not a new invention: `certification/ledger.go:131-135` already works this
way. It computes the verdict, and when the verdict is not `certified` or
`certified_with_conditions` it returns before appending anything — "a
non-certifying evaluation is a deterministic report; it never pretends a
certified event occurred." That comment is the rule. Unit 1 generalizes it from
one site to all of them.

The measured need is largest at admission. Across both repositories, 59
admission decisions are persisted at `<task>/admission/decision.yaml`:

| `decision` | Count | Positively admits |
|---|---|---|
| `admitted` | 10 | yes |
| `admitted_with_conditions` | 2 | yes |
| `waiting` | 27 | no |
| `refused` | 19 | no |
| `uncertifiable` | 1 | no |

**12 of 59 admit; 47 do not.** A blind `admission_decided ⇒ admitted` mapping
would be wrong on 80% of the decisions this system has actually made — and would
write the phase `admitted` for the 19 refusals, which is precisely the false
durable fact §3(a) exists to prevent.

Required condition table for the advancing dispositions:

| Event | Advancing only when | Otherwise |
|---|---|---|
| `admission_decided` | `decision ∈ {admitted, admitted_with_conditions}` | non-phase-bearing |
| `certified` | `verdict ∈ {certified, certified_with_conditions}` | not appended at all (existing behavior, retained) |
| `result_transition_recorded` | classification is `ProvingReady` | non-phase-bearing (the blocked path, per §7) |
| `task_prepared` | always | — |
| `convergence_advanced` | current phase is `prepared` | same-phase |
| `change_observed` | always (an observation of a change that occurred) | — |
| `scope_verified` | verification passed | non-phase-bearing |
| `completed` | a completion receipt binds a result | not appended |
| `abandoned` | no result transition exists (existing guard) | refused |
| `revoked` | revocation authority resolved | refused |

The condition is part of the phase's definition and is tested by D8 (§9), not
left to each producer.

**Payload schema (all ten).** Each carries the existing
`ledger.TaskEventPayload` with `task_id`, `session_id`, `task_phase`, `status`,
`artifacts`, and — new and required — the phase this transition moved *from*, so
that the admitted transition is self-describing rather than reconstructed by
scanning backward. `latestTaskPhase`-style backward scans become a fallback for
legacy chains only (§7.2).

**Relationship to result transitions.** `result_transition_recorded` is the only
event that both advances a phase and binds a result. Three constraints follow,
and all three are today unenforced (assessment R5-1):

1. No `result_transition_recorded` may be admitted from a terminal phase —
   absorbing or closed.
2. A chain carrying both `abandoned` and any `result_transition_recorded` is
   contradictory, whichever order they appear in.
3. `abandoned` asserts *no result was produced*; the gateway must therefore
   refuse abandonment when a result transition exists (enforced today at
   `abandon.go:204-218`) **and** refuse a result transition after abandonment
   (enforced nowhere). Removing `proving → abandoned` closes the first direction
   in the table as well as in the guard.

**Replay / idempotency.** Uniform rule: admitting a transition already durably
recorded, with an identical payload digest, is a **replay** and succeeds without
appending. A different payload for the same logical transition is a **conflict**
and is refused. The gateway performs this check *before* the transition-legality
check, since a replayed terminal necessarily reads as an illegal
terminal→terminal move — the ordering defect repaired in round three of #350 and
one the gateway must own so no producer rediscovers it. Replay is distinct from
**same-phase** (§4.1.4): a replay appends nothing, a same-phase event appends a
durable record that asserts no move.

#### 4.1.6 Consequence recorded, not repaired: `proving` has no terminal

*(Round 3: this consequence is real but is stated in the task-lifecycle frame.
Under §4.0.1 the question becomes which lifecycle owns "a result was produced and
will not be certified" — plausibly the result lifecycle, where `certification`
already has `CertificationBlocked`, `CertificationUncertifiable` and
`CertificationStale` verdicts that no task phase mirrors. Re-open it there.)*

Removing `proving → abandoned` is correct and leaves a real gap that this packet
does not close. A task at `proving` whose result can never be certified now has
no terminal phase at all: abandonment is refused because a result exists,
`certified` is not appended on a non-certifying verdict, and `completed` requires
certification. Such a task remains at `proving` indefinitely.

That gap is honest — the system genuinely has no vocabulary for "a result was
produced and will not be certified" — and inventing one here would be exactly the
speculative machinery the owner refused. It is recorded as **C-1**, an input to
Unit 5 (abandonment rebuild), not as a Unit 1 obligation.

### 4.2 Derived assessments — CANDIDATE (8) *(approval withdrawn)*

**Round 3: withdrawn, and its reasoning partly wrong.** The "Fails clause"
column below attributes `(a) — nothing causes it` to `ready_for_admission` and
the four `waiting_*` states. §0.1 and §3 correct that: `waiting_mechanical_repair`
is caused by a durable `ScopeVerification` with a non-verified outcome
(`governance.go:134-136`) and `ready_for_admission` by a durable
`authority_resolved` with no subsequent decision (`governance.go:139-142`). They
are deterministic reconstructions from durable causes.

That does not automatically make them phases — a reconstruction still has to
answer §4.0's four questions, and "which write boundary enforces its
consequences" is the one they are least likely to survive. But *"nothing causes
it"* is not the reason, and the classification must be redone on the real one.

**Superseded ruling, retained for the record — 2026-09-10 (round 2):** All eight are derived assessments:
`ready_for_admission`, `waiting_architect`, `waiting_evidence`,
`waiting_governance`, `waiting_mechanical_repair`, `refused`, `stale`,
`uncertifiable`. Each recomputable and reportable; none a transition; none
terminal; none admissible through the gateway (§6.6, D6).

| Assessment | Derived from | Fails clause | Today |
|---|---|---|---|
| `ready_for_admission` | authority resolved, no `admission_decided` yet | (a) — nothing causes it | `governance.go:142` |
| `waiting_architect` | open architect question | (a), (c) | status under `scope_verified` |
| `waiting_evidence` | outstanding evidence requirement | (a), (c) | status |
| `waiting_governance` | governance record unreadable or undecided | (a), (c) | `session.go:572` fail-closed default |
| `waiting_mechanical_repair` | `scope_verification` present and not verified | (a), (c) | `governance.go:136` |
| `refused` | `admission_decided` that does not bind **at time `now`** | (b) — time-varying | `governance.go:150` |
| `stale` | `verifySession` against the **live working tree and pointer** | (b) — world-varying | `session.go:565` |
| `uncertifiable` | admission decision or convergence status | (b) — new evidence reverses it | `session.go:1543` |

### 4.3 The five-lifecycle authority mapping (complete)

Every claim below carries the owner's eight fields. **Kind** is *historical* (a
fact about something that happened), *re-evaluated* (recomputed against the world
each time it is asked) or *projected* (a cached derivation of other records).
Source citations are at `42475333`; method in §11.4.

Fields per row: **subject · predicate · established by · writer and invocation
path · reader · enforcement boundary · kind · identity conjunction.**

#### L1 — Sensei Code workflow *(local orchestration)*

| | |
|---|---|
| **Subject** | one local Sensei Code run and the candidate it produces |
| **Predicate** | "this run reached state X" — started, review completed, validation run, workflow completed/observed/stopped/failed/awaiting-authority |
| **Established by** | `internal/event` stream kinds (`internal/event/event.go`) |
| **Writer / path** | the sensei-code broker and router, over the local event stream |
| **Reader** | the sensei-code control plane |
| **Enforcement boundary** | local only. `workflow.completed` grants nothing in Sensei; it asserts a change was admitted (`event.go:124-127`) |
| **Kind** | historical |
| **Identity** | run id, candidate id. **Carries no Sensei task id, session, base revision or graph digest** |

**Gap G1 — a missing bridge *contract*, not yet a defect** (owner, round 5). A
Sensei Code run may legitimately terminate without closing the governed task: an
audit that admits nothing has succeeded, which is exactly why `workflow.observed`
exists. Nothing checks that an L1 terminal agrees with an L2 one, and **nothing
states that it should**. It becomes a defect only if the contract requires
propagation and no owner performs it. Writing that contract — or recording that
none is required — is the work; a test asserting agreement would be premature.

#### L2 — Task *(durable disposition and history)*

| | |
|---|---|
| **Subject** | a Sensei task |
| **Predicate** | "this task was prepared / advanced / certified / completed / abandoned" |
| **Established by** | ledger events `task_prepared`, `convergence_advanced`, `closure_assessed`, `task_control_projected`, `certified`, `completed`, `abandoned` — each with a content-addressed payload artifact |
| **Writer / path** | `tasksession` (`session.go:946`, `control.go:853`) via `sensei prepare-change` / `advance-task`; `certification/ledger.go:151`; `completion/complete.go:227`; `completion/abandon.go:350` |
| **Reader** | ledger projection (`ledger/projection.go`), `completion/load.go`, `InspectTerminalState` |
| **Enforcement boundary** | `abandon.go:204-218` (a result transition forbids abandonment); `certification/ledger.go:131-135` (verdict gates the append) |
| **Kind** | historical |
| **Identity** | `task.id` + `task.session_id` + the hash-linked entry chain (`previous_entry_digest_sha256`) |

**Measured:** only the first four of these events exist in the corpus; the four
terminal writers have never run in either repository (§5.5.5).

#### L3 — Authorized operation *(admission, consumption, scope)*

| | |
|---|---|
| **Subject** | one authorized change operation within a task |
| **Predicate** | "authority is resolved / a decision was made / the capability was consumed / the change was observed / its scope was verified" |
| **Established by** | typed receipts bound to ledger entries: `authority_resolution`, `admission_decision`, `capability_consumption`, `observed_change_set`, `scope_verification` |
| **Writer / path** | `admission.RecordAuthorityResolved/AdmissionDecided/AdmissionConsumed/ChangeObserved/ScopeVerified` — **sole non-test caller `cmd/awg/cmd_admission_v2.go`**. A *second, older* writer produces the same predicate outside the chain: `admission.WriteCanonicalDecision` from `cmd/awg/cmd_admit_change.go:66` (`sensei admit-change --output`) writing `<task>/admission/decision.yaml`, and `control.go:292` writing `<generation>/admission-decision.yaml` |
| **Reader** | `foldGovernance` (`governance.go:100-175`) — chain only, no filesystem fallback. **Plus** `projectControlStatusAndClosure` (`control.go:611-624`) and `BuildTaskBriefing` (`briefing.go:106-113`), which read the *file* decision directly |
| **Enforcement boundary** | `control.go:316-336` (AdvanceTask: governed grant, fails closed); `session.go:569-576` (Status: governed, fails closed); `advance_result.go:170-183` (entry to L4). **Not** `control.go:575-577`, which serves a cached projection ungoverned |
| **Kind** | **re-evaluated** — `recordedDecisionBinds(dec, rec, now)` takes a clock; a decision that binds today may not tomorrow |
| **Identity** | `binding{repository_domain, revision, graph_digest_sha256}` + `request_receipt.digest_sha256` + `admission_id` + `session_receipt.session_id` (a *convergence* session id, not the task session id). **The decision record carries no `task_id`** |

**Gap G2 (identity).** Because the decision carries no task id, its binding to a
task is positional — which directory it sits in. Measured: 11 binding triples are
shared by more than one task, covering 42 of the 59 file decisions (§5.5.8). The
binding triple alone therefore cannot identify a task, and directory position is
doing work that no field records.

**Gap G3 (two authorities, one predicate).** Two writers establish "mutation is
permitted": the chain receipts and the decision file. §5.5.8 is the measurement.

#### L4 — Result *(binding, evidence, proof, certification)*

| | |
|---|---|
| **Subject** | one result produced under one authorized operation |
| **Predicate** | "a result was bound / evidence was observed / an obligation was discharged / the result is certified" |
| **Established by** | `result_transition_recorded` + `ResultBinding`; `EvidenceReceipt`; `ProofDischarge`; `CertificationReceipt` |
| **Writer / path** | `resultrecording.RecordTransition` (`record.go:70`); `certification/ledger.go:151`. **Evidence and proof have no production writer**: `runtimeprobe.ToEvidenceReceipt` (`receipt.go:15`) has no caller outside its package and `proofdischarge.Discharge` (`discharge.go:20`) has **zero callers except its own tests** |
| **Reader** | `certification/source.go:151-185` resolves both kinds **by digest** from the task directory; `certification/lanes.go` evaluates the evidence and proof lanes; `request.go:113-129` verifies the declared digest sets |
| **Enforcement boundary** | `certification/ledger.go:131-135` — a non-certifying verdict appends nothing |
| **Kind** | historical, with a re-evaluated freshness overlay (`ReasonProofDischargeStale`, `errors.go:64`) |
| **Identity** | `ResultBinding{base_revision, patch_digest, result_tree_digest, graph_digest}` + receipt digests named in the certification request |

**Gap G4 — two gaps, deliberately separated** (owner, round 5). Round 4 stated
this as one "unwired lifecycle," which hides a distinction that matters:

- **G4a — missing production invocation.** The construction code exists and is
  correct enough to pass its own tests: `runtimeprobe.ToEvidenceReceipt`
  (`receipt.go:15`) has no caller outside its package; `proofdischarge.Discharge`
  (`discharge.go:20`) has **zero callers except `discharge_test.go`**.
  Certification consumes artifacts no production code here produces.
- **G4b — missing task-ledger integration.** `evidence_recorded` and
  `proof_discharged` have no emitter, no consumer and no persisted instance
  (§5.5.1).

**Either can be closed without the other.** A production invocation could write
receipts into the task directory that certification already resolves by digest,
with no ledger event at all — that is how certification works today. Conversely
the events could be emitted for artifacts an external process supplies now.
Treating them as one gap would force a design decision the measurement does not
require.

#### L5 — Current validity *(re-evaluated)*

| | |
|---|---|
| **Subject** | the relationship between a stored record and the world right now |
| **Predicate** | "this task's binding is stale / this receipt is invalid, stale, conflicted, superseded or revoked / certification is uncertifiable on present evidence" |
| **Established by** | nothing durable, deliberately — live recomputation, plus `status` fields on receipts |
| **Writer / path** | none. `verifySession` computes staleness against the live worktree and active pointer |
| **Reader** | `session.go:562-567` (Status), `control.go:625-628` (verifyErrors → refuse) |
| **Enforcement boundary** | `session.go:563-567` (stale ⇒ `PhaseStale`, next action "prepare a new task"); `control.go:625-628` (verify errors ⇒ inspect `uncertifiable`, modify `refused`) |
| **Kind** | **re-evaluated** — by construction, and the reason §5.1–5.3 refused to make these terminal phases |
| **Identity** | task binding vs live worktree and pointer, **at the moment of asking**; no durable identity, and none should be manufactured |

**Consistent with the ruling.** L5 is why `stale`, `refused` and `uncertifiable`
must not be irreversible task phases: they are L5 claims, and §5.1–5.3 remain
correct for that reason rather than the one round 1 gave.

#### 4.3.1 What the mapping establishes

Answering the four questions for all five lifecycles yields exactly four gaps,
and **none of them is a missing phase**:

| Gap | Kind | Concrete |
|---|---|---|
| **G1** | missing bridge **contract** | L1 terminal vs L2 terminal — no contract states whether propagation is required, so no owner can be missing it |
| **G2** | identity insufficiency | L3 decision records carry no task id; 42 of 59 share a binding triple with another task |
| **G3** | conflicting authority | two writers establish L3's mutation predicate; one reader path consumes the ungoverned one (§5.5.8) |
| **G4a** | missing production invocation | evidence/proof construction exists but nothing production-reachable calls it |
| **G4b** | missing ledger integration | no task-ledger integration for `evidence_recorded` / `proof_discharged` |

G3, G4a and G4b meet the owner's bar for an implementation proposal — *a concrete
missing writer, missing reader connection, or conflicting authority*. G1 needs a
contract written before it can be called a defect at all; G2 needs its own
measurement. **No proposal is made here**, per
the standing instruction; §12 records what each would require.

### 4.4 Unit 1's likely design rule — single predicate authority

Recorded from the owner, 2026-09-10 (round 5), after the §5.5.9 observation. Not
yet a proposal; the rule the eventual proposal is expected to implement.

> **Mutation permission may be produced only from one verified ledger snapshot and
> its content-addressed artifacts. Direct files and cached projections may explain
> or display history, but may never independently grant permission.**

**What it resolves.** G3, without requiring every reader to share presentation
code. Three surfaces already consult `foldGovernance`
(`control.go:316`, `session.go:571`, `advance_result.go:170`); one does not
(`projectControlStatusAndClosure`, `control.go:553-647`). The rule does not ask
them to look alike — it says the *predicate* has one producer, and everything else
displays its result.

**Why it is the right shape.** It centralizes **predicate authority**, not views.
Multiple readers deriving state from one receipt is legitimate and already
happens; multiple authorities establishing the same predicate is the conflict
(§7's `scope_verified` question turns on exactly this distinction).

**What it makes of the existing artifacts.** Nothing is deleted. The 124 decision
files keep explaining what was decided and when; the seven `control/latest.yaml`
projections keep displaying a past permission state. Neither may be the thing a
caller acts on. The rule converts them from latent authorities into history —
which is what they already are in three of four surfaces.

**What it does not settle**, and must not be assumed to:

- Whether the cached projection is repaired by re-consulting governance on read,
  by refusing to serve a cached permission at all, or by not persisting one.
- Whether `resultrecording/classify.go:92` establishes or projects
  `scope_verified` (§7) — the same establishes-vs-projects question, unresolved.
- Anything about G1, G2, G4a or G4b, which the rule does not reach.

### 4.5 The operation-state decision table — replacing `permission.modify`

Owner-specified, 2026-09-10 (round 7). **Design record only; not implemented.**

`permission.modify` is a single ambiguous predicate carrying three presentation
strings (`admitted`, `waiting`, `refused`) that consumers read as authorization.
It answers no specific question: "may I consume the capability?", "may I apply the
bound operation?" and "may I verify scope?" are different questions with
different ledger preconditions, and one string cannot distinguish them. §5.5.10's
F2 disagreement is exactly this ambiguity — see §4.5.4.

#### 4.5.1 The table

Derived **only** from the verified ledger and its content-addressed artifacts:

| Ledger condition | Legal next action |
|---|---|
| No binding typed decision | None; resolve admission |
| Valid decision, capability unconsumed | Consume the capability |
| Capability consumed, no change observed | Apply exactly the bound operation |
| Change observed, scope not verified | Verify scope |
| Scope verified | No further mutation |
| Invalid, expired, stale or conflicting authority | None |

#### 4.5.2 The replacement for `GrantModify`

`GrantModify` is one boolean standing for four distinct authorizations. It is
replaced by four, each with a **closed type** and **one ledger-derived owner**:

| Capability | True exactly when | Owner |
|---|---|---|
| `CanConsumeCapability` | a valid typed decision binds and no `admission_consumed` exists | the ledger reducer |
| `CanApplyBoundOperation` | the capability is consumed and no `change_observed` exists | the ledger reducer |
| `CanObserveChange` | the bound operation has been applied and no `change_observed` exists | the ledger reducer |
| `CanVerifyScope` | `change_observed` exists and no `scope_verification` does | the ledger reducer |

These may be fields of one derived operation disposition, but each must be
separately typed and separately answerable. Two constraints follow:

1. **One ledger-derived owner.** Every field is computed by the reducer over one
   verified chain snapshot. No file, cache or projection may set one (§4.4).
2. **Presentation strings are not authorization.** `admitted`, `waiting` and
   `refused` may be displayed; no consumer may branch on them to permit an
   action. Today three do — §4.5.5.

`CanApplyBoundOperation` and `CanObserveChange` are separated deliberately: the
first authorizes a mutation that has not happened, the second records one that
has. Collapsing them would let a reader that has observed a change conclude it
may make another.

#### 4.5.3 What the table makes visible that `modify` hides

The row **"capability consumed, no change observed → apply exactly the bound
operation"** has no representation in the current vocabulary at all. After
consumption, `foldGovernance` returns `GrantModify=false` (§5.5.10 F3) — correct
for "may I consume?", wrong as an answer to "may I apply what I consumed?". The
one predicate cannot express the state in which application is *precisely* what
is authorized.

#### 4.5.4 Fixture reclassification (owner, round 7)

| Fixture | Round-6 label | Corrected |
|---|---|---|
| **F1** | over-report | **proves over-authorization.** Governance authorizes nothing; a public surface reports `modify=admitted`; an execution boundary accepts it (§5.5.11) |
| **F2** | under-report | **proves disagreement.** Whether it is *under*-reporting depends on whether `modify` means "consume" or "apply" — the ledger state (decision recorded, capability unconsumed) authorizes **consumption**, not application. Under `CanConsumeCapability` the public `waiting` is wrong; under `CanApplyBoundOperation` it is right. **The ambiguity is the finding**, not the direction |
| **F3** | agree | **does not test consumption enforcement.** Its file decision never granted mutation, so the ungoverned paths had no grant to leak. Agreement here is not evidence that consumption is honoured |

F2 is therefore the strongest argument for the table: the same observation is a
defect or correct behaviour depending on a question the current type cannot ask.

#### 4.5.5 Additional G3 finding — `validateCurrentBinding` accepts `waiting`

`cmd/awg/cmd_synthesis_run.go:1056-1064` refuses only the exact string
`refused`:

```go
if control.Permission.Modify == admission.CapabilityRefused { return ... }
```

So `waiting` — and any future or misspelled value — **passes**. This is a
separate defect from reading the ungoverned source: even given a correct
`permission.modify`, the check admits a state that authorizes nothing. A guard
that enumerates the one value it rejects fails open on everything else; reading a
closed vocabulary by exclusion is the recurring shape here.

**Its replacement must require the exact positive action capability appropriate to
the command** — for a command that creates a candidate, that is whichever of the
four §4.5.2 capabilities the command actually needs, asserted true, not "not
refused".

## 5. Forced decisions

Leaving a state declared but unreachable is no longer an option. Each decision
below is binary and must be ruled on before implementation.

### 5.1 `refused` — RECOMMEND: derived assessment; remove from the table

`governanceDisposition` computes it as `!recordedDecisionBinds(dec, rec, now)`.
The `now` parameter is decisive: a decision that binds today may not bind
tomorrow, and one that does not bind now may bind after the base is re-resolved.
The state is therefore **reversible**, and `AllowedTaskTransitions` currently
declares `PhaseRefused: {}` — no outgoing edges, i.e. irreversible.

That is a false assertion of irreversibility, and it is the more dangerous
direction: it would let a governance opinion permanently close a task if the
guard ever became effective.

*Alternative, if rejected:* make refusal a durable authority act with its own
producer and receipt, pinning the decision and base it refused against, at which
point it is stable by construction. This is more work and asserts more than the
system currently means.

### 5.2 `stale` — RECOMMEND: derived assessment; remove from the table. Keep the event as an assessment record.

`res.Phase = PhaseStale` is set when `verifySession` fails against the live
working tree and active pointer. It is a statement about the **relationship
between a task and the current world**, not about anything that happened to the
task. Restoring the tree makes it verify again.

Two consequences argue for removal beyond the classification rule:

1. A stale task must remain **abandonable**. Today `PhaseStale: {}` would forbid
   exactly that — the single most useful action on a stale task. That the guard
   is inert (A-1) is the only reason this has not surfaced.
2. Staleness is what abandonment most often *responds to*. Making it terminal
   inverts the relationship.

`LedgerEventTaskMarkedStale` (today producerless) should be **retained** as the
durable record of *a staleness assessment made at a moment* — who looked, when,
against what base, what they found. Recording that an assessment occurred is not
the same as entering a phase, and the distinction is the point.

*Alternative, if rejected:* delete `LedgerEventTaskMarkedStale` from the event
vocabulary as well, and let staleness be purely ephemeral.

### 5.3 `uncertifiable` — RECOMMEND: derived assessment; remove from the table

Derived from `decision.Decision == DecisionUncertifiable || conv.Status ==
StatusUncertifiable`. Both inputs are durable, so it satisfies (a) and arguably
(c) — but it fails (b) in the sense that matters: "certification cannot be
reached **on the present evidence**" is a claim that new evidence reverses. The
table's `PhaseUncertifiable: {}` asserts the opposite.

This is the closest call of the three. It satisfies the letter of stability
(recomputing over the *same* chain gives the same answer) while failing its
intent (the chain grows, and the answer changes). The recommendation follows the
intent, and this is flagged as the decision most worth the owner overruling.

*Alternative, if rejected:* keep it durable and terminal, and accept that
recording it forecloses a task that later evidence could have certified.

### 5.4 `revoked` — RECOMMEND: durable fact; build the producer (or delete the state)

Not one of the three named, but the rule reaches it and the same prohibition
applies. Revocation is an **authority act** — someone with standing withdraws a
completed or certified result. It cannot be derived from anything; nothing else in
the record implies it. It satisfies (a) in principle, (b) absolutely (an act is
stable), and (c) — `complete.go:401` and `load.go:222` already branch on it.

Yet no code can produce it, so `PhaseCertified: {PhaseCompleted, PhaseRevoked}`
and `PhaseCompleted: {PhaseRevoked}` are dead edges, and `completed` is in
practice absolutely terminal while the table says otherwise.

Two honest options, no third:

- **Build the producer** — a revocation operation with authority resolution and a
  receipt, structurally parallel to abandonment. Larger than Unit 1.
- **Delete `revoked`** from the phase and event vocabularies, delete its two
  consumer branches, and declare `completed` absolutely terminal.

**HELD 2026-09-10 (round 3) — no writer until the subject is explicit.** The
owner's instruction: *do not create a writer for an event whose meaning could be
task revocation, result revocation, evidence revocation, or capability
revocation.* Four candidate subjects, and the vocabulary supports all four:

| Candidate subject | What already exists | Lifecycle |
|---|---|---|
| task revocation | `PhaseRevoked`; consumers at `complete.go:401`, `load.go:222` | L2 |
| result revocation | `CertificationRevoked` verdict (`vocabulary.go`) | L4 |
| evidence/receipt revocation | `ReceiptRevoked` status; `closureprotocol.RevocationReceipt` (`model.go:481`) with `revoked_target_id` | L5 |
| capability revocation | withdrawal of an unconsumed admission grant — no representation | L3 |

`RevocationReceipt.RevokedTargetID` is deliberately generic, so the receipt type
cannot settle the question either. **Building a producer now would pick one
meaning by accident.** The §5.4 ruling that the phase is not deleted stands; the
producer is held.

**Superseded in part by §4.1.3.** The observation above — that `completed` is
"in practice absolutely terminal while the table says otherwise" — was correct
about the symptom and wrong about the cure. The cure is not to pick one of the
two; it is that `completed` is **closed** rather than absorbing, and the
`completed → revoked` edge is `governed`, not ordinary.

**OWNER RULING 2026-09-10 — DECIDED: `revoked` remains a durable terminal
phase.** It represents an authority act, cannot be derived, and existing readers
already depend on it. **Unit 1 must provide its governed producer.** Deleting it
would erase intended semantics rather than repair reachability. The "delete the
state" alternative above is closed.

### 5.5 Measured integration state of `evidence_recorded`, `proof_discharged`, `migration_executed`, `task_marked_stale`

Read-only measurement performed 2026-09-10 at `42475333` on a fresh verified
clone, plus a census of every persisted ledger in both working repositories.
Method in §11.2.

**Restated in round 3.** Round 2 applied the rule *"an event with no producer, no
consumer and no stored compatibility obligation leaves the executable
vocabulary"* and concluded "delete." Two corrections:

1. **The precise finding is missing built-in event integration, not a missing
   algorithm.** For evidence and proof the construction code exists and is
   reachable by its own tests — `proofdischarge.Discharge`
   (`discharge.go:20`) constructs discharge records, `runtimeprobe.ToEvidenceReceipt`
   (`receipt.go:15`) constructs evidence receipts — and certification consumes
   both kinds of artifact. What is absent is the wiring between them and the
   ledger. "Delete the event" and "connect the event" are both live options and
   the measurement does not choose between them.
2. **Source absence of a producer is not proof no external caller has ever
   written such an event.** The ledger `Append` API is exported. The corpus
   census (§5.5.5) shows none in *these two repositories*; it is not evidence
   about ledgers elsewhere.

The dispositions below are therefore stated as **findings with options**, and the
recommendation column is advisory. Each option must still answer §4.0's four
questions before it is implementable.

#### 5.5.1 Summary

| Event | Producer | Consumer | Payload schema | Tests | Persisted instances | Disposition |
|---|---|---|---|---|---|---|
| `evidence_recorded` | no built-in emitter. **Constructor exists**: `runtimeprobe.ToEvidenceReceipt` (`receipt.go:15`), no caller outside its package | certification consumes evidence *artifacts*; nothing consumes the *event* | `evidence-receipt.schema.json` + `closureprotocol.EvidenceReceipt` | none reference the event | **0** | integration gap — connect, or remove the event and keep the record |
| `proof_discharged` | no built-in emitter. **Algorithm exists**: `proofdischarge.Discharge` (`discharge.go:20`), **zero callers anywhere but its own tests** | certification consumes discharge *artifacts*; nothing consumes the *event* | `proof-discharge.schema.json` + `closureprotocol.ProofDischarge` | none reference the event | **0** | integration gap — the deeper one: the algorithm itself is unreached |
| `migration_executed` | no built-in emitter | none | `migration-execution-receipt.schema.json` + `closureprotocol.MigrationExecutionReceipt` | 1, vacuous (§5.5.3) | **0** | remove, or repurpose as the §7.2 era event — owner's call |
| `task_marked_stale` | no built-in emitter | no type-specific consumer; one documented obligation | **none** — no schema of its own | 1, test-only producer that the round-2 ruling invalidates | **0** | **staleness is already computed live** (§5.5.4); an emitter is not needed to *report* it, only to record a historical assessment — a separate feature |
| `revoked` | no built-in task-revocation writer | completion readers recognise task revocation (`complete.go:401`, `load.go:222`) | `revocation-receipt.schema.json` + `closureprotocol.RevocationReceipt` | — | **0** | integration gap; note receipt revocation is a **different subject** from task revocation (§5.4) |

In each of the four Go identifiers, every reference in the tree is either the
declaration in `closureprotocol/vocabulary.go` or its entry in the
`LedgerEventTypes` list — with the two test exceptions noted below. None is
constructed by non-test code anywhere.

#### 5.5.2 `evidence_recorded` and `proof_discharged` — the record is live, the event is not

This is the distinction the previous round missed. The *record kinds* are real,
typed, validated and load-bearing:

- `closureprotocol.EvidenceReceipt` (`model.go:219`) and
  `closureprotocol.ProofDischarge` (`model.go:241`) are consumed by certification.
  `certification/source.go:151-185` resolves both from the task directory **by
  digest**, `certification/request.go:113-129` verifies the request's declared
  digest sets against what was resolved, and `certification/lanes.go` evaluates
  the proof and evidence lanes over them. `certification/errors.go:63-64` carries
  dedicated refusal reasons (`proof.discharge_invalid`, `proof.discharge_stale`).
- Both have frozen JSON schemas and producer-side validators
  (`golang/architecture/evidencereceipt/`, `golang/architecture/proofdischarge/`).

What has no producer, no consumer and no instance is the *ledger event that would
announce them*. Certification does not learn of evidence from the chain; it is
handed digests in the certification request and resolves them from disk. The
event type is a name for a message nobody sends and nobody reads.

**Round-3 correction to this subsection.** The paragraph above says certification
"does not learn of evidence from the chain," which is right, and round 2 inferred
"so the event is useless," which does not follow. Two further source facts change
the picture:

- `proofdischarge.Discharge` (`discharge.go:20`) — the function that *computes*
  discharges — has **zero callers anywhere in the tree except its own tests**. So
  the gap is not only "no event announces the discharge"; nothing in production
  computes one. Certification consumes discharge artifacts that some external
  process must supply.
- `runtimeprobe.ToEvidenceReceipt` (`receipt.go:15`) is likewise uncalled outside
  its package.

That is a materially different finding from "a name for a message nobody sends."
The proof and evidence lifecycle (§4.0.1, object 4) is **built but not wired**,
and deleting its ledger events would remove the most visible marker of that.

**Options, neither recommended without the §4.0 mapping:**

(a) *Connect.* Give the evidence/proof lifecycle its producer path and let the
events announce it, with a named reader. This answers §4.0 questions 2–4 for an
object that currently answers only 1.

(b) *Remove the events, keep the records.* Certification is unaffected. Cheaper,
and it makes the unwired lifecycle less visible rather than more — which is the
argument against it now that the wiring gap is the actual finding.

Round 2 recommended (b) on the strength of "no producer, no consumer." With the
constructors' existence and their zero call counts on the table, **this packet
makes no recommendation** and refers the choice to the §4.0 mapping for object 4.

**What this costs, stated plainly.** It gives up the ability to know *from the
chain alone* which evidence a certification rested on. Today that is already
given up — the binding lives in the certification request and the
`certification_receipt` artifact the `certified` event already carries
(`certification/ledger.go:165`). The event would be a second, redundant
binding, and building it now to serve no reader is the machinery the owner
refused. If a future unit needs chain-derivable evidence provenance, it should
add the event *with* its reader, as one change.

**Alternative, if rejected:** build both events as advancing-nothing annotations
(§4.1.4, non-phase-bearing) appended by certification at the moment it resolves
the records, and give the reader that needs them a name. This is coherent but is
new machinery for a need nothing currently expresses.

#### 5.5.3 `migration_executed` — nothing consumes it, and its one test cannot fail

`closureprotocol.MigrationExecutionReceipt` (`model.go:493`) has a validator
(`validate.go:501`), a canonicalization test, a fixture slot, a JSON schema and
an ontology class (`ontology/awareness.ttl:388`). It has **no producer and no
consumer** anywhere in the tree — the RDF drift test
(`golang/rdf/closure_protocol_drift_test.go:60`) asserts only that the ontology
still declares the class, which is a vocabulary-parity check, not a consumer.

Its single test reference is a **vacuous negative control**:

```go
// questiondisposition/matrix_test.go:249-262
// TestBoundaryProofNoLaterPhaseOrGovernedMutation
for _, forbidden := range []string{
    "certified", "completed", "revoked", "migration_executed",
} {
    if hasEventType(t, env.TaskDir, forbidden) { t.Fatalf(...) }
}
```

The assertion is that a question disposition does not produce a
`migration_executed` event. Nothing in the system can produce one, so this
assertion could not fail under any mutation of the code it is guarding. It
creates confidence rather than raising an alarm. (`certified`, `completed` and
`revoked` in the same list are not equally vacuous — the first two have real
producers.)

**Recommendation (unchanged by round 3, and the weakest-attached of the four):
remove `migration_executed` from `LedgerEventTypes`** and drop it from that
test's forbidden list, *unless* the owner adopts option (b) of §7.2,
in which case it is repurposed as the durable era-adoption event and acquires
both a producer and a reader in M1. The two options are mutually exclusive and
§7.2 is where the choice belongs; this section only records that on its current
semantics the event is unreachable and unread.

The receipt type, validator and ontology class are untouched either way — they
describe architectural migration plan steps, which is a separate concern from the
ledger event.

#### 5.5.4 `task_marked_stale` — current staleness needs no emitter; a historical record is a separate feature

**Round-3 correction, and it changes the conclusion.** The owner's audit
establishes that **current staleness is already computed during task
verification**. Nothing needs to be emitted for the system to *report* that a
task is stale — `verifySession` determines it live, against the working tree and
the active pointer, every time it is asked (which is also why §5.2 correctly
classified it as world-varying).

So the round-2 framing was wrong at its root. It asked "how do we make this event
producible" when the prior question is what the event would be *for*. There are
two distinct things, and only one of them is missing:

| Claim | Object | Established by | Reader | Enforcing boundary | Status |
|---|---|---|---|---|---|
| "this task is stale **now**" | task ↔ current world | live `verifySession` recomputation | task status reporting | none — it is a report | **already works; needs nothing** |
| "a staleness assessment was made at time T against base B, finding F" | a historical assessment | would need `task_marked_stale` + a payload schema | **none exists** | none identified | **a separate feature nobody has asked for** |

The round-2 specification below (producer at `session.go:565`, new
`staleness-assessment` schema, M6 reader) is a design for the second row. It
answers §4.0's questions 1 and 2 and **fails questions 3 and 4**: no reader needs
the history, and no write boundary enforces anything on it. By §4.0 it is not
implementable, and building it would be the speculative machinery the owner
refused two rounds ago — arrived at, this time, by trying to satisfy a ruling.

**Revised disposition: neither build the producer nor treat the event as
load-bearing.** Leave the event declared and unemitted *only if* the owner wants
the historical-assessment feature on the roadmap; otherwise remove it. The
round-2 claim that "retaining it producerless would reproduce A-1 inside the
repair" was overstated — A-1 was a *guard* that silently failed open over a
missing phase, not a declared-and-unused name.

Measurement of what exists today, retained:

- **Producer:** none.
- **Consumer:** no type-specific consumer. `completion/abandon.go:481`
  (`latestTaskPhase`) reads the `task_phase` of *any* entry, so it would consume
  one generically. Beyond that it carries a **documented compatibility
  obligation**: the comment at `abandon.go:469-479` names `task_marked_stale`
  explicitly as the case that motivated the "an unreadable entry is an error,
  never a skip" rule.
- **Payload schema:** none of its own. Unlike the other three it has no receipt
  schema; there is no declared shape for what a staleness assessment records.
- **Test:** exactly one, and the ruling invalidates it. `abandon_test.go:844-872`
  defines a helper `appendPhase` that uses `LedgerEventTaskMarkedStale` as a
  *generic carrier* to fold a task to an arbitrary phase, and
  `TestAbandonmentIsRefusedFromAnAlreadyTerminalPhase` (R3-F2, `:875`) calls it with
  `refused`, `stale` and `uncertifiable` — the three phases now ruled derived
  assessments. Under §6.6 and D6 the gateway must **refuse** exactly what this
  helper appends. The test becomes inexpressible as written and must be rewritten
  against the approved terminals or retired. This is a required M2 work item and
  is listed as such in §7.1.
- **Persisted instances:** 0.

**Required producer, specified.** For the event to be reachable it needs the
three things the record is supposed to carry, which the ruling already named —
who looked, when, against what base, and what they found:

| Element | Requirement |
|---|---|
| Producer | `tasksession.verifySession`, at the point it concludes the session does not verify (`session.go:565`) — the site that already computes the assessment |
| Disposition | **non-phase-bearing** (§4.1.4). Writes no `task_phase`. It records that an assessment occurred; it does not move the task |
| Payload | a new `staleness-assessment` record: assessor identity, `observed_at`, the `base_binding` assessed against, the working-tree/pointer facts found, and the resulting assessment |
| Schema | a new `staleness-assessment.schema.json`, since none exists |
| Reader | the derived-assessment reporter of M6, which reports `stale` from the *live* recomputation and cites the last durable assessment record as provenance |

The distinction the ruling turns on is preserved structurally: the assessment's
*occurrence* is durable, its *conclusion* is not the task's phase.

**Round 3 supersedes the paragraph above.** The specification is retained as *what
the historical-assessment feature would require if it is ever wanted*, not as a
Unit 1 obligation. The forced choice in §12 is restated accordingly: put the
feature on the roadmap, or remove the event — not "build this producer now."

#### 5.5.5 The persisted corpus — what any migration will actually meet

Census of every ledger in both working repositories (`globulario/sensei` and
`globulario/sensei-code`), 2026-09-10:

- **25 ledgers, 187 entries.**
- **Exactly four event types occur, ever:**

| Event | Count | Note |
|---|---|---|
| `task_prepared` | 25 | exactly one per ledger, always sequence 1 |
| `convergence_advanced` | 54 | up to 9 in one chain |
| `closure_assessed` | 54 | |
| `task_control_projected` | 54 | |

- **Zero occurrences** of the other 16 event types — including all four measured
  here, and including every event that writes a phase.
- **`task_phase` appears in zero persisted payloads.** Not once, anywhere, in
  either repository.
- 59 admission decisions are persisted at `<task>/admission/decision.yaml`
  (§4.1.5) and **not one** produced a ledger event: the only non-test caller of
  `admission.RecordAdmissionDecided` is `cmd/awg/cmd_admission_v2.go`, a path the
  live corpus shows was never taken.
- Zero persisted `EvidenceReceipt`, `ProofDischarge`, `MigrationExecutionReceipt`
  or `RevocationReceipt` records. The only files under any `receipts/` directory
  are `prepare-change.yaml` and `task-status.yaml`.

Two consequences bear directly on the rest of this packet:

1. §2.1 named five *producible* phases. The corpus shows **zero produced**. The
   real chain shape is `task_prepared → (convergence_advanced → closure_assessed
   → task_control_projected)*`, and it carries no lifecycle phase at any point.
2. This makes M3's compatibility question sharper, not milder. Every existing
   chain lacks `prepared` — not because an old binary wrote a different phase,
   but because no binary has ever written any phase at all. §7.2 is written
   against that fact.

#### 5.5.6 Vocabulary outcome

| | Before | After (if §5.5 accepted) |
|---|---|---|
| Phases declared | 18 | 10 durable + 8 derived assessments |
| Event types declared | 20 | 17 — minus `evidence_recorded`, `proof_discharged`, and `migration_executed` *(the last retained only under §7.2 option (b))* |
| Declared-but-unproducible events | 5 | 0 — `revoked` gets its producer (§5.4), `task_marked_stale` gets its producer (§5.5.4), the rest are deleted |

#### 5.5.7 Collateral finding — the frozen schema's event enum is already 3 short

Not a Unit 1 obligation; recorded because any change to `LedgerEventTypes` must
not make it worse.

`docs/schemas/architectural-closure/v1/common.schema.json` declares
`ledger_event_type` with **17** members. Go's `LedgerEventTypes` has **20**. The
schema is missing `result_transition_recorded`, `question_disposition_recorded`
and `abandoned` — the three most recently added, and among them the sole
phase-advancing event of the result path and the terminal PR #350 was built
around. A validator reading the frozen schema would reject entries the running
system writes.

Two closed vocabularies for the same set, maintained separately, already
disagreeing. Whichever way §5.5 is ruled, the two lists must be reconciled by a
generated-or-tested parity check, not by editing both by hand. Recorded as
**C-2**, an input to the same test obligation that D3 covers.

#### 5.5.8 C-3 classified — two authorities for one predicate, not an unread reader

**Round 3 stated C-3 as "a correct reader with nothing to read." That was wrong,
and the owner's four-way test is what exposed it.** Proximity is not identity, and
the decisions are not unread: a *second* reader reads them, from the file, without
the governance reducer. The corrected finding is **G3 — conflicting authority**.

##### The population is 124 records, not 59

| Location | Count | Authoritative when |
|---|---|---|
| `<task>/admission/decision.yaml` (prepare-time) | 59 | `control/latest-generation.yaml` does **not** exist |
| `<task>/control/generations/<digest>/admission-decision.yaml` | 65 | that digest is the one `latest-generation.yaml` names |

The supersession rule is explicit at `control.go:604-616` and `briefing.go:106-108`:
`if paths.Results != "" → the generation's decision`, and `currentControlPaths`
(`control.go:873-882`) returns empty paths exactly when the pointer file is
absent. The comment records that reading the prepare-time file unconditionally
**was a live defect** — a task whose initial decision declared zero proof
obligations reached candidate-ready after a recomputed decision declared real
ones. The supersession rule is the repair, and it is in place.

##### Provenance

| Attribute | Value | Established from |
|---|---|---|
| `schema_version` | `1` — **all 124** | record |
| `generated_by` | `sensei admission` — **all 124** | record; = `admission.GeneratedBy` (`admission/admission.go:33`) |
| `policy` | `admission.strict.v1` ×110, `admission.strict.v2` ×14 | record |
| Writer, prepare-time | `admission.WriteCanonicalDecision` ← `cmd/awg/cmd_admit_change.go:66` — i.e. `sensei admit-change --output` | source |
| Writer, generation | `control.go:292` `writeFileAtomic(.../admission-decision.yaml)` during task advance | source |
| Writer that binds to a chain | `admission.RecordAdmissionDecided`, sole non-test caller `cmd/awg/cmd_admission_v2.go` | source |

No record is provenance-unknown: every one carries a schema version and a
`generated_by` that resolves to a single source constant, and both file writers
are identified in source.

##### Identity, tested rather than assumed

| Test | Result |
|---|---|
| decision `binding{repository_domain, revision, graph_digest_sha256}` == host task's binding (`session.yaml`) | **59 / 59 match** |
| `binding.repository_domain` matches the repository the record lives in | **59 / 59 match** |
| decision carries a `task_id` | **0 / 59** — the field does not exist in the record |
| binding triple uniquely identifies a task | **no** — 11 triples are shared by ≥2 tasks, covering **42 of 59** records |

So none is "a decision for another task, revision, or repository world" — but the
records cannot *prove* that themselves. Their task binding is positional. That is
**G2**, and it is a design property, not an accident of this corpus.

##### The four totals

Classification: *canonical* = authoritative under the supersession rule above;
*referenced* = its digest is reachable from a verified chain entry.

| # | Category | Prepare | Generation | Total |
|---|---|---|---|---|
| 1 | correctly referenced canonical decisions | 0 | 0 | **0** |
| 2 | **same-directory migration candidates** | 8 | 17 | **25** |
| 3 | legacy or superseded, requiring migration | 51 | 48 | **99** |
| 4 | unrelated or provenance-unknown | 0 | 0 | **0** |

Category 3 splits: **67** belong to tasks with no ledger at all (34 prepare + 33
generation) — there is no chain to be missing from, so this is migration debt,
not an integration defect; **32** are superseded by a later generation (17 + 15)
and are correctly ignored by both readers.

**Category 2 is named for what was actually established, not for what it looks
like** (owner, round 5). These records are canonical under the supersession rule
and their binding triple matches the host task — but the decision carries **no
`task_id`**, and 11 binding triples are shared across tasks (G2). Directory
placement is currently contributing identity, so "canonical, matching" would
overclaim. **They must not be appended to any chain automatically.** Doing so
requires task and session identity, and chronology, established independently of
where the file sits — none of which these records carry.

**Zero in category 1** is the finding. No admission decision in either repository
has ever been bound to a chain, and no chain contains `authority_resolved` or
`admission_decided`.

##### One chain-resident decision, and it is orphaned

Not proximity: an artifact census of all 25 chains found **348 stored artifacts,
344 reachable from a chain entry, 4 orphaned** — all four in
`task.bootstrap-direction-records.b6c53bfde3aa`, and they are exactly
`architecture_change_request`, `architecture_admission_decision`,
`architecture_direction_bootstrap_authorization` and
`architecture_admission_verification`.

That task's governance quartet was written *into the chain's content-addressed
store* and then never referenced by any ledger entry. `foldGovernance` decodes
only artifacts that entries reference, so it cannot see them either. This is the
integration gap caught mid-act: the artifacts were stored, the entries were not
appended.

##### The 12 positive decisions — the capability question

Of the 124 records, **15 are positive** (13 `admitted`, 2
`admitted_with_conditions`); 12 are prepare-time, 3 generation. **9 are
authoritative**; 8 of those belong to tasks that have a ledger chain, all in
`sensei-code`.

For all 8: the chain contains **no `authority_resolved` and no
`admission_consumed` event**, so `foldGovernance` returns `waiting_governance`
at its first check and `gov.Resolved` is false.

**Mistaken as available — the governed paths: NO.** `control.go:333-336` handles
exactly this case:

> A task that has not resolved typed governance must not be granted a mutation
> capability through the legacy path.

and downgrades a positive legacy decision to `waiting`. `session.go:569-576` does
the same for Status, failing closed even on a governance *error*. Three of the
four consuming boundaries are correct.

**Mistaken as available — the projection path: YES, and it is persisted.**
`projectControlStatusAndClosure` (`control.go:553-647`) never calls
`governanceDisposition`. It reads `decision.MutationCapability` and passes it
into `taskcontrol.Project`, downgrading only when the session fails verification.
Measured on disk right now:

| Task (sensei-code) | file decision | cached `permission.modify` |
|---|---|---|
| `task.defect.1ddb28dcfa76` | admitted | **admitted** |
| `task.defect.23620399bfa1` | admitted | **admitted** |
| `task.defect.483915c3a3db` | admitted | **admitted** |
| `task.defect.4d00ea3fade3` | admitted | **admitted** |
| `task.defect.b165d8e2088a` | admitted | **admitted** |
| `task.defect.b3852005b2c6` | admitted | **admitted** |
| `task.defect.f6fdca49d3b9` | admitted | **admitted** |
| `task.defect.c8d726b51be5` | admitted | `waiting` |

**Seven persisted control projections assert `modify: admitted` for tasks whose
chains carry no authority resolution and no consumption.** The eighth shows the
downgrade, which is what proves the other seven are not simply how the projector
behaves — they predate the fail-closed guard (`control.go:333-336` says legacy
admission "no longer" hands out modify permission, so once it did).

And `control.go:575-577` serves that cache without re-consulting governance:

```go
if len(verifyErrors) == 0 && latestState != nil && !taskHasGovernedDisposition(taskDir) {
    return *latestState, ...
}
```

All 8 tasks have `control/latest.yaml` and no recorded question disposition, so
the cache branch is the one taken whenever the session still verifies. The reach
is not internal: `ControlStatus` feeds `cmd/awareness-mcp/main.go:781`,
`workspace_tools.go:248` and `BuildTaskBriefing` → `main.go:828` — the MCP
briefing and control tools an agent reads as its permission.

**Ignored when available: not measured** — it requires a chain that *does* carry
a positive resolved authority, and none exists.

**Reused after consumption: unenforceable on this path.** The consumption receipt
is L3's single-use guarantee. `projectControlStatusAndClosure` never looks for
one, so on that path there is nothing for a consumption to spend.

##### What is and is not established

**Established (source + on-disk records):** the ungoverned projection path
exists, has no governance call, is reachable from the MCP tools, and seven
persisted projections currently assert `modify: admitted` for ungoverned tasks.

**Not established:** whether those seven would be *served* today. The cache branch
requires `len(verifyErrors) == 0`, and these tasks are bound to revisions the
working trees have long since left; a failing verification sends the request to
the recompute path, which refuses. Confirming this needs execution, which is
outside this step's boundary. **Reported as unknown, not as a live exposure.**

The design property stands regardless of what a run today would return: **a
cached permission projection is durable, is served without re-validation against
the governance reducer, and its correctness depends entirely on the guard state
of whichever binary last wrote it.** That is a bridge defect between L3 and the
local projection surface — the class the owner predicted, at the boundary the
owner named.

#### 5.5.9 Falsification of the seven cached permissions — observed truth table

**Result: the hypothesis is refuted for these eight tasks. None of the eight
tested stale tasks served mutation permission.**

**Scope, corrected in round 6 at the owner's instruction:** this establishes that
*these eight stale tasks* serve no grant. It does **not** establish that no fresh
binding does — §5.5.10 shows one does. Read every "no bypass" statement in this
subsection as scoped to the observations below.

Read-only observation, 2026-09-10, executed twice: once against a sandbox copy of
`.sensei`, once against the live `sensei-code` working repository. Both runs agree.
Method and the read-only proof in §11.5.

##### The truth table

All eight tasks (the seven with cached `modify: admitted`, plus
`c8d726b51be5` as the in-corpus negative):

| # | Observation | Result — identical for all 8 unless noted |
|---|---|---|
| 1 | `ledger.Store.Verify()` | **valid**, 4 or 7 entries |
| 2 | repository / base-binding validity | **2 errors**: `repository revision changed`, `task.binding.working_tree_outside_envelope` |
| 3 | direct `decision.yaml` | `decision=admitted`, `mutation_capability=admitted` |
| 4 | `foldGovernance` | `phase=waiting_governance status=waiting_governance resolved=false GrantModify=false` |
| 5 | persisted `control/latest.yaml` | **`modify=admitted`** (7) · `modify=waiting` (`c8d726b51be5`) |
| 6 | public `ControlStatus` | **`modify=refused inspect=uncertifiable`** |
| 7 | MCP briefing (`BuildTaskBriefing`) | **refused**: *"task briefing refused: repair task binding before using architectural context"* |
| 8 | is `permission.modify` actually served? | **No.** Every public surface refuses |
| 9 | source branch that selected the answer | `control.go:575-577` cache shortcut **not taken** (`len(verifyErrors) != 0`) → recompute path → `control.go:625-628` sets `inspect=uncertifiable`, `modify=refused`. Briefing refuses earlier still, on the binding check |

`Status(Verify:true)` independently reports `phase=stale status=stale
verified=false` for all eight.

##### What refuted the hypothesis

Round 4 predicted the exposure from `resolveControlTask:704-712`, which returns a
**nil active pointer** for an explicitly-named task directory — the MCP call
shape. The inference was that `verifySession(repo, taskDir, sess, nil)` would then
skip every binding check and return no errors, so the cache branch would be taken.

**That inference was wrong.** `verifySession` performs repository-level checks
that do not depend on the pointer at all: the bound revision against the current
repository, and the working tree against the task's envelope. With `ptr == nil`
those two still fire, and two errors are enough to defeat the cache branch. The
pointer only adds *further* checks (5 to 7 more, observed).

The lesson is the one this packet keeps finding: I read a guard's *inputs* and
inferred its *outcome*. Only executing it settled it.

##### Controls

**C1 — governed-state control, *not* a positive permission control** (owner,
round 6). Its capability has already been consumed at `scope_verified`, so it
proves the reducer recognizes governance without proving the public surface can
correctly grant permission. §5.5.10's F2 is the positive permission control C1 is
not.

**C1 — current, fully governed task whose chain references authority.** Seeded
from the repository's own fixture (`internal/resulttestkit.Seed`). Chain:

```
000001-task_prepared   000002-authority_resolved   000003-admission_decided
000004-admission_consumed   000005-change_observed   000006-scope_verified
```

`foldGovernance` → `phase=scope_verified status=scope_verified resolved=true
GrantModify=false terminal=true`.

This is the control that matters: it proves `foldGovernance` does **not**
vacuously answer `waiting_governance`. Given a chain that carries authority it
resolves, and it correctly withholds the modify grant at `scope_verified` — the
capability was consumed and the operation is closed to further mutation. The
`waiting_governance` seen on all 25 real chains is a real verdict about their
contents, not a stuck default.

*Limitation, stated:* the fixture builds a ledger-only task — no `session.yaml`,
no `admission/decision.yaml`, no `control/latest.yaml` — so rows 2, 3, 5, 6 and 7
cannot be observed on it. It controls the governance reducer, not the projection
surface.

**C2 — deliberately stale task that must not serve cached permission.** The same
seeded world with the working tree moved off the bound revision. `foldGovernance`
is **unchanged** (`resolved=true`, `GrantModify=false`) — correctly, since it
reads only the chain and staleness is an L5 claim about the world. The eight real
tasks serve as the natural form of this control, and all eight are refused.

##### The one cell not observed, and what it decides

The dangerous combination is **verification passes AND governance is unresolved
AND the file decision is positive**. No task in either repository is in that
state: every task carrying a positive ungoverned decision is also stale.

Constructing one requires a repository clone checked out at a task's bound
revision with that task's directory inside it. That was not done — it is beyond a
read-only observation of existing state, and the owner's instruction bounds this
step.

What is established without it: `projectControlStatusAndClosure`
(`control.go:553-647`) contains **no call to `governanceDisposition`** — the four
call sites in the tree are `control.go:316`, `session.go:571`,
`advance_result.go:170` and the definition — and at `control.go:618-624` it
assigns `mutationCapability = decision.MutationCapability`, downgrading only on
verification errors. With an empty `verifyErrors` there is nothing in that
function that can withhold a positive file decision.

So the branch grants by construction; only staleness is standing between it and a
live grant. **That is a latent conflicting-authority design, recorded as such and
not as a presently reachable bypass.**

##### G3 restated with the observation

| | |
|---|---|
| **Is there a live authorization bypass today?** | **No.** All eight refuse at every public surface. |
| **Is there one authority for the mutation predicate?** | **No.** Two writers establish it; one consuming path reads the ungoverned one. |
| **What suppresses the conflict?** | Staleness — an **L5** property, re-evaluated per call, about the world rather than about authority. |
| **Is that a durable guarantee?** | **No.** It is correct today by coincidence of these tasks being old. A task that is both fresh and ungoverned would take the granting branch. |
| **Do the persisted projections still assert a grant?** | **Yes.** Seven `control/latest.yaml` files say `modify: admitted` right now. They are inert only because nothing reads them without first failing verification. |

#### 5.5.10 G3 diagnostic — the disagreement is demonstrated, in both directions

**Result: a permission-reporting defect is proven on a healthy binding, and an
execution boundary that accepts the ungoverned answer as authority is identified
in source.** Disposable fixtures at `42475333`; no live task record was read,
changed or migrated. Method in §11.6.

##### Fixtures and observed results

Every fixture was built with the repository's own production APIs and test
helpers (`authorityRepo`, `Prepare`, `identity.Enroll`, `admission.DecideAdmission`,
`admission.RecordAdmissionDecided`, `admission.ConsumeCapability`,
`admission.RecordAdmissionConsumed`). **Binding verification returned zero errors
in all three** — this is the healthy-binding case the round-5 observation could
not reach.

| | F1 governance grants nothing, file says admitted | F2 typed admission, capability unconsumed | F3 same, after consumption |
|---|---|---|---|
| **Required result** | no mutation grant | grant only the admitted scope | no grant, incl. via older cache |
| binding verification | **0 errors** | **0 errors** | **0 errors** |
| chain | `task_prepared … authority_resolved …` | `… authority_resolved … admission_decided` | `… admission_decided admission_consumed` |
| **governance** (`foldGovernance`) | `ready_for_admission` resolved=true **GrantModify=false** | `admitted`/`ready_for_mutation` **GrantModify=true** scope `[gin.go]` | `admitted` **GrantModify=false** scope `[]` |
| file decision | **`admitted`** (digest-valid) | `waiting` | `waiting` |
| cache `control/latest.yaml` | `waiting` | `waiting` | `waiting` |
| `ControlStatus` (cache branch) | `waiting` | `waiting` | `waiting` |
| `ResolveControlAndClosure` (recompute) | **`admitted`** | `waiting` | `waiting` |
| `BuildTaskBriefing` | `waiting` | `waiting` | `waiting` |
| **verdict** | ***DISAGREEMENT — over-report*** | ***DISAGREEMENT — under-report*** | agree |

##### F1 — the over-report, and the answer to the round-5 open cell

Governance resolves and **withholds** the mutation grant (`ready_for_admission`:
authority is resolved, no typed decision exists). The decision *file* says
`admitted`. The public recompute path reports **`modify=admitted`**.

Per the owner's criterion — *a public `modify=admitted` disagreement proves a
permission-reporting defect* — **that criterion is met.** The branch is
`control.go:618-624`, which assigns `mutationCapability =
decision.MutationCapability` and never consults `governanceDisposition`.

This is the cell §5.5.9 left unobserved. It required no stale binding, no tamper
and no fabricated record.

##### F2 — the under-report, which was not predicted

Governance **grants** modify over scope `[gin.go]`, having recorded a real
`admission_decided` whose capability is unconsumed. Every public surface reports
`waiting` — the file decision still says `waiting` because nothing rewrites it
when the typed decision lands in the ledger.

So the two authorities disagree in *both* directions, and neither direction is a
cache-staleness artifact: F2's recompute path disagrees too. The defect is not
"a stale cache"; it is that these surfaces read a different authority.

##### F3 — consumption, including through an older cache

After `admission_consumed`, governance withholds the grant (`GrantModify=false`,
scope emptied). The pre-consumption cache was restored to test the owner's "older
cache" condition. All surfaces report `waiting` — **agreement, but not because
consumption was honoured**: the file decision was `waiting` throughout, so the
ungoverned paths never had a grant to leak. F3 passes for a reason unrelated to
the guarantee it was meant to test, and must not be read as evidence that
consumption is enforced on these paths. Testing that properly needs F2's ledger
state combined with F1's positive file decision.

##### One guard found along the way

The decision file is **digest-sealed**: `admission.LoadDecision`
(`admission.go:1018`) recomputes `decisionDigest` and refuses on mismatch. A
first attempt at F1 that hand-edited `decision: waiting → admitted` was rejected
with `decision digest invalid`, and an edited `control/latest.yaml` was rejected
with `task control digest mismatch`.

So the positive file decision cannot be forged by editing — it must be produced by
the real writer. F1 therefore used `admission.WriteCanonicalDecision`, the same
call `sensei admit-change --output` makes, which reseals the digest
(`admission.go:1024-1028`). **Stated explicitly as required:** the decision's
*content* (`admitted`) was set by the harness; the record was produced and sealed
by the production writer. This is the on-disk shape the seven live tasks already
have with valid digests, so the state is reachable without any tampering.

##### The execution boundary — traced in source, no mutation performed

`cmd/awg/cmd_synthesis_run.go:205` obtains `control` from
**`ResolveControlAndClosure`** — the F1 over-reporting path — and its only
mutation-permission gate is `validateCurrentBinding` (`:1056-1064`):

```go
if control.BindingHealth != "current" { return ...stale... }
if control.Permission.Modify == admission.CapabilityRefused {
    return fmt.Errorf("task mutation capability is %q; ...")
}
```

It refuses only on exactly `refused`. In the F1 state it receives `admitted` and
proceeds. `cmd_synthesis_run.go` never calls `governanceDisposition`; its header
comment (`:11`) records that admit-change/verify-admission are a *separate human
step*, so the omission is by design rather than oversight.

The downstream apply step does not restore the ledger as authority either:
`cmd_synthesis_apply.go:197` loads a decision from a caller-supplied
`--decision` path and gates on `d.MutationCapability` at `:417-420`. That is the
same **file** authority, consulted a second time.

**So the mutation path is authorized end to end by the decision file, and at no
point by the verified ledger.** Per the owner's standard, an execution boundary
that accepts the ungoverned answer as authority is identified — traced in source;
no candidate was generated and no mutation was applied.

##### The additional guard that stands between this and the live tasks

`validateCurrentBinding`'s **first** check, `BindingHealth != "current"`, is the
same staleness suppressant §5.5.9 measured. The seven live tasks fail it. It is
an **L5** property — about the world, re-evaluated per call — standing in for an
authority check that is absent. It is why nothing is presently exploitable and
why that fact is not a guarantee.

##### What this settles for G3's repair scope

| Question | Answer |
|---|---|
| Is there a permission-reporting defect? | **Yes — proven**, both directions, healthy binding, zero verification errors |
| Is it a cache artifact? | **No.** The recompute path disagrees in both F1 and F2 |
| Does an execution boundary accept the answer as authority? | **Yes** — `synthesis-run` → `validateCurrentBinding`, and `synthesis-apply` re-reads the same file authority |
| Is it presently exploitable on live records? | **No** — every live positive-decision task fails the binding-health check first (§5.5.9) |
| Does the repair need a lifecycle redesign? | **No.** §4.4's rule is sufficient: one verified-ledger producer for the mutation predicate; files and caches may display, never grant |

#### 5.5.11 The synthesis boundary traced against the operation-state table

Source trace at `42475333`. No command was executed; no candidate generated; no
mutation applied.

##### What each command actually does

| Command | Creates a candidate | Consumes authority | Mutates the repository | Observes a mutation | Verifies scope |
|---|---|---|---|---|---|
| `synthesis-run` | **yes** — seals a candidate artifact + lineage bundle into the candidate store | **no** | **no** — writes only to the candidate store (`cmd_synthesis_run.go:8-11`: "never touches admission or application") | no | no |
| `synthesis-admit` | no | **no** — explicitly ("does NOT evaluate admission", `cmd_synthesis_admit.go:18-21`) | no | no | no |
| `synthesis-apply` | no | **yes, but not the ledger's** — see below | **yes** — applies the candidate to a target worktree | no | no |

**Nothing in the pipeline observes a mutation or verifies scope.** Rows 4 and 5
of §4.5.1's table have no participant here: `change_observed` and
`scope_verification` receipts are written by `admission.RecordChangeObserved` /
`RecordScopeVerified`, whose sole non-test caller is `cmd_admission_v2.go`. A
mutation applied through `synthesis-apply` therefore terminates the governed
sequence without ever reaching the two states that close it.

##### Position against the table

| Table row | Legal next action | Command that performs it | Gate it actually applies |
|---|---|---|---|
| No binding typed decision | resolve admission | *(none in this pipeline)* | — |
| Valid decision, capability unconsumed | consume the capability | **no command consumes the ledger capability** | — |
| Capability consumed, no change observed | apply the bound operation | `synthesis-apply` | its **own** file claim, not the ledger's consumption |
| Change observed, scope not verified | verify scope | *(none)* | — |
| Scope verified | no further mutation | *(none)* | — |
| Invalid/expired/stale/conflicting | none | partially — `BindingHealth` only | staleness, not authority |

`synthesis-run` sits at no row: it creates a candidate, which the table does not
govern, yet gates itself on a mutation predicate (§4.5.5).

##### The durable values that bind an application to its authorization

Bound today by `synthesis-apply`:

| Value | Where | How enforced |
|---|---|---|
| task id | `lineage.TaskBinding.TaskID` | compared to `session.TaskID` (`synthesis_task_drift.go:64-67`) |
| task session digest | `lineage.SessionDigestSHA256` | must equal the sealed candidate's `artifact.SessionDigestSHA256` (`:50-53`) |
| task control state digest | `lineage.TaskControlStateDigestSHA256` | drift refusal |
| closure report digest | `lineage.ClosureReportDigestSHA256` | drift refusal |
| candidate artifact digest | `lineage.CandidateArtifactDigestSHA256` | names the store paths; sealed |
| admission request identity + derived scope | `admissioncomposition.ComposeDecisionReceipt` (`compose.go:107-119`) | recomputed and compared; decision must be bound to the composed request |
| decision digest | `canonicalDecision.DecisionDigestSHA256` | captured into the O5A receipt |
| base revision | step 6 — base manifest re-read from git at the candidate's own base | |

**Not bound, and each is a separate missing conjunction:**

| Missing | Consequence |
|---|---|
| **capability-consumption digest** | nothing ties the application to a `capability_consumption` receipt |
| **expected ledger head** | the chain may advance between the checks and the mutation |
| **ledger `task.session_id`** | the session *digest* is bound; the ledger's session identity is not |
| **consumed operation set** | `capability_consumption.consumed_operations` is never compared to what is applied |

##### Does `synthesis-apply` prove these five things?

Measured by exhaustive search: **`cmd_synthesis_apply.go` contains no reference to
the `ledger` package at all.** Every "consumption" in that file is its own
file-based record in the candidate store.

| # | Conjunction | Proven? | Evidence |
|---|---|---|---|
| 1 | the capability was consumed | **NO** | no ledger read; no `admission_consumed` lookup anywhere |
| 2 | the consumption belongs to this task and decision | **NO** | vacuous — there is no consumption record to attribute |
| 3 | the candidate matches the admitted operation and scope | **YES** | `ComposeDecisionReceipt` recomputes the request identity digest and compares `DerivedScope`, then requires the decision's `RequestReceipt.DigestSHA256` and `Scope` to match and `RequestedMode == modify` (`compose.go:107-119`) |
| 4 | no `change_observed` or prior application already consumed the operation | **PARTIAL** | prior *application* is refused by `.o5b-receipt.json` plus an atomic `O_CREATE\|O_EXCL` claim (`:226-252`) — a genuine, well-built gate. But it is **scoped to this candidate in this store**: it never consults `change_observed`, so a mutation recorded in the ledger by any other path is invisible to it |
| 5 | the verified ledger state is still current immediately before mutation | **NO** | no expected-head check; the drift refusal compares task/closure *digests* from the lineage bundle, not the ledger head, and runs at step 3 rather than immediately before the write |

Four missing conjunctions, recorded separately as **G3.1 – G3.4**:

- **G3.1** — application does not prove the ledger capability was consumed.
- **G3.2** — application does not attribute a consumption to this task and decision.
- **G3.3** — application does not consult `change_observed`; its idempotence is
  per-candidate-store, not per-operation.
- **G3.4** — application does not re-verify the ledger head immediately before
  mutating.

##### A third authority for the single-use guarantee

The consumption gate `synthesis-apply` *does* implement is a parallel one:
`.o5b-claim` and `.o5b-receipt.json` in the candidate store. It is careful — the
atomic claim closes a real concurrent-apply race, and its comments show the
reasoning. But it means the single-use property now lives in **two independent
places that never meet**:

| Authority | Record | Reader |
|---|---|---|
| ledger | `capability_consumption` via `admission_consumed` | `foldGovernance` |
| candidate store | `.o5b-claim` / `.o5b-receipt.json` | `synthesis-apply` |

Neither can see the other. Consuming the ledger capability does not stop a
re-apply; removing the store receipt does not restore a ledger capability. This
is the same G3 shape one layer down, and it is why §4.5.2 insists each capability
have exactly one ledger-derived owner.

##### The two capability gates, compared

| Command | Gate | Requires a positive capability? |
|---|---|---|
| `synthesis-run` | `validateCurrentBinding` (`:1056-1064`) | **No** — refuses only the literal `refused`; `waiting` passes (§4.5.5) |
| `synthesis-apply` | `admissionDoesNotAuthorize` (`:411-422`) | **Yes** — requires `Decision ∈ {admitted, admitted_with_conditions}` **and** `MutationCapability ∈ {admitted, admitted_with_conditions}`, and its comment explains why both are read |

`synthesis-apply` gets the shape right and reads the wrong authority — the
decision **file** supplied by `--decision`, never the ledger. `synthesis-run`
reads an ungoverned source *and* uses a fail-open comparison. The two defects are
independent and need separate repairs.

## 6. Gateway design requirements

**Round-3 scope correction.** These requirements were written for "the single
admission point through which every *phase-bearing* event passes." Per §0.1 and
§4.0.2 that is the wrong seam: most lifecycle facts are established by typed
receipts, not by the phase field, and the enforcement that matters — single-use
capability consumption, terminal exclusion, result identity — reads receipts.

The requirements below are individually sound and several are independent of the
phase framing: **3 (replay before legality), 5 (fail closed on unreadable state)
and 7 (one edge object)** are properties any write boundary needs, and 5 is the
direct repair of A-1. Requirements **1, 2, 6 and 8** are scoped to phase-bearing
events and must be re-derived once §4.0 establishes which boundary enforces
what, for which object.

What replaces "the gateway" as the organising idea is §4.0's question 4 asked per
lifecycle: *which write boundary enforces this fact's consequences?* There may be
more than one, and they are not required to be the same code.

Retained as written, pending that re-derivation:

Requirements, not an implementation. The gateway is the single admission point
through which every phase-bearing event passes.

1. **Sole admission.** No producer appends a phase-bearing event except through
   it. Enforced by construction where possible (unexported append, or a token the
   gateway alone mints) rather than by convention.
2. **Self-describing transitions.** The caller supplies `from` and `to`; the
   gateway verifies `from` against the durable record rather than trusting it,
   and refuses when they disagree. This removes every caller's need to reconstruct
   the current phase.
3. **Replay before legality** (§4.1). Fixed ordering, owned once.
4. **Terminal exclusion is one predicate — with two answers.** Terminal/result
   contradiction — completed vs abandoned vs revoked vs result-transition — is
   computed in one place over `terminalFacts` and consumed identically by the
   gateway (to refuse) and by `InspectTerminalState` (to classify). The writer and
   the reader must not hold separate copies; that divergence is assessment finding
   R5-1. Per §4.1.3 the predicate reports **absorbing** or **closed**, never a
   single boolean: a `closed` phase refuses ordinary work and admits a `governed`
   edge from the revocation authority alone.
5. **Fail closed on unreadable state.** An unreadable lifecycle payload is never
   "no phase." A-1's mechanism — `haveFrom == false` silently skipping the guard
   — must be structurally impossible: absence of a readable phase is either a
   legitimate origin state or an integrity failure, and the gateway must
   distinguish them rather than defaulting to permit.
6. **Derived assessments cannot enter.** The gateway accepts only the 10 durable
   phases. Passing `stale` or `refused` is a programming error, rejected by type
   where the language allows.
7. **The edge set is one object.** The gateway validates against the canonical
   list of §4.1.1. Predecessor and successor views, documentation tables and any
   reporting surface are *generated* from it. No second copy is maintained, and
   D4 proves the generated views are reciprocal (§9).
8. **Every producer declares a disposition.** Each phase-bearing call site
   declares `advancing`, `same-phase` or `non-phase-bearing` (§4.1.4), and the
   advancing ones supply the payload condition of §4.1.5. The gateway evaluates
   the condition; it does not accept a phase a producer asserts on its own
   authority. An advancing call whose condition is false is not an error — it is a
   non-phase-bearing append.
9. **The era is read, never inferred.** The gateway determines which lifecycle
   era a chain belongs to by the mechanism ruled in §7.2, and never by observing
   that a phase is missing. Absence of `prepared` must not be readable as
   "legacy" (§7.2).

## 7. Migration of existing append sites

From the assessment's §5 classification.

**Group A — phase-bearing, must migrate (4):**

| Site | Change |
|---|---|
| `completion/abandon.go` | replace its local guard with the gateway; delete `latestTaskPhase` |
| `completion/complete.go` | route through the gateway (unguarded today) |
| `certification/ledger.go` | route through the gateway (unguarded today) |
| `resultrecording/record.go` | route through the gateway; reconcile `validTransitionPhaseStatus` with §4.1, dropping its three unreachable keys |

**Group B — must begin writing a phase, then migrate (3):**

`tasksession/session.go` (`task_prepared` ⇒ `prepared`),
`tasksession/control.go` (`convergence_advanced` ⇒ `converging`),
`admission/admission_ledger.go` (`admission_decided`/`admission_consumed` ⇒
`admitted`, `change_observed` ⇒ `mutation_observed`, `scope_verified` ⇒
`scope_verified`).

**REOPENED 2026-09-10 (round 3) — ownership of the predicate vs. the number of
readers projecting it.** The owner's refinement: *multiple readers may
legitimately derive state from one receipt; multiple authorities establishing the
same predicate are the actual conflict.*

Applied to `scope_verified`, the round-2 ruling answered a question that may not
have been the right one. What must be separated:

| Question | Answer at `42475333` |
|---|---|
| Which authority **establishes** "scope was verified"? | `admission.RecordScopeVerified` (`admission_ledger.go:103`) binds a typed `ScopeVerification` receipt. This is the durable predicate, and it has one writer. |
| Which readers **project** state from it? | at least three: `foldGovernance` (`governance.go:128-137`, → `scope_verified` or `waiting_mechanical_repair`), `advance_result.go:182-183` (→ entry to L4), and `resultrecording/classify.go:92` (→ `PhaseScopeVerified` on the blocked path) |
| Is the third a competing **authority**? | **Not established.** It writes a phase *label* into a result-transition payload; it does not write a `ScopeVerification` receipt. Round 2 assumed label-writing made it a second authority and ruled it out on that basis. |

So the question is now precise and still open: **does
`resultrecording/classify.go:92` establish the predicate, or project it?** If it
projects, the round-2 ruling removed a legitimate reader and the "sole owner"
framing was a category error. If it establishes, it is a genuine second authority
and the ruling was right for the wrong reason. Deciding it requires tracing
whether any consumer treats that phase label as evidence that scope *was*
verified — which this packet does not do.

**Superseded ruling, retained for the record — 2026-09-10 (round 2):**
`admission/admission_ledger.go:103` is its sole producer. Result recording may
*require or reference* an established scope verification but must not manufacture
it as a fallback. The blocked-result path (`resultrecording/classify.go:92`) must
preserve its refusal/assessment **without becoming a second phase producer**.

Consequences, which fall under the still-unapproved event-to-phase mapping:

- `resultrecording`'s only phase production becomes `proving`.
- `validTransitionPhaseStatus`'s `PhaseScopeVerified → {waiting_architect,
  waiting_governance, waiting_mechanical_repair}` row is removed, along with its
  three unreachable `PhaseWaiting*` keys (§2.4).
- The blocked path emits a `result_transition_recorded` carrying an assessment
  and **no `task_phase`**, or a distinct non-phase-bearing event. Which of the two
  is a Unit 1 design question, not settled here.

### 7.1 Migration order — M3 withdrawn; the rest re-scoped by §4.0

**OWNER RULING 2026-09-10.** M1 is approved in principle: introduce the unused
gateway, the canonical edge set and the tests, changing no stored behavior.
**M2–M7 remain unapproved until compatibility is explicit.**

Each step is independently reviewable and safe to stop after. "Safe to stop"
means the tree is consistent and no ledger is left half-migrated.

| # | Step | Behavior change | Compatibility risk | Safe to stop | Status |
|---|---|---|---|---|---|
| M1 | Gateway exists, unused. Canonical edge list (§4.1.1), generated views, two-kind terminality (§4.1.3), three dispositions (§4.1.4), payload conditions (§4.1.5), replay-before-legality ordering. D3/D4/D7/D8 scaffolding. | none | none | yes | **approved in principle** |
| M2 | Group A routes through the gateway: `abandon`, `complete`, `certification`, `resultrecording`. Phases already produced stay identical. Includes rewriting `abandon_test.go`'s `appendPhase` helper and R3-F2, which today fold a task to `refused`/`stale`/`uncertifiable` via `task_marked_stale` (§5.5.4) and become inexpressible. | none intended | low — same phases, new path | yes | held |
| M3 | ~~Group B begins writing phases~~ **WITHDRAWN in round 3.** These events already establish their facts by typed receipt and `foldGovernance` already reconstructs the phases from them (§0.1). Making them populate `task_phase` as well would add a second representation of the same fact and an obligation to keep the two in agreement forever. | — | — | — | **withdrawn** |
| M4 | `scope_verified` ownership transfers to admission. `resultrecording` blocked path stops producing a phase. | changes which event carries the phase | medium — mixed-era chains carry it from either site | yes, after M3 | held |
| M5 | `revoked` governed producer built (authority resolution + receipt, parallel to abandonment). `task_marked_stale` producer built per §5.5.4, or the event is deleted. | new operations | low — additive | yes | held |
| M6 | `tasksession` local `Phase*` constants deleted; the 8 derived assessments formalized as a reported, non-transition set. | reporting only | low | yes | held |
| M7 | `AllowedTaskTransitions` replaced by the generated canonical set; deleted events removed from `LedgerEventTypes` and from `common.schema.json` in the same change (C-2). D1–D9 complete. | table shrinks to the reachable graph | low, once M3–M5 land | terminal step | held |

**M3 is withdrawn, and with it the premise that made M4–M7 sequential.** The
remaining steps that survive on their own merits are M2 (route existing phase
writers through one boundary, including the `abandon_test.go` rewrite), M5's
`revoked` producer *if* §5.4's subject question is answered first, M6 (delete the
duplicate `tasksession` `Phase*` constants — a pure de-duplication that the
correction makes *more* clearly right), and M7's dead-edge and schema-parity
cleanup. None of them depends on Group B writing phases.

The replacement first step is not a migration at all: it is the §4.0 mapping,
completed for each of the five lifecycles in §4.0.1, which is what tells us which
of M2/M5/M6/M7 are still worth doing and in what order.

**Vocabulary unification.** `tasksession`'s local `Phase*` constants (§2.4) are
deleted; its `Status*` constants are retained as the operational status
vocabulary, and the two are explicitly documented as orthogonal — phase is
durable and admitted, status is derived and reported.

### 7.2 The era-boundary problem — retained, now subordinate to §4.0

*(Written against M3, which round 3 withdraws. It survives because the question is
not specific to phases: **any** durable lifecycle fact a new binary starts writing
raises it. "Missing X means legacy" is refused whatever X is, and the mechanism
comparison below is the answer for any X. Re-read "phase" as "newly-written
durable fact" throughout.)*

The requirement, as ruled: a **new** ledger must not omit `prepared`, and an
**old** ledger must not be rejected merely because earlier binaries never wrote
it. **"Missing phase means legacy" is refused** — it would let a malformed new
chain pass as an old one forever, and the malformation it would hide is exactly
the one A-1 was.

§5.5.5 makes the problem concrete rather than hypothetical: **all 25 persisted
chains lack `prepared`, and every phase, entirely.** There is no partially-migrated
population to reason about; there is one old era containing everything, and a new
era containing nothing yet. Whatever mechanism is chosen must therefore work when
applied to a corpus that is 100% pre-M3.

Three mechanisms compared. They are not exclusive; (a) is a precondition for the
others.

#### (a) Explicit lifecycle-schema version in ledger metadata

**What exists.** `ledger.Head` already carries `schema_version`
(`ledger/model.go:12-22`), persisted in every `ledger/HEAD.yaml`, currently `"1"`.

**Why it cannot be used as-is — measured.** `append.go:99` rewrites
`SchemaVersion: HeadSchemaVersion` on *every* append, from the writing binary's
constant. And no read site validates it: the only references to
`HeadSchemaVersion` in the tree are the declaration, that write, and an identical
write in `verify.go:129`. It is a write-only field stamped by the last writer.

So today it records *which binary wrote most recently*, not *which era the chain
began in* — and those are precisely the two facts M3 must distinguish. A pre-M3
chain touched once by a post-M3 binary would claim the new version while
containing no `prepared`, which is the failure mode the ruling forbids, arrived
at from the other direction.

**What it would take.** A lifecycle-schema version that is written **once, at
chain creation**, never rewritten, and validated on read. That is a different
field from `Head.SchemaVersion` and should be named differently to avoid
inheriting its semantics. `Head` is a mutable pointer; era is an immutable
property of the chain, so the natural home is the chain's first entry, not HEAD —
which is mechanism (b).

**Assessment: necessary but not sufficient on its own, and not satisfiable by the
existing field.**

#### (b) A durable migration/adoption event establishing the starting phase

**What exists as precedent.** `legacy_import` is already this mechanism, working:
`ledger/legacy.go:57-72` appends one event at sequence 1 with
`ExpectedHeadDigestSHA256: ""`, carrying a `Limitations` list that names exactly
what the earlier era could not express — `legacy_task_session`,
`scope_verified_legacy`, `terminal_completion_unavailable` and four more. It is
replay-idempotent (`legacy.go:47-52`). It is the shape to copy.

**Where the precedent does not reach.** `legacy_import` can only be applied to an
*empty* ledger — it appends at head `""` and its replay check requires the chain
to be exactly one entry. It cannot mark the 25 existing chains, which have 4 to 28
entries each. A mid-chain adoption event is therefore a genuinely new thing, not a
reuse.

**Shape required.** An event appended once to each existing chain at its current
head, declaring: the lifecycle-schema era now in force, the phase the chain is
deemed to be in at that point, and the limitations of what precedes it (by
analogy: `phase_history_unavailable`). After it, every entry is governed; before
it, none is. The `from` phase of the first governed transition is the phase this
event established, so §4.1's self-describing transitions have a referent from
entry one of the new era.

**The `migration_executed` question.** §5.5.3 recommends deleting
`migration_executed` for want of a producer and a reader. This mechanism is a
producer and a reader looking for an event. The fit is close but not exact: the
existing `MigrationExecutionReceipt` (`model.go:493`) describes an *architectural
migration plan step* — `migration_plan_id`, `step_id`, `source_state`,
`target_state`, `mechanism` — which is a different concern from lifecycle-schema
adoption. Repurposing the event name while giving it a new payload type is
defensible; silently widening the receipt is not. **This is an owner decision**,
recorded in §12: reuse the event name with a new adoption payload, or delete it
and declare a new `lifecycle_era_adopted` event.

**Assessment: this is the mechanism that carries the era durably, and it subsumes
(a) by putting the version in an immutable entry rather than a rewritten pointer.**

#### (c) A one-time migration projecting existing chains into the new model

**What it would do.** Walk each existing chain and back-fill the phases the new
model expects — reading `task_prepared` as `prepared`, the first
`convergence_advanced` as `converging`, and so on.

**What the measurement says about it.** It is more tractable than feared and less
useful than it looks. Tractable, because the corpus is uniform: 25 chains, one
`task_prepared` each at sequence 1, and only three other event types (§5.5.5).
Less useful, because back-filling means *writing durable facts nobody observed* —
asserting that a task passed through `converging` because an event named
`convergence_advanced` exists, when no producer ever made that determination.
That is a fabricated durable fact, and §3(a) exists to forbid exactly it.

There is also no `admitted` to back-fill even where an admission decision exists:
the 59 decisions are on disk but were never bound to their chains (§5.5.5), so a
projection would have to *infer* the binding.

**Assessment: recommended against as back-fill.** The defensible reduction of (c)
is not projection but *classification*: one pass that appends the (b) event to
each existing chain, recording the era and the deemed current phase, without
inventing the history that preceded it. That is (b) applied to the existing
corpus, and it is the only part of (c) worth executing.

#### Recommendation

**(b), with (c) reduced to a one-time application of (b) to the 25 existing
chains, and (a) satisfied by (b) rather than by `Head.SchemaVersion`.**

The resulting rule, which is the property the gateway enforces (§6.9):

> A chain's era is established by the presence and content of its adoption event,
> never by the absence of a phase. A chain with no adoption event is pre-era: its
> entries are ungoverned and no phase is expected. A chain with an adoption event
> is governed from that entry forward, and a governed entry that omits a required
> phase is **malformed** — refused, not tolerated.

This is what makes the refused rule unavailable: absence of `prepared` in a
governed chain is an integrity failure, and absence in an ungoverned chain is
expected, and the two are told apart by a durable fact rather than by the
absence itself. It is the same repair as §6.5 — "absence of a readable phase is
either a legitimate origin state or an integrity failure, and the gateway must
distinguish them rather than defaulting to permit" — applied one level up, to
whole chains rather than single entries.

**Not decided here:** whether the adoption pass runs over the existing 25 chains
at all, or whether pre-era chains are simply left ungoverned for their remaining
life and only newly created chains are governed. The second is cheaper and loses
nothing measurable, since none of the 25 carries a phase to preserve. Recorded as
a forced sub-decision in §12.

## 8. Legacy and import exceptions, and their containment

Three exceptions are justified; each must be *contained*, not merely permitted.

| Site | Exception | Containment |
|---|---|---|
| `ledger/legacy.go` (`legacy_import`) | imports pre-protocol history; by definition precedes the lifecycle | may never assert a phase; chains containing it are marked legacy, and the gateway treats a legacy chain's first admitted transition as an origin rather than an illegal move from nothing |
| `questiondisposition/record.go` | records a question's disposition, not a phase change | must be structurally unable to write `task_phase`; enforced by payload type, not review |
| `admission/admission_ledger.go` — *partial* | some events annotate without advancing (`authority_resolved`) | the annotating subset stays exempt; the advancing subset (§7 Group B) migrates. The split must be explicit in the code, not implied by which fields happen to be set |

The containment principle: an exception is a **named, tested** category, not the
absence of enforcement. Today all five non-phase-bearing sites are exempt for the
same reason — they set no `TaskPhase` field — which is indistinguishable from an
oversight, and in two cases (§7 Group B) was one.

## 9. Test obligations

### 9.0 Round-3 re-scoping

The owner's revised target — *test agreement across the boundaries* — reorders
these. D1–D4 and D7–D9 were written to prove properties of a phase vocabulary and
its transition table; with §4 demoted to hypothesis they cannot be the primary
obligations. They are retained because most survive re-scoping, and because D5
(mutation controls) applies to whatever replaces them.

The primary obligations become the four agreement classes the owner named. Each
is a cross-boundary property, not a property of one vocabulary:

**A1 — terminal exclusion, per lifecycle.** A task cannot be both completed and
abandoned; an operation cannot mutate after its capability is consumed; a result
cannot be certified and revoked. Tested at the write boundary that enforces each,
and at the reader that classifies it, with the writer and reader sharing one
predicate (the R5-1 requirement, generalised). Includes the §4.0.2(ii) case:
"terminal" must name its lifecycle, and `scope_verified` being terminal for the
operation must not be readable as terminal for the task — `advance_result.go`
depends on exactly that.

**A2 — consumed authority.** The §4.0.2(i) case. With an `admission_consumed`
receipt present, no mutation is granted; with it absent, it is; and an
*unreadable* one grants nothing. The third case is the one that matters and the
one `governance.go:162-168` comments on. Mutation control: make the consumption
decode failure return "unconsumed" and the test must fail.

**A3 — result identity.** The observed change bound by `scope_verification` and
the change bound by the result transition are the same change. Today
`RecordChangeObserved`'s comment asserts this is why the event records the
observed mutation itself rather than a tree digest; nothing tests it.

**A4 — recovery, at every boundary.** The A-1 mechanism generalised: for each
reader, an absent record and an unreadable record must be distinguishable, and
neither may default to permit. `foldGovernance` already implements this
deliberately in three places; the test that it *keeps* doing so does not exist.

**A5 — reader/writer connectivity.** New, from §5.5.8: for each lifecycle fact,
the writer that establishes it and the reader that reconstructs it agree on
*where it lives*. The measured failure — 59 typed admission decisions on disk
that `foldGovernance` structurally cannot see — passes every existing test.

D5's mutation-control requirement applies to A1–A5 unchanged: each must be proven
non-vacuous by a matched mutation. §5.5.3 measured one vacuous assertion already
in the tree; A1–A5 must not add more.

### 9.1 Round-2 obligations, retained and re-scoped

Unit 1 is not implementable-and-provable without these. Both directions are
required; either alone is satisfiable by a vacuous implementation.

**D1 — every declared phase is producible.** For each of the 10 durable phases, a
test that drives a real producer and asserts the phase appears in a verified
ledger payload. A phase with no passing D1 test may not remain in the vocabulary.
This test is what A-1 would have failed, and what nothing in the current suite
attempts.

**D2 — every produced phase was admitted through the gateway.** A whole-tree
check that no `TaskPhase` assignment reaches a payload except via the gateway,
plus a runtime assertion. Static enumeration alone rots; both are required.

**D3 — exhaustive vocabulary coverage.** A test iterating `TaskPhase` values and
failing on any not classified as durable-with-a-D1-test or explicitly declared
derived. This is what makes adding a phase without a producer impossible, and is
the structural remedy for A-1 rather than a repair of it.

**D4 — the edge set is one object and its views agree.** Every edge in the
canonical list (§4.1.1) is exercisable between producible phases, or the edge is
deleted; and the generated predecessor and successor views are proven reciprocal
— every `(from, to, class)` reachable from the successor view appears in the
predecessor view and conversely. This is the mechanical check that makes the
defect the owner found unrepeatable: it is not possible to hand-edit one view.
Against today's `AllowedTaskTransitions`, six of roughly forty edges would pass
the first half.

**D5 — mutation controls.** Per the standing requirement, each of D1–D4 and
D7–D9 is proven non-vacuous by a matched mutation: remove the gateway call from
one producer and D2 must fail; delete one producer and its D1 must fail; add an
unproducible phase and D3 must fail; add a dead edge, or desynchronize one
generated view, and D4 must fail; make a `same-phase` producer advancing and D7
must fail; make one payload condition unconditional and D8 must fail; reintroduce
"missing phase means legacy" and D9 must fail. A check that passes under its own
mutation is not evidence — §5.5.3 measured one already in the tree
(`TestBoundaryProofNoLaterPhaseOrGovernedMutation` asserting the absence of an
event nothing can produce), and these obligations must not add another.

**D6 — negative control on the derived set.** Attempting to admit `stale`,
`refused`, or `uncertifiable` through the gateway must be refused, and the
refusal tested. This is what prevents the removed states from creeping back as
undeclared strings. Note that this obligation *conflicts with an existing test*:
`abandon_test.go`'s `appendPhase` helper folds a task to exactly these three
phases, and R3-F2 depends on it (§5.5.4). D6 and R3-F2 cannot both hold as
written; M2 must resolve it in favour of D6.

**D7 — repeated-phase semantics.** For each producer declared `same-phase`
(§4.1.4), a test that invokes it twice and asserts the second occurrence appends
a durable entry and moves no phase; and, for `convergence_advanced` specifically,
that the second and later advances do not re-assert `converging` over a further
phase. The measured corpus has a chain with nine of them (§5.5.5), so this is the
ordinary case, not an edge case. Paired with a negative test: a `same-phase`
event declaring a phase different from the current one is refused.

**D8 — payload conditions decide phase production.** For each advancing producer
in §4.1.5's condition table, matched positive and negative cases: an
`admission_decided` carrying `admitted` produces the phase, and one carrying
`waiting`, `refused` or `uncertifiable` produces no phase and no error. The
negative direction is the one that matters — 47 of 59 real decisions take it
(§4.1.5) — and it is the direction a happy-path suite omits.

**D9 — era is read, never inferred.** Three cases. A pre-era chain (no adoption
event) with no phase verifies. A governed chain (adoption event present) missing a
required phase is **refused as malformed**, not tolerated as legacy. A governed
chain with the phase verifies. The middle case is the whole point: it is the test
that "missing phase means legacy" would fail, and it must fail if that rule is
ever reintroduced.

## 10. What this packet does not decide

**Round 3 moved most of §12's "corrected, awaiting ruling" list back into this
one.** The packet no longer proposes a settled model. It proposes a *method*
(§4.0), records what was measured, and marks which round-2 conclusions survived
the correction and which did not.

The largest thing it does not decide: **the §4.0 mapping itself, for any of the
five lifecycles.** That is the next unit of work, and it is analysis, not
implementation. Nothing in §4–§7 may be built before it.

Also undecided, and newly surfaced:

- Which path wrote the 59 admission decisions that `foldGovernance` cannot see,
  and whether `cmd_admission_v2` supersedes it or parallels it (§5.5.8).
- Whether the evidence/proof lifecycle is wired up or its events removed
  (§5.5.2) — deferred to the §4.0 mapping for object 4.
- Whether the historical staleness-assessment feature is wanted at all (§5.5.4).
- What object `revoked` revokes. The owner's audit notes that receipt revocation
  and task revocation are different subjects; §5.4's ruling settled that a task
  phase stays, not what its producer would be revoking.

Settled since round 1: `revoked` remains a durable task phase (§5.4, ruled — its
producer's *subject* is not settled). The §7 `scope_verified` ownership ruling is
**partly undercut** by §4.0.2(iii): the blocked-result path and the admission
path were not competing for one slot, so "sole owner" answered a question that
may not have been the right one. The 10/8 partition is **withdrawn** (§4).

Still open, and listed in §12:

- Whether `migration_executed` is deleted or repurposed as the era-adoption event
  (§5.5.3 / §7.2(b)).
- Whether `task_marked_stale`'s producer is built in Unit 1 or the event is
  deleted (§5.5.4).
- Whether the era-adoption pass is applied to the 25 existing chains or they are
  left ungoverned for their remaining life (§7.2).
- **C-1**: `proving` has no terminal once `proving → abandoned` is removed
  (§4.1.6). Re-open in the result lifecycle, where certification already has
  `blocked`/`uncertifiable`/`stale` verdicts no task phase mirrors.
- **C-3**: `foldGovernance` returns `waiting_governance` for all 25 real chains
  while typed decisions sit beside them unread (§5.5.8). The highest-value
  measured gap in this packet, and not a phase problem.
- **C-2**: `common.schema.json`'s event enum is three short of Go's
  (§5.5.7). Reconciled in M7, by a parity check rather than by hand.
- Anything in Units 2–6. In particular Unit 1 does **not** repair R5-2, R5-3, or
  R5-4; those belong to Units 2, 3, and 4.
- Whether PR #350 is ever revived. This packet assumes it stays parked and that
  abandonment is rebuilt as Unit 5.

## 11. Provenance

All code facts read from a clone verified clean at `42475333`; the owner's
working clone does not contain that revision.

- §2.1 from exhaustive `TaskPhase:` payload assignments across
  `golang/architecture`, cross-checked against `ClassifyNextState`'s two return
  sites.
- §2.2 by restricting `AllowedTaskTransitions` (`vocabulary.go:218-238`) to §2.1's
  five phases.
- §2.3 by searching each of the 20 `LedgerEventTypes` for a non-test producer;
  five have none, two of those have consumers.
- §2.4 from `tasksession/session.go:52-70` and
  `resultrecording/payload.go:14-20`.
- §5.1–5.3 from `tasksession/governance.go:118-152`, `session.go:552-570`,
  `session.go:1538-1546`.

### 11.1 Round-2 code measurement (2026-09-10)

The round-1 clone was gone; the tree was re-cloned and re-verified clean at
`42475333dd22165c12f144491fcb1bc22760096c` before any measurement.

- §4.1.1's `AllowedTaskTransitions` comparison read directly from
  `closureprotocol/vocabulary.go:218-237`. The claim that `certified → abandoned`
  is absent today is from `vocabulary.go:230`.
- §4.1.2's two views were **generated** from the canonical edge list by script,
  not transcribed, and the script asserts set equality between the forward and
  reverse projections. It reports `reciprocal: True | edges: 14 | ordinary: 12 |
  governed: 2`. The edge list and generator are session scratch, not repository
  artifacts; M1 makes the generator a repository test (D4).
- §4.1.5's certification precedent from `certification/ledger.go:131-135`; the
  decision vocabulary from `admission/admission.go:44-48`.
- §5.5.1 by resolving each of the four Go identifiers across the whole tree. Each
  appears only in its `vocabulary.go` declaration and its `LedgerEventTypes`
  entry, except `LedgerEventTaskMarkedStale` (`abandon_test.go:859,862`).
- §5.5.2 from `certification/source.go:151-185`, `request.go:113-129`,
  `lanes.go`, `errors.go:63-64`, and the importer sets of
  `golang/architecture/evidencereceipt` (one: `certification/lanes.go`) and
  `golang/architecture/proofdischarge` (one non-test: `certification/*`).
- §5.5.3 from `questiondisposition/matrix_test.go:249-262`,
  `closureprotocol/model.go:493`, `validate.go:501`,
  `rdf/closure_protocol_drift_test.go:60`, `ontology/awareness.ttl:388`.
- §5.5.4 from `completion/abandon.go:469-497` and
  `completion/abandon_test.go:844-900`.
- §7.2(a) from `ledger/model.go:12-22`, `append.go:99`, `verify.go:129`, and an
  exhaustive search for read sites of `HeadSchemaVersion` (none).
- §7.2(b) from `ledger/legacy.go:19-77`.
- §5.5.7 by comparing `LedgerEventTypes` (`vocabulary.go:194-215`) with
  `docs/schemas/architectural-closure/v1/common.schema.json`'s
  `ledger_event_type` enum.

### 11.2 Round-2 corpus measurement (2026-09-10)

Read-only, over the working copies of both repositories — `globulario/sensei` at
`3b6e710b` and `globulario/sensei-code` on
`fix/readiness-checks-query-the-configured-address`. Nothing was written.

- Population: every directory matching `.sensei/tasks/*/ledger/` in both trees.
  25 ledgers, 212 files, 25 of them `HEAD.yaml`, leaving **187 entries**.
- Event-type census by entry filename, which encodes the type
  (`ledger.ledgerEntryFilename`), cross-checked against the `event_type` field of
  sampled entries.
- Phase census by searching every file under every task directory for
  `task_phase`: **0 occurrences**. The same search for the four measured event
  names: **0 occurrences**.
- Admission-decision census over `find … -path '*/admission/decision.yaml'`: 59
  files, exactly one `decision:` each.
- Receipt census over every `receipts/` directory: only `prepare-change.yaml`
  (59) and `task-status.yaml` (59), plus one
  `bootstrap-direction-consumption.yaml`.

**Limitation.** This corpus is the two development repositories on one machine.
It is the population any migration written today would meet, and it is not
evidence about ledgers elsewhere. The §7.2 recommendation is written to be
correct regardless of corpus contents; only the "cheaper and loses nothing
measurable" clause depends on this census.

### 11.3 Round-3 verification (2026-09-10)

The owner's audit claims were re-verified independently against the same clone at
`42475333`, read-only, before this revision was written. All checked out; two are
stronger than stated.

| Claim | Verified at | Result |
|---|---|---|
| `TaskPhase` is optional; events may establish facts by artifact | `ledger/event.go:22,26` | confirmed — `task_phase,omitempty` beside `Artifacts map[string]LedgerPayloadRef` |
| `foldGovernance` reconstructs state from receipts, not from the phase field | `tasksession/governance.go:100-175` | confirmed — indexes `latest[eventType]`, decodes four typed artifacts, reads no `task_phase` |
| `admitted` reported for both available and consumed capability | `governance.go:162-175` | confirmed — same `Phase`, differing `Status` and `GrantModify` |
| `scope_verified` is terminal for mutation and the entry to result recording | `governance.go:128-137`, `advance_result.go:182-183` | confirmed — `Terminal: true`, then `case disp.Terminal: return advanceAtScopeVerified(...)` |
| blocked result stays `scope_verified` while reporting a blocker | `governance.go:134-136`, `resultrecording/classify.go:60-100` | confirmed — a present-but-unverified `ScopeVerification` yields `waiting_mechanical_repair` |
| `proofdischarge.Discharge` exists, no production caller | `proofdischarge/discharge.go:20` | confirmed, **and stronger** — zero callers anywhere except `discharge_test.go` |
| evidence-receipt construction exists | `runtimeprobe/receipt.go:15` | confirmed, **and stronger** — `ToEvidenceReceipt` has no caller outside its own package |
| CLI reaches the admission recorders | `cmd/awg/cmd_admission_v2.go` | confirmed — sole non-test caller of `RecordAdmissionDecided`, `RecordAdmissionConsumed`, `RecordScopeVerified`, `RecordChangeObserved` |
| `workflow.completed` means a change was admitted | `sensei-code internal/event/event.go:124-127` | confirmed, verbatim, including the `workflow.observed` rationale |

Additional round-3 measurement, not from the owner's audit:

- `foldGovernance` has **no filesystem fallback**: it opens only the verified
  chain and the artifacts its entries reference (`governance.go:81-105`). It never
  reads `admission/decision.yaml`. Combined with the §11.2 census — no chain
  contains `authority_resolved` — this yields §5.5.8.
- `certification/ledger.go:131-135` gates the append on the verdict, which is the
  existing precedent §4.1.5 generalises. Unchanged by round 3.

**What round 3 did not do.** No live-ledger inspection beyond the §11.2 census;
no re-examination of PR #350; no attempt to establish whether an external caller
has ever emitted one of the five events, which source evidence cannot settle.

### 11.4 Round-4 measurement — the mapping and C-3 (2026-09-10)

Read-only, at `42475333` for source and over both working repositories for
records. Nothing was written, executed or modified.

**Mapping (§4.3).** Each row's writer, reader and enforcement boundary resolved by
exhaustive call-site search from the named symbol. The four `governanceDisposition`
call sites are complete: `control.go:316`, `session.go:571`,
`advance_result.go:170`, and the definition. `projectControlStatusAndClosure`
(`control.go:553-647`) is absent from that list, which is the basis of G3.

**C-3 population.** `find`-based census of `*/admission/decision.yaml` (59) and
`*/control/generations/*/admission-decision.yaml` (65). Fields parsed from the
YAML by indentation. *One defect found and fixed mid-measurement*: the first
field regex was `[a-z_]+`, which silently dropped every key containing digits —
including `graph_digest_sha256` — and produced "0 of 59 bindings match." The
corrected regex `[a-z0-9_]+` gives 59 of 59. The false result was caught because
zero was implausible, not because anything checked it; a parser that silently
drops fields it cannot name is the same failure shape this packet is about.

**Authoritativeness** determined by the supersession rule read from
`control.go:604-616`, `briefing.go:106-108` and `currentControlPaths`
(`control.go:873-897`): the prepare-time decision is authoritative iff
`control/latest-generation.yaml` is absent; otherwise the generation that pointer
names is.

**Reachability.** For each of the 25 chains, every `digest_sha256` in every entry
was collected, each entry's payload artifact was opened and its nested
`digest_sha256` values collected, and the union compared against the artifact
store. 348 stored, 344 reachable, 4 orphaned.

**Cached projections** read directly from `<task>/control/latest.yaml`
(`permission.modify`, `permission.inspect`).

**Not done, and therefore not claimed:** no CLI or MCP tool was invoked; no
verification was run against a live worktree; whether the seven `modify: admitted`
projections would be served today is **unknown**, and §5.5.8 marks it so. Nothing
establishes which binary wrote them or when — only that a guard now exists whose
own comment says the behavior changed.

### 11.5 Round-5 falsification (2026-09-10) — method and read-only proof

**What was executed.** A temporary observation harness compiled into a throwaway
clone of `globulario/sensei` at `42475333`, calling, per task: `ledger.Store.Verify`,
`verifySession` (with a nil pointer and with the active pointer),
`admission.LoadDecision`, `governanceDisposition`, `LoadTaskControl`,
`ControlStatus`, `BuildTaskBriefing` and `Status`. `governanceDisposition` is
unexported, which is why the harness had to live inside the `tasksession` package
rather than call the public surface only.

**Run twice.**

1. Against a copy of `sensei-code/.sensei` in the session scratchpad.
2. Against the **live** `sensei-code` working repository — because run 1's sandbox
   had no git repository, and two of the observed errors (`repository revision
   changed`, `working_tree_outside_envelope`) could have been artifacts of that.
   Both runs produced identical results for every row, so they were not.

**Read-only, proven three ways.**

- The harness hashes every file under the repository root before and after; both
  runs reported `read-only=true` (identical digests).
- Independently, `sha256sum` over every file in the live `sensei-code/.sensei`
  tree before and after run 2: `04f6715ace7b…` both times.
- `git status --porcelain` on `sensei-code` unchanged at 4 entries (the three
  audit documents and this packet), before and after.

**Controls.** C1 and C2 were seeded with the repository's own fixture,
`internal/resulttestkit.Seed`, into a Go `t.TempDir()` — no repository was
touched. C1's chain contents are listed in §5.5.9.

**Cleanup.** The three harness files were deleted; the clone reports
`git status --porcelain` empty at `42475333`. Nothing was added to either
repository, no PR was touched, no projection was refreshed, no record migrated.

**A defect found in my own instrument, and what it cost.** Round 4's YAML field
regex was `[a-z_]+`, which silently dropped every key containing a digit —
including `graph_digest_sha256` — and reported "0 of 59 bindings match." It was
caught only because zero was implausible. Recorded because it is the same shape as
the findings themselves: a reader that cannot name a field treats it as absent,
and absence reads as a clean negative result.

**Not done.** No task exists in either repository that is simultaneously
verification-clean and governance-unresolved with a positive file decision.
Constructing one requires a repository clone checked out at a task's bound
revision with that task inside it; that is beyond a read-only observation of
existing state and was not attempted. §5.5.9 records which cell that leaves
unobserved and what it would decide.

### 11.6 Round-6 G3 diagnostic (2026-09-10) — method

**Fixtures, all disposable.** Built in Go `t.TempDir()` directories by a
temporary test compiled into the throwaway `42475333` clone. No file in either
working repository was read for state, changed, or migrated;
`sensei-code/.sensei` hashed `04f6715ace7b847a…` before and after, and
`git status --porcelain` stayed at its four pre-existing entries.

**Built with production APIs**, per the instruction:

| Step | Call |
|---|---|
| repository with authority policy sources + graph | `authorityRepo` (existing test helper; copies the real `docs/awareness/*.yaml` policy files) |
| enrolment | `identity.Enroll` |
| task creation | `tasksession.Prepare` |
| typed decision | `admission.DecideAdmission` → `admission.RecordAdmissionDecided` |
| consumption | `admission.ConsumeCapability` → `admission.RecordAdmissionConsumed` |
| projections / pointer | `ledger.RebuildProjections`, `WriteActivePointer` |
| F1's positive decision file | `admission.WriteCanonicalDecision` — the call `sensei admit-change --output` makes |

**Manually constructed input, stated explicitly:** exactly one — F1's decision
*values* (`decision`, `mutation_capability`, `inspection_capability` set to
`admitted`). Everything else about that record, including its digest, was
produced by the production writer. Two earlier attempts to construct F1 by
editing YAML were **rejected by the system** (`decision digest invalid`; `task
control digest mismatch`), which is itself reported in §5.5.10.

**Assertions required by the instruction, all performed:** binding verification
was asserted to return zero errors in every fixture before any permission was
compared; both public read paths were exercised (`ControlStatus` with
`forceRebuild=false` for the cache branch, `ResolveControlAndClosure` with
`forceRebuild=true` for the recompute branch); the branch that supplied each
answer was recorded by comparing the returned permission and scope against the
persisted cache; and each was compared against `governanceDisposition` evaluated
over the same ledger state.

**Not done.** No candidate was generated, no mutation applied, no PR touched. The
execution-boundary claim is a source trace of `cmd/awg/cmd_synthesis_run.go` and
`cmd_synthesis_apply.go`, as the instruction permits.

**Harness disposition.** Deleted; the clone reports `git status --porcelain`
empty at `42475333`. A copy of the diagnostic source is retained in the session
scratchpad only, so the run can be reproduced without reconstructing it.

## 12. Approval checklist

Rewritten 2026-09-10 (round 4). The packet no longer proposes a model. It records
a completed mapping, four measured gaps, and what each would require.

**Delivered this round (7):**

- [x] §4.5 the operation-state decision table replacing `permission.modify`, with
      `GrantModify` split into `CanConsumeCapability`, `CanApplyBoundOperation`,
      `CanObserveChange`, `CanVerifyScope` — each closed-typed and ledger-owned.
- [x] §4.5.4 fixture reclassification: F1 proves **over-authorization**; F2 proves
      **disagreement** whose direction depends on whether `modify` means consume
      or apply; F3 **does not test** consumption enforcement.
- [x] §4.5.5 `validateCurrentBinding` accepting `waiting` recorded as G3.5.
- [x] §5.5.11 the synthesis-boundary trace: per-command actions, the durable
      binding values, and the four missing conjunctions G3.1–G3.4.

**Delivered round 5:**

- [x] §5.5.9 the observed truth table for the seven cached permissions plus the
      eighth, with controls C1 (governed chain) and C2 (deliberately stale), run
      twice and proven read-only three ways (§11.5).
- [x] §4.4 Unit 1's likely design rule — single predicate authority.
- [x] The three round-5 qualifications: category 2 renamed, G1 restated as a
      missing contract, G4 split into G4a and G4b.

**Delivered round 4:**

- [x] §4.3 the five-lifecycle authority mapping, complete, all eight fields per
      lifecycle (L1 workflow, L2 task, L3 operation, L4 result, L5 validity).
- [x] §4.0.1 the five lifecycles with their primary truth and the four bridges.
- [x] §5.5.8 all 124 admission-decision records classified by producer path,
      schema/version, identity, disposition and chain reachability, with the four
      totals and the positive-decision capability analysis.
- [x] §11.4 provenance, including the parser defect found and corrected
      mid-measurement.

**The four totals (§5.5.8), for the record:**

| # | Category | Total |
|---|---|---|
| 1 | correctly referenced canonical decisions | **0** |
| 2 | same-directory migration candidates (not auto-appendable — G2) | **25** |
| 3 | legacy or superseded, requiring migration | **99** |
| 4 | unrelated or provenance-unknown | **0** |

**The four gaps (§4.3.1). None is a missing phase.**

- [ ] **G3.1** — `synthesis-apply` does not prove the ledger capability was
      consumed. No `ledger` reference exists in that file.
- [ ] **G3.2** — it does not attribute a consumption to this task and decision
      (vacuous while G3.1 holds).
- [ ] **G3.3** — it never consults `change_observed`; its idempotence is
      per-candidate-store, not per-operation.
- [ ] **G3.4** — it does not re-verify the ledger head immediately before mutating.
- [ ] **G3.5** — `validateCurrentBinding` refuses only the literal `refused`, so
      `waiting` passes. Its replacement must require the exact positive action
      capability the command needs (§4.5.5).
- [ ] **G3.6** — the single-use guarantee has two authorities that never meet: the
      ledger's `capability_consumption` and the candidate store's `.o5b-claim` /
      `.o5b-receipt.json` (§5.5.11).
- [ ] **G3 — conflicting authority. DEMONSTRATED** (§5.5.10), not presently
      exploitable (§5.5.9). Both directions proven on healthy bindings; execution
      boundary traced (`synthesis-run` → `validateCurrentBinding`;
      `synthesis-apply` re-reads the same file authority). §4.4's rule is
      sufficient to repair it — **no lifecycle redesign required**. *Awaiting a
      ruling on repair scope; no proposal written.*
- [ ] *(superseded framing, retained)* **G3 — LATENT, not live** (§5.5.9). Two writers
      establish L3's mutation predicate and `projectControlStatusAndClosure`
      consumes the ungoverned one, but **no public surface serves a grant today**:
      all eight tasks are refused, suppressed by staleness — an L5 property, not an
      authority guarantee. Seven `control/latest.yaml` files still assert
      `modify: admitted`. *Meets the bar for a proposal; none made.* §4.4 records
      the rule a proposal would implement.
- [ ] **G4a — missing production invocation.** `proofdischarge.Discharge` and
      `runtimeprobe.ToEvidenceReceipt` have no production caller. *Meets the bar
      for a proposal; none made.*
- [ ] **G4b — missing ledger integration.** No emitter or consumer for
      `evidence_recorded` / `proof_discharged`. **Independent of G4a** — either can
      be closed without the other.
- [ ] **G1 — missing bridge *contract*.** A local workflow may legitimately end
      without closing the governed task. The gap is that no contract says whether
      propagation is required. Not a defect until it does.
- [ ] **G2 — identity insufficiency.** L3 decision records carry no task id; 42 of
      59 share a binding triple with another task. Needs its own measurement
      first.

**Reopened / held this round:**

- [~] §7 `scope_verified` ownership — **reopened.** The real question is whether
      `resultrecording/classify.go:92` *establishes* the predicate or *projects*
      it. Multiple readers are legitimate; multiple authorities are the conflict.
- [~] §5.4 `revoked` producer — **held.** Four candidate subjects (task, result,
      evidence/receipt, capability); `RevocationReceipt.RevokedTargetID` is generic
      and cannot settle it. No writer until the subject is explicit.

**Standing withdrawals, unchanged from round 3:**

- [~] §4 the 10-durable / 8-derived partition — withdrawn. It compressed several
      lifecycles into one enum and would likely have weakened authority.
- [~] §7.1 M3 (Group B writes phases) — withdrawn.
- [~] §5.5.2 delete-the-events recommendation — restated as G4.
- [~] §5.5.4 build the `task_marked_stale` producer — withdrawn; current staleness
      is an L5 claim and is already computed live.

**Standing rulings that survive the correction:**

- [x] §5.1–5.3 `refused`, `stale`, `uncertifiable` are not durable *terminal*
      phases — now for the mapping's reason: they are L5 claims.
- [x] §7.2 "missing phase means legacy" is refused as an era-detection rule.
- [x] §9 D5 / A1–A5: every enforcement test requires a mutation proving it can
      fail.

**Next step, and the only thing this packet asks for:**

- [ ] Rule on whether G3 and G4 proceed to an implementation proposal, separately
      and in that order. G3 is a live authority conflict on the surface an agent
      reads as its permission; G4 is an unwired lifecycle with no production
      producer. Neither proposal is written here.
- [ ] Authorize (or not) the two follow-up measurements G1 and G2 require.

**Blocked:**

- [ ] Independent review of this model — **blocked by B1**, unchanged.

**Standing:** this packet is committed (see Status); the companion assessment
remains uncommitted; PR #350 stays parked at
`42475333`; sensei-code#167 unchanged; no Unit 1 code begins; no thread activity,
no publication, and no F5 mutation accompanies this revision.
