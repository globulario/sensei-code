# Sensei Code Contract List v1

**Status:** M0 deliverable — first contract list
**Audience:** Sensei Code implementers, Sensei maintainers, reviewers
**Companion:** [`architecture.md`](architecture.md)

Verified against `globulario/sensei` at `f8a4e763`, binary `sensei version` → `0.0.1-dev`.

---

## 1. Purpose

`architecture.md` states which concepts Sensei owns. This document states, for each of
those concepts, **the exact surface Sensei Code calls, the exact document it receives, and
whether that surface exists today.**

It exists to answer one question before any Go is written:

> Can Sensei Code be built against Sensei's public contracts as they stand, and where can it not?

The short answer: **the workflow is buildable through candidate-ready (M4). It is not
buildable through admission-and-apply (M5) without Sensei-side work.** Section 8 records
the gaps precisely.

---

## 2. How to read a contract entry

Each contract has:

| Field | Meaning |
|---|---|
| **ID** | Stable reference (`C1`…`C12`) used by implementation briefs and tests |
| **Owner** | Always Sensei. Listed to name the producing package |
| **Surface** | CLI command, MCP tool, or gRPC call Sensei Code invokes |
| **Transport** | `graph` (requires `sensei serve`), `offline` (file-based), or `mcp` |
| **Document** | Canonical `schema_version` constant Sensei Code pins |
| **Binds** | Fields Sensei Code must carry forward as exact identity |
| **Status** | `available` / `partial` / `absent` |

### 2.1 Rules that apply to every contract

**R1 — Pin the constant, reject the unknown.** Every Sensei schema declares
`schema_version` as a JSON Schema `const`, not a range. Sensei Code pins the exact
constant and **refuses** a document whose `schema_version` it does not recognize. It never
partially interprets one. This is Sensei's own stated consumer rule, quoted in
`workspace-identity-v1.schema.json`.

**R2 — Digests are the join key.** Contracts reference each other by
`*_digest_sha256`, never by embedding. Sensei Code's session record stores digests and
resolves documents on demand. A digest that fails to resolve is an error, not a blank field.

**R3 — Never re-derive.** Where a Sensei document reports a state
(`composition_state`, `disposition`, `coverage_state`), Sensei Code renders that value
verbatim. It computes no summary state of its own. (`architecture.md` Law G, Law L.)

**R4 — Structured only.** No contract is satisfied by parsing human-oriented text output.
Where a command has no structured mode, that is recorded as a gap, not worked around.

---

## 3. Readiness and identity

### C1 — Workspace identity

| | |
|---|---|
| **Surface** | MCP `sensei_workspace_status` |
| **Transport** | mcp |
| **Document** | `sensei.workspace.identity.v1` |
| **Status** | available |

The canonical answer to "is this repository governable right now."

Binds: `composition_state` (`complete` \| `partial` \| `unavailable`), `binding`,
`repository_domain_source` (`configured` \| `unbound`), `graph_authority`,
`coverage_state` (`COVERAGE_STATE_UNSPECIFIED` \| `EMPTY` \| `THIN` \| `SUFFICIENT`),
`task_identity`, `limitations[]`.

The schema's own `$comment` states that repository-root hash, MCP session id, worktree id,
job id, and provider id are **deliberately not Sensei's facts** — a runner binds and
compares its own copies against this receipt. That runner is Sensei Code. Those five fields
are the legitimate contents of Sensei Code's local run record, and the only ones.

`limitations[]` is load-bearing. It is the mechanism by which Sensei says "ready, but not
for that." Sensei Code renders it; it never collapses a non-empty `limitations` into green.

### C2 — Graph coverage and freshness

| | |
|---|---|
| **Surface** | `sensei metadata --json [--domain <d>] [--addr <host:port>]` |
| **Transport** | graph (default `localhost:10120`) |
| **Status** | available |

Distinguishes "no rules apply here" from "this area is unannotated" — the difference
between an empty briefing that means safety and one that means ignorance. Sensei Code must
never present an empty C3 result without the C2 coverage state beside it.

### C3 — Onboarding path

| | |
|---|---|
| **Surface** | `sensei init`, `import`, `bootstrap`, `build`, `serve`, `report`, `repo-eval --json` |
| **Status** | available |

Invoked only on explicit user action. `architecture.md` §10 already forbids silent
bootstrap; this contract adds that Sensei Code presents the command it intends to run and
requires confirmation, because each of these mutates governance sources.

---

## 4. Architect context

### C4 — Briefing, impact, preflight, proof plan

| | |
|---|---|
| **Surface** | `sensei briefing --json`, `impact --json`, `preflight --json`, `query`, `resolve`, `proof-plan`, `edit-check` |
| **MCP** | `awareness_briefing` (`depth: agent_compact\|compact\|standard\|deep`), `awareness_impact`, `awareness_preflight`, `awareness_query`, `awareness_resolve`, `awareness_edit_check` |
| **Transport** | graph / mcp |
| **Status** | available |

This is the architect context packet of `architecture.md` §13.2, and it is fully available
today. `awareness_preflight` returns risk class, required actions, forbidden fixes, and
tests to run for a task — the four things the architect proposal must bind.

Sensei Code assembles the packet from these calls **and records which calls returned
unavailable**. Per `architecture.md` §13.2, absence is never rendered as "none."

The MCP path and the CLI path must not be mixed within one packet: they are the same
owner but different freshness moments. Pick one transport per packet and record it.

---

## 5. Task session

### C5 — Task creation and control state

| | |
|---|---|
| **Surface** | `sensei prepare-change --repo-domain --description --mode inspect\|modify --task-class --risk-class --direction preserve\|evolve\|migrate\|not_applicable\|unknown --graph-nt --file <read\|modify>:<path> --format json` |
| **Read** | `sensei task-status [--active\|--task <dir>] [--verify] --format json`; MCP `task_status`, `task_briefing`, `advance_task` |
| **Transport** | offline (writes `.sensei/tasks/<task-id>/`, `active.yaml`) |
| **Status** | available |

`prepare-change` is the **required predecessor of C6** — `synthesis-run` creates no task.

The `--file <operation>:<path>` list is where the architect's proposed scope becomes a
machine-checked boundary. This is the single most important handoff in the product: the
architect's structured output (`architecture.md` §13.3, "proposed scope" / "out-of-scope
items") maps directly onto these flags, and everything downstream — admission scope,
verification compliance — is evaluated against them. An architect proposal that cannot be
expressed as `--file` entries is not yet a plan.

`task-status --verify` checks pointer, session digest, graph digest, revision, and artifact
references. Sensei Code calls it with `--verify` before every state transition, not once at
open. This is the drift detection required by `architecture.md` §17.1.

`advance-task` executes only `static_read` probes — never commands, tests, network, runtime
reads, or mutation. It is safe to call from the UI. Nothing else in this list is.

---

## 6. Governed synthesis (O1–O4)

### C6 — Synthesis driver

| | |
|---|---|
| **Surface** | `sensei synthesis-run --interpretation <path.json> --agent codex\|claude --agent-command <abs path> --format json [--agent-env NAME] [--agent-workdir <dir>] [--candidate-store <dir>] [--evidence-store <dir>] [--deadline-minutes N] [--gate-policy <path>] [--addr <host:port>]` |
| **Transport** | graph |
| **Input** | `sensei.synthesis.interpretation.v1` |
| **Output** | `sensei.synthesisdriver.receipt.v1` |
| **Status** | available |

Receipt binds: `final_phase` (`created` \| `planning` \| `planned` \| `attempting` \|
`evaluating` \| `retry` \| `replan` \| `succeeded` \| `failed`), `disposition`
(`candidate-ready` \| `terminal-failure` \| `provider-stopped` \| `runner-stopped` \|
`step-limit-reached`), `step_count`, `session_digest_sha256`,
`candidate_artifact_digest_sha256`, and digest lists for O2 / runner / evaluation receipts.

Those five dispositions map onto `architecture.md` §24's failure classes and should be
carried through unflattened. `provider-stopped` and `runner-stopped` are different facts
and must not both render as "worker failed."

**The command never admits, applies, commits, pushes, or merges.** A sealed candidate is a
proposal on disk. Sensei Code's UI must state this at the point the run completes, because
`succeeded` / `candidate-ready` reads like completion and is not.

**Escape hatches.** `--force-thin-coverage` and `--force-unconverged` proceed past
governance preconditions. Sensei Code must never pass either without an explicit,
per-invocation human action, and must record their use in the session event log and in any
support bundle. A configuration file may not set them — this is the `architecture.md` §26
prohibition applied to a real flag.

### C7 — Provider port

| | |
|---|---|
| **Owner** | `golang/architecture/{providerport,runnercomposition}` |
| **Document** | `sensei.providerport.capabilities.v1`, `.request.v1`, `.result.v1`, `.receipt.v1`, `.observationbatch.v1` |
| **Status** | available |

**This contract changes an architecture decision.**

`architecture.md` §8 proposes `internal/provider/{claude,codex,cursor}` inside Sensei Code,
and §14/§25 give Sensei Code ownership of process spawn, env allowlist, workdir binding,
and process-group cancellation. But C6 shows Sensei already owns all of that: it takes
`--agent codex|claude`, requires an absolute `--agent-command` with no PATH lookup, has its
own `--agent-env` allowlist, binds `--agent-workdir`, and enforces a deadline — behind a
typed, schema'd provider port with a declared capability document.

Building `internal/provider/` as designed would create **a second implementation of the
worker boundary**, enforcing Law D twice with two different sandboxes. That is precisely
the "weaker private version" §3.1 forbids, applied to execution rather than to knowledge.

**Recommendation:** Sensei Code does not spawn Claude Code or Codex for governed runs. It
detects them, verifies native authentication, and passes their absolute paths to C6. Its
provider layer becomes *detection and readiness*, not *process lifecycle*. Sensei Code's
own process runner survives only for ungoverned work — the exact-SHA reviewer checkout and
manual sessions (§22.1).

Sensei Code should consume `sensei.providerport.capabilities.v1` as the source of the
capability record in §14, rather than defining the parallel list that section proposes.

### C8 — Candidate artifact

| | |
|---|---|
| **Document** | `sensei.runnercomposition.candidateartifact.v1` |
| **Status** | available |

Binds: `repository_domain`, `base_revision`, `workspace_identity_digest_sha256`,
`session_digest_sha256`, `plan_digest_sha256`, `plan_generation`, `attempt_number`,
`input_candidate_digest_sha256`, `proposed_change_digest_sha256`,
`final_candidate_content_digest_sha256`, `manifest[]`, `candidate_artifact_digest_sha256`.

`candidate_artifact_digest_sha256` **is** the lane identity of `architecture.md` §16.1 and
the only value eligible to flow into an admission request. Sensei Code invents no candidate
id of its own.

### C9 — Deterministic evaluation

| | |
|---|---|
| **Document** | `sensei.evaluatorcomposition.evaluationreceipt.v1` (+ `evaluationinput`, `evaluationpolicy`, `evaluatordescriptor`, `evaluatorresult`) |
| **Status** | available |

Binds `disposition` (`invalid-output-terminated` \| `candidate-load-failure` \|
`materialization-failure` \| `required-evaluator-unavailable` \| `composition-failure` \|
`evaluated`) and `candidate_artifact_verified`.

Note that `evaluated` is the only non-failure disposition and it does **not** mean "passed."
Pass/fail lives in `evaluator_result_bindings`. Rendering `evaluated` as a green check is
the exact §20 error this product exists to prevent. `required-evaluator-unavailable` is an
infrastructure fact and must never surface as a candidate quality judgment
(`architecture.md` §24, final line).

---

## 7. Admission, apply, closure

### C10 — Admission decision and scope verification

| | |
|---|---|
| **Surface** | `sensei admit-change --bundle <dir> --request <request.yaml> --graph-nt <graph.nt> --repo <checkout> [--policy admission.strict.v1] [--output <decision.yaml>] [--require-admitted] [--require-write-admitted] --format json` |
| | `sensei verify-admission --decision <decision.yaml> --bundle <dir> --repo <tree> [--require-compliant] [--output <verification.yaml>] --format json` |
| | `sensei admission-status --decision <d.yaml> [--verification <v.yaml>] --format json` |
| **MCP** | `admit_change`, `verify_admission`, `sensei_workspace_admit_change`, `sensei_workspace_verify_admission` |
| **Transport** | offline |
| **Document** | `architectural-closure/v1/admission-decision`, `admission-request`; projected as `sensei.workspace.admission.v1` |
| **Status** | available — but see G1 |

Decision binds: `decision_id`, `request_digest_sha256`, `policy_id`, `operation_verdicts[]`,
`capability_id`, `capability_expiry`, `risk_budget`, `operation_budget`,
`required_proof_slots[]`, `required_evidence_profiles[]`, `required_result_rebuilds[]`,
`completion_policy_id`.

Two facts Sensei states plainly and the UI must repeat:

- *"Admission is permission to attempt, not proof of correctness."*
- *"Scope compliance is not correctness certification."*

`capability_expiry` means an admission decision goes stale. Sensei Code must check it before
apply and treat expiry as a distinct failure class, not as refusal.

`sensei.workspace.admission.v1` carries `correctness_certified` verbatim from the admission
owner and its schema states it is **never inferred** from admission, tests, CI, or scope
compliance. Sensei Code propagates that field untouched.

`admission-status` is offline and graph-free — it re-verifies receipt digests with no server
and no repository. It is the right call for rendering a resumed session's admission state.

### C11 — Governed candidate apply (O5B)

| | |
|---|---|
| **Owner** | `golang/architecture/candidateapply` |
| **Document** | `sensei.candidateapply.request.v1`, `sensei.candidateapply.receipt.v1` (Go constants) |
| **Status** | **absent — no invocable surface** (G2) |

The package is implemented and does exactly what `architecture.md` Law E requires: applies
only an admitted sealed artifact to a clean dedicated worktree at the admitted base
revision, dispositions `applied` \| `verification-recorded`, no commit / push / merge.

Its `Request` binds `admission_composition_request_digest_sha256`,
`admission_composition_receipt_digest_sha256`, `admission_decision_digest_sha256`,
`candidate_artifact_digest_sha256`, `input_candidate_digest_sha256`,
`final_candidate_content_digest_sha256`, `proposed_change_digest_sha256`, `modify_paths[]`
— the complete four-way binding needed to prove the applied tree is the reviewed tree.

But no CLI command, MCP tool, or other package imports it, and no JSON Schema is published
for it under `docs/schemas/`. See G2.

### C12 — Proof, closure, completion

| | |
|---|---|
| **Surface** | `sensei verify-obligations --json` (consumes `go test -json`), `assess-closure --format json`, `certify-change --format json`, `complete-task --format json`, `inspect-terminal`, `evidence --json` |
| **Gates** | `sensei gate`, `impact-gate`, `merge-check` |
| **Document** | `architectural-closure/v1/{proof-discharge,evidence-receipt,completion-receipt,result-binding,artifact-receipt,certification-receipt,ledger-entry}` |
| **Status** | available |

The required/observed split of `architecture.md` §20.1 already exists as two document
families: `required_proof_slots[]` on the C10 decision versus `proof-discharge` /
`evidence-receipt` records. Sensei Code renders them in separate columns and never joins
them into one badge.

`merge-check` verifies a PR is merge-authorized and never merges — the correct shape for the
§21.3 publication boundary.

---

## 8. Gap register

These are the M0 findings. Each is Sensei-side work that Sensei Code cannot route around
without violating a law in `architecture.md` §32.

### G1 — The O4→O5 seam is unwired *(blocks M5)*

`synthesis-run` persists an admission lineage bundle beside the sealed candidate.
`admit-change` and `verify-admission` take their own separate inputs and **do not read it**.
`admissioncomposition` is imported by `cmd_synthesis_run.go` alone. Sensei's own help text
states it: *"Wiring the lineage bundle into an O5 admission command is a distinct,
not-yet-built step. Until then, review the bundle directly before authoring an admission
request by hand."*

Sensei Code cannot author that request by hand on the user's behalf. Composing an
admission request outside Sensei is exactly the §15 prohibition ("reimplement admission
evaluation") and would let a UI-shaped bug widen a governed scope.

**Needs:** a Sensei command that reads a lineage bundle and emits
`sensei.admissioncomposition.request.v1` — the schema for which already exists, including
`admission_eligible`, `derived_scope`, and `unsupported_operations[]`.

### G2 — O5B has no invocable surface *(blocks M5)*

C11. The apply logic exists; nothing can call it. **Needs:** a CLI/MCP surface plus
published JSON Schemas for `sensei.candidateapply.request.v1` / `.receipt.v1`.

Together G1 and G2 mean the chain `candidate-ready → admission → exact apply → verification`
cannot be driven end-to-end by any external consumer today. Everything before it can.

### G3 — No contract-version negotiation *(blocks §29)*

`sensei version` returns `0.0.1-dev`. No surface reports which `schema_version` constants
this build produces. §29 requires fail-closed negotiation for governed workflows, and R1
requires exact-constant pinning; neither is implementable against a bare version string.

**Needs:** `sensei contracts --json` listing produced schema versions. Small, and it
unblocks every other contract's version check.

### G4 — Inconsistent structured-output flags

Three conventions coexist: `-json` boolean (`metadata`, `briefing`, `impact`, `preflight`,
`evidence`, `repo-eval`), `--format text|yaml|json` (`admit-change`, `synthesis-run`,
`assess-closure`, `certify-change`, `complete-task`), and both plus `-yaml` on `task-status`.
Cosmetic, but every inconsistency is an adapter special case and a place to get it wrong.

**Needs:** normalize on `--format`, keeping `-json` as an alias.

### G5 — Split transport

C2/C4/C6 need a running `sensei serve` at `--addr`. C5/C10 are offline and file-based.
Sensei Code's readiness model must therefore carry **two independent states** — graph server
reachable, and task/bundle artifacts present — because either can fail alone. A single
"sensei: ready" indicator, as sketched in the README's mockup, is not truthful. The mockup
should show both.

### G6 — Provider ownership overlap

C7. Resolve before writing `internal/provider/`. This is a design decision to make now, not
a Sensei defect.

---

## 9. Consequences for the milestone plan

**M1–M4 are buildable today.** C1–C9 exist with structured output. A repository can be
opened, assessed truthfully, given a bounded task, and driven to a sealed, evaluated,
candidate-ready artifact through real Sensei machinery.

**M5 is blocked upstream** by G1 and G2, in a repository Sensei Code does not own.

This inverts the sequencing in `architecture.md` §30. The recommended first move is not M1
but a **throwaway M5 spike**: drive `prepare-change → synthesis-run → candidate-ready`
against a real repo with a stub interpretation, then attempt the admission handoff by hand
and record exactly what is missing. That spike costs days, has no UI, and either confirms
this gap register or corrects it — before any TUI, architect runtime, or provider adapter is
written against assumptions.

The `internal/provider/` question (G6) should be settled by the same spike, since driving C6
means using Sensei's provider port whether or not Sensei Code also has one.

---

## 10. Open questions for Sensei maintainers

1. Are G1 and G2 already planned, and on what horizon? Sensei Code's M5 date is theirs.
2. Is `sensei.providerport.capabilities.v1` intended as a public consumer contract, or an
   internal composition detail? C7's recommendation depends on the answer.
3. Which schemas are frozen and which are still moving? R1 makes Sensei Code brittle by
   design; a stability tier per schema family would let it pin confidently.
4. Should Sensei Code ever surface `--force-thin-coverage` / `--force-unconverged`, or should
   those remain maintainer-only CLI flags with no UI path at all?
