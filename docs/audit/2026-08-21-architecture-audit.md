# Sensei Code architecture audit — 2026-08-21

Produced by a governed `/run Audit sensei-code` under Sensei Code itself.

| | |
|---|---|
| Task | `task-1787329104612942353` |
| Session | `session-20260820T205241.970783482Z` |
| Base commit | `6516b4596289` |
| Plan mode | `inspect` — read-only, no repository diff |
| Architect | ChatGPT (GPT-5.6 Sol) |
| Implementor | Claude Opus 5 |
| Authority | Level-3 boundary reached and authorized by the human (option 1) at 16:21:04Z |
| Graph | `github.com/globulario/sensei-code`, authoritative and current, 150,197 triples |
| Outcome | `workflow.completed` — read-only plan completed with no change to the repository |

The findings below are the implementor's, reproduced as written. Severity and
confidence labels are its own. Nothing here has been re-verified by a second
party, and a finding marked Hypothesis has not been reproduced.

The run wrote this file's *contents* but could not write the file: its bounded
plan required no repository diff, so creating `docs/audit/` was outside what it
was authorized to do. That step was carried out separately, which is why this
paragraph exists rather than a claim that the audit filed itself.

---


## Findings, ranked by root contract impact

### 1. [High · Confirmed] A deferred publication decision is recorded as standing *and* terminated in the same run
`offerPullRequest` discards the error from `awaitChoice` ([engine.go:538](internal/workflow/engine.go#L538)). When the human presses Esc at the publication rendezvous, `awaitChoice` emits `WorkflowAwaitingAuthority` ("the question stands") and returns `errAuthorityDeferred`; `offerPullRequest` returns silently, and `implement` then reports success, records the candidate `Retained`, and emits `WorkflowCompleted` ([engine.go:1986–1998](internal/workflow/engine.go#L1986)). `FindInterrupted` treats `WorkflowCompleted` as terminal ([store.go:154](internal/session/store.go#L154), [:181](internal/session/store.go#L181)), so the task is filtered out of `/resume`.

Reproduced via `go test -overlay=` (no file entered the worktree): a task with `awaiting_authority` followed by `completed` yields **0** resumable entries. The TUI has already told the human "deferred · the question stands, /resume asks it again" ([model.go:399](internal/tui/model.go#L399)); `/resume` will answer "nothing to resume" ([model.go:1057](internal/tui/model.go#L1057)). The same shape applies to a stop at that rendezvous: `ctx.Err()` is discarded, no `WorkflowStopped` is emitted, and the run reports success.

A second, related defect: even if the question *were* resumable, `resumeAuthority` answers it and then calls `e.execute(...)` ([engine.go:2147](internal/workflow/engine.go#L2147)) — re-running architecture and implementation from the top. A yes to "open a pull request" would start a whole new task, not publish.

**Contracts:** `invariant:sensei_code.authority.only_an_explicit_answer_satisfies_a_boundary` (critical), `invariant:sensei_code.resume.never_skips_a_human_decision` (critical).
**Repair:** return a typed publication outcome from `offerPullRequest`; on defer or stop, emit no terminal completion and keep the question resumable; make resume of a publication question publish rather than re-execute.
**Proof required:** behavioral tests for defer-at-publication, stop-at-publication, and resume-after-publication-defer.

### 2. [High · Confirmed] Automatic candidate removal reads a stale audit snapshot instead of the candidate
`candidateEvidence` derives `ProducedNoWork` solely from `tc.EvidenceSnapshot` ([engine.go:2027](internal/workflow/engine.go#L2027)), and that snapshot is written only *after* validation and a decodable Sensei audit ([engine.go:905](internal/workflow/engine.go#L905)). Every first-cycle exit before that point — a read-only plan that edited files, a `validate` error, an `awareness_audit_diff` transport error, a `DecodeDiffAudit` contract error — returns with a non-empty diff and a zero snapshot. The terminal `disposeIfEmpty` then takes the removal branch, deletes the worktree and the branch, and records the reason "the candidate holds no work".

Reproduced via overlay: `candidateEvidence` with an unset snapshot returns `ProducedNoWork=true, DiffBytes=0`. The `candidate` package's own guard exists precisely for this shape and is bypassed by the false assertion — `Validate` only demands recorded work when `ProducedNoWork` is *false* ([disposition.go:133](internal/candidate/disposition.go#L133)).

**Contracts:** `invariant:sensei_code.candidate.disposition_is_decided_and_evidence_outlives_removal` (critical); `invariant:sensei_code.outcome.reporting_never_alters_the_result` (the recorded reason is false).
**Repair:** observe the candidate at disposal time (`CandidateDiff` against `BaseSHA`) rather than replay a snapshot; treat an unobservable candidate as holding work.
**Note:** the guarding test `TestOnlyAnEmptyCandidateIsRemovedAutomatically` ([engine_test.go:691](internal/workflow/engine_test.go#L691)) asserts the *order of identifiers* inside `disposeIfEmpty`. It passes through this defect unchanged.

### 3. [High · Confirmed] Publication outcomes are not carried, so lifecycle state can contradict Git and GitHub
`offerPullRequest` returns nothing, and its caller unconditionally records `Retained` — "accepted by review and unpublished" — and emits one `WorkflowCompleted` ([engine.go:1986–1998](internal/workflow/engine.go#L1986)). Therefore a successfully opened PR is still recorded as unpublished; a commit/push/PR failure emits `WorkflowFailed` followed by `WorkflowCompleted`, two terminal events for one run; and a successful push followed by a failed `gh pr create` leaves a published remote branch recorded as unpublished, with the partial effect nowhere ([publish.go:98–103](internal/publish/publish.go#L98)).

Separately, `prURL` falls back to `strings.TrimSpace(out)` ([publish.go:112](internal/publish/publish.go#L112)) — arbitrary command output — directly contradicting its own doc comment ("so a caller never shows a success line that contains no pull request"). That value is emitted as `"pull request opened…: "+url` with a `{"url": …}` payload ([engine.go:554](internal/workflow/engine.go#L554)). Its test covers only the happy extraction ([publish_test.go:48](internal/publish/publish_test.go#L48)).

**Contracts:** `invariant:sensei_code.candidate.disposition_is_decided_and_evidence_outlives_removal`, `invariant:sensei_code.outcome.reporting_never_alters_the_result`, `invariant:sensei_code.evidence.counts_carry_their_provenance`.
**Repair:** return a staged publication outcome (committed / pushed / PR-opened / URL), select the disposition from it, record partial effects explicitly, emit exactly one terminal state, and make an absent URL a failure rather than an echo.

### 4. [High · Confirmed] A read-only run's success condition is "the diff was empty", not the audit
In `ModeInspect` an empty diff returns `accepted=true` immediately ([engine.go:812](internal/workflow/engine.go#L812)) — before validation, before the Sensei audit, before the reviewer, before `emitChangeReport` and `recordDecision`. The worker's own result text is discarded (`if _, err := impl.Run(...)`, [engine.go:798](internal/workflow/engine.go#L798)), and the workflow completes stating findings are "in the transcript" ([engine.go:1971](internal/workflow/engine.go#L1971)).

The worker prompt is also mode-blind: it says "You may inspect, edit, build, and test" and "Implement only the architect's bounded plan" regardless of `tc.Mode` ([engine.go:1700](internal/workflow/engine.go#L1700)); `Mode` never reaches `implementationPrompt`. A worker that edits is caught only after the fact, and via finding 2 its candidate is then deleted.

So an empty, unevidenced, or malformed audit is reported successful, and no governed findings artifact reaches the architect. The three guarding tests here ([engine_test.go:838](internal/workflow/engine_test.go#L838), [:869](internal/workflow/engine_test.go#L869), [:878](internal/workflow/engine_test.go#L878)) are all source-text assertions.

**Contract:** `invariant:sensei_code.report.states_what_it_does_not_establish`; `invariant:sensei_code.evidence.counts_carry_their_provenance`.
**Repair:** preserve the worker result as a structured inspection report, require evidence and limitations, review it independently, and make that verdict the completion condition; tell the worker its mode.

### 5. [Medium-High · Confirmed] A Sensei survey that fails entirely is reported to the human as "the graph holds nothing"
`surveyPlan` drops every per-class query error with a bare `continue` ([assisted.go:161](internal/workflow/assisted.go#L161)) and returns `Surveyed: len(nodes)`. `SurveyOutcome.Describe()` renders `Surveyed == 0` as "the graph holds nothing of the surveyed classes for this domain" ([semantic.go:167](internal/retrieval/semantic.go#L167)) — an affirmative claim about the graph, produced by transport failure.

This is the recorded critical failure mode `failure.sensei_code.empty_sensei_tool_response_accepted_as_present_evidence` and the forbidden fix `forbidden_fix.sensei_code.derive_observation_presence_from_transport_success`, and it contradicts `invariant:sensei_code.sensei_boundary.refusal_carries_its_reason` — which the surrounding assisted path honours correctly everywhere else (workspace status and preflight both state their caveats).
**Repair:** carry failed-query count and reason in `SurveyOutcome`; refuse the affirmative wording when any class query failed.

### 6. [Medium · Strong signal] One human "yes" is reused for later, materially different plans in the same task
`answeredConditions` keys the memo on the routing condition text alone ([engine.go:2384](internal/workflow/engine.go#L2384)), and conditions carry no file list — e.g. `"Sensei requires approval for this change class: human_approval_required (blast radius security)"` ([authority.go:161](internal/workflow/authority.go#L161)). `applyAnsweredCondition` then proceeds without asking ([engine.go:1136](internal/workflow/engine.go#L1136)). A yes given for plan P1 silently authorizes any later plan in the same task reaching the same gate and blast radius, including one touching entirely different files. The recorded `authority.Resolution` already carries the question and the option; the plan scope is simply not part of the key.

**Contracts:** `invariant:sensei_code.authority.only_an_explicit_answer_satisfies_a_boundary` (critical), `invariant:sensei_code.workflow.context_never_widens_worker_scope` (critical).

### 7. [Medium · Strong signal] Credential stripping is Anthropic-only
`SessionOnlyEnv` lists only `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN` ([provider.go:339](internal/provider/provider.go#L339)). The Claude status branch relies on that stripping to claim "the account reported here IS the one a worker authenticates with" ([provider.go:204](internal/provider/provider.go#L204)). The ChatGPT/Codex branch ([provider.go:139](internal/provider/provider.go#L139)) reads stored Codex credentials and makes the same implicit claim with no equivalent stripping; `OPENAI_API_KEY` appears nowhere in the repository. The mechanism gap is proven; I did not verify Codex CLI's env-vs-stored-login precedence, so exploitability is unconfirmed.

**Contracts:** `invariant:sensei_code.provider.credentials_remain_provider_owned`, `invariant:sensei_code.provider.unknown_auth_is_not_connected`, failure mode `provider_auth_state_claimed_from_an_acknowledged_call_instead_of_confirmed_with_the_owner`.

### 8. [Low · Hypothesis] The architect resolution loop has no overall bound
`resolveArchitecture` is `for attempt := 1; attempt <= 2; attempt++`, but the escalate-and-certified path sets `attempt = 0; continue` ([engine.go:1179](internal/workflow/engine.go#L1179)) and rebuilds `prompt = certifiedResolutionPrompt(prompt, d)`, nesting the previous prompt. An architect that keeps returning `escalate` in a region Sensei certifies loops unbounded, spending provider turns and growing the prompt each round. Only context cancellation stops it. The human-answer paths share the shape but are bounded by the person.

## Awareness gaps (not defects)

- **`internal/admission` has no non-test consumer.** No file outside the package imports it, so the canonical chain is argv-constructible and unit-tested but never invoked by the running product. `invariant:sensei_code.admission.the_chain_is_senseis_and_only_it_establishes_admission` holds vacuously at runtime.
- **The checkout-relative field `gate.go` was waiting for now exists.** The live receipt this run published `binding.revision`, `binding.revision_status`, `binding.tree_digest_sha256` and `binding.graph_digest_status`. `sensei.WorkspaceStatus.Binding` decodes only `repository_domain` and `repository` ([contracts.go:481](internal/sensei/contracts.go#L481)), and `certifyStart` still discards `repositoryHead` on the stated grounds that no field answers the question ([gate.go:85](internal/workflow/gate.go#L85)). `WorkspaceStatus.Permits()` also never consults the `graph_authority` block it decodes.
- **Dead surfaces:** `sameCommit` and `short` ([gate.go:107](internal/workflow/gate.go#L107)), `certifiedStart.RiskClass`/`Invariants`/`GraphSourceCommit`, `Coverage.PatternOnly` (only self-referential), `WorkspaceStatus.CoverageState`. The certified start's risk classification reaches no caller.
- **Guarding tests for the highest-severity invariants are structural, not behavioral** — findings 2 and 4 both sit behind AST/source-text assertions that would keep passing.

## Positive boundaries that held

Only `admit-change` establishes admission and `Admitted()` is narrowly scoped to it ([admission.go:198](internal/admission/admission.go#L198)). PR argv contains no merge verb, and push plus commit capabilities are mechanically enforced. The reviewer is excluded from judging its own work by construction, inherits nothing, and binds to a revision. A reviewer accepting over a Sensei `block` does not conclude the candidate. Typed decoders fail closed on missing structured content and unrecognised enum members. The authority router takes no model text as input, and architect-supplied options cannot decide what choosing them means. `answeredConditions` fails closed on a store read error.

**One claim from the earlier audit I am withdrawing:** doctor returning overall OK with a provider at `UNPROVEN` is settled architect intent, not a defect — quoted in `doctor.go:29–42` (question.20de26bd75eab654 / answer.70ebacb875a8a1c5) and locked by `TestWarningsDoNotMasqueradeAsPassButDoNotBlockReadiness`.

## Verification

Sensei workspace receipt: composition `complete`, domain `github.com/globulario/sensei-code` from configured source, revision resolved `6516b459…`, tree digest resolved, graph authority current and stamped, seed current, live store 150,197 triples, coverage `SUFFICIENT`, no limitations. File-scoped preflight over 12 files returned `PREFLIGHT_STATUS_OK`, `SECURITY_RISK`, `CONFIDENCE_HIGH`, coverage anchors=11 / files=12 / indexed=12 / sufficient, blast=security, approval=human_approval_required. `go vet ./...` clean; `go test ./...` passes across all 32 packages. Findings 1 and 2 were reproduced with `go test -overlay=`, so no scratch file entered the worktree. `git status --porcelain` was empty at start and end.

## Limitations

No live publication, provider turn, or `gh` invocation was exercised. Typed `contract` queries returned zero rows for this domain, so the two-gate publication rule rests on the active intent YAML and the implementation rather than on a graph contract node. The runtime transaction stamp `.sensei/graph-authority.transaction.tsv` is missing (`embedded_transaction_matches_seed: false`), so the graph is authoritative by digest match to the validated artifact but carries no transaction stamp. Finding 7's exploitability is unverified. None of the findings matched a recorded forbidden-fix shape.

## Left undone, deliberately

The conversation asked for the audit to be written to `docs/audit/<date>.md` with an index. The architect's bounded plan does not contain that step and explicitly requires "no repository diff", so I did not create those files. That needs a new plan.

AWG: briefing(sensei-code architecture audit) | invariants: candidate.disposition_is_decided_and_evidence_outlives_removal, authority.only_an_explicit_answer_satisfies_a_boundary, resume.never_skips_a_human_decision, outcome.reporting_never_alters_the_result, sensei_boundary.refusal_carries_its_reason | uncertainty: Codex env-credential precedence unverified; no graph `contract` rows for this domain, so the publication rule rests on local intent plus implementation

=== RESULT ===
Audit complete. No files changed; the worktree has no diff.

## Findings, ranked by root contract impact

### 1. [High · Confirmed] A deferred publication decision is recorded as standing *and* terminated in the same run
`offerPullRequest` discards the error from `awaitChoice` ([engine.go:538](internal/workflow/engine.go#L538)). When the human presses Esc at the publication rendezvous, `awaitChoice` emits `WorkflowAwaitingAuthority` ("the question stands") and returns `errAuthorityDeferred`; `offerPullRequest` returns silently, and `implement` then reports success, records the candidate `Retained`, and emits `WorkflowCompleted` ([engine.go:1986–1998](internal/workflow/engine.go#L1986)). `FindInterrupted` treats `WorkflowCompleted` as terminal ([store.go:154](internal/session/store.go#L154), [:181](internal/session/store.go#L181)), so the task is filtered out of `/resume`.

Reproduced via `go test -overlay=` (no file entered the worktree): a task with `awaiting_authority` followed by `completed` yields **0** resumable entries. The TUI has already told the human "deferred · the question stands, /resume asks it again" ([model.go:399](internal/tui/model.go#L399)); `/resume` will answer "nothing to resume" ([model.go:1057](internal/tui/model.go#L1057)). The same shape applies to a stop at that rendezvous: `ctx.Err()` is discarded, no `WorkflowStopped` is emitted, and the run reports success.

A second, related defect: even if the question *were* resumable, `resumeAuthority` answers it and then calls `e.execute(...)` ([engine.go:2147](internal/workflow/engine.go#L2147)) — re-running architecture and implementation from the top. A yes to "open a pull request" would start a whole new task, not publish.

**Contracts:** `invariant:sensei_code.authority.only_an_explicit_answer_satisfies_a_boundary` (critical), `invariant:sensei_code.resume.never_skips_a_human_decision` (critical).
**Repair:** return a typed publication outcome from `offerPullRequest`; on defer or stop, emit no terminal completion and keep the question resumable; make resume of a publication question publish rather than re-execute.
**Proof required:** behavioral tests for defer-at-publication, stop-at-publication, and resume-after-publication-defer.

### 2. [High · Confirmed] Automatic candidate removal reads a stale audit snapshot instead of the candidate
`candidateEvidence` derives `ProducedNoWork` solely from `tc.EvidenceSnapshot` ([engine.go:2027](internal/workflow/engine.go#L2027)), and that snapshot is written only *after* validation and a decodable Sensei audit ([engine.go:905](internal/workflow/engine.go#L905)). Every first-cycle exit before that point — a read-only plan that edited files, a `validate` error, an `awareness_audit_diff` transport error, a `DecodeDiffAudit` contract error — returns with a non-empty diff and a zero snapshot. The terminal `disposeIfEmpty` then takes the removal branch, deletes the worktree and the branch, and records the reason "the candidate holds no work".

Reproduced via overlay: `candidateEvidence` with an unset snapshot returns `ProducedNoWork=true, DiffBytes=0`. The `candidate` package's own guard exists precisely for this shape and is bypassed by the false assertion — `Validate` only demands recorded work when `ProducedNoWork` is *false* ([disposition.go:133](internal/candidate/disposition.go#L133)).

**Contracts:** `invariant:sensei_code.candidate.disposition_is_decided_and_evidence_outlives_removal` (critical); `invariant:sensei_code.outcome.reporting_never_alters_the_result` (the recorded reason is false).
**Repair:** observe the candidate at disposal time (`CandidateDiff` against `BaseSHA`) rather than replay a snapshot; treat an unobservable candidate as holding work.
**Note:** the guarding test `TestOnlyAnEmptyCandidateIsRemovedAutomatically` ([engine_test.go:691](internal/workflow/engine_test.go#L691)) asserts the *order of identifiers* inside `disposeIfEmpty`. It passes through this defect unchanged.

### 3. [High · Confirmed] Publication outcomes are not carried, so lifecycle state can contradict Git and GitHub
`offerPullRequest` returns nothing, and its caller unconditionally records `Retained` — "accepted by review and unpublished" — and emits one `WorkflowCompleted` ([engine.go:1986–1998](internal/workflow/engine.go#L1986)). Therefore a successfully opened PR is still recorded as unpublished; a commit/push/PR failure emits `WorkflowFailed` followed by `WorkflowCompleted`, two terminal events for one run; and a successful push followed by a failed `gh pr create` leaves a published remote branch recorded as unpublished, with the partial effect nowhere ([publish.go:98–103](internal/publish/publish.go#L98)).

Separately, `prURL` falls back to `strings.TrimSpace(out)` ([publish.go:112](internal/publish/publish.go#L112)) — arbitrary command output — directly contradicting its own doc comment ("so a caller never shows a success line that contains no pull request"). That value is emitted as `"pull request opened…: "+url` with a `{"url": …}` payload ([engine.go:554](internal/workflow/engine.go#L554)). Its test covers only the happy extraction ([publish_test.go:48](internal/publish/publish_test.go#L48)).

**Contracts:** `invariant:sensei_code.candidate.disposition_is_decided_and_evidence_outlives_removal`, `invariant:sensei_code.outcome.reporting_never_alters_the_result`, `invariant:sensei_code.evidence.counts_carry_their_provenance`.
**Repair:** return a staged publication outcome (committed / pushed / PR-opened / URL), select the disposition from it, record partial effects explicitly, emit exactly one terminal state, and make an absent URL a failure rather than an echo.

### 4. [High · Confirmed] A read-only run's success condition is "the diff was empty", not the audit
In `ModeInspect` an empty diff returns `accepted=true` immediately ([engine.go:812](internal/workflow/engine.go#L812)) — before validation, before the Sensei audit, before the reviewer, before `emitChangeReport` and `recordDecision`. The worker's own result text is discarded (`if _, err := impl.Run(...)`, [engine.go:798](internal/workflow/engine.go#L798)), and the workflow completes stating findings are "in the transcript" ([engine.go:1971](internal/workflow/engine.go#L1971)).

The worker prompt is also mode-blind: it says "You may inspect, edit, build, and test" and "Implement only the architect's bounded plan" regardless of `tc.Mode` ([engine.go:1700](internal/workflow/engine.go#L1700)); `Mode` never reaches `implementationPrompt`. A worker that edits is caught only after the fact, and via finding 2 its candidate is then deleted.

So an empty, unevidenced, or malformed audit is reported successful, and no governed findings artifact reaches the architect. The three guarding tests here ([engine_test.go:838](internal/workflow/engine_test.go#L838), [:869](internal/workflow/engine_test.go#L869), [:878](internal/workflow/engine_test.go#L878)) are all source-text assertions.

**Contract:** `invariant:sensei_code.report.states_what_it_does_not_establish`; `invariant:sensei_code.evidence.counts_carry_their_provenance`.
**Repair:** preserve the worker result as a structured inspection report, require evidence and limitations, review it independently, and make that verdict the completion condition; tell the worker its mode.

### 5. [Medium-High · Confirmed] A Sensei survey that fails entirely is reported to the human as "the graph holds nothing"
`surveyPlan` drops every per-class query error with a bare `continue` ([assisted.go:161](internal/workflow/assisted.go#L161)) and returns `Surveyed: len(nodes)`. `SurveyOutcome.Describe()` renders `Surveyed == 0` as "the graph holds nothing of the surveyed classes for this domain" ([semantic.go:167](internal/retrieval/semantic.go#L167)) — an affirmative claim about the graph, produced by transport failure.

This is the recorded critical failure mode `failure.sensei_code.empty_sensei_tool_response_accepted_as_present_evidence` and the forbidden fix `forbidden_fix.sensei_code.derive_observation_presence_from_transport_success`, and it contradicts `invariant:sensei_code.sensei_boundary.refusal_carries_its_reason` — which the surrounding assisted path honours correctly everywhere else (workspace status and preflight both state their caveats).
**Repair:** carry failed-query count and reason in `SurveyOutcome`; refuse the affirmative wording when any class query failed.

### 6. [Medium · Strong signal] One human "yes" is reused for later, materially different plans in the same task
`answeredConditions` keys the memo on the routing condition text alone ([engine.go:2384](internal/workflow/engine.go#L2384)), and conditions carry no file list — e.g. `"Sensei requires approval for this change class: human_approval_required (blast radius security)"` ([authority.go:161](internal/workflow/authority.go#L161)). `applyAnsweredCondition` then proceeds without asking ([engine.go:1136](internal/workflow/engine.go#L1136)). A yes given for plan P1 silently authorizes any later plan in the same task reaching the same gate and blast radius, including one touching entirely different files. The recorded `authority.Resolution` already carries the question and the option; the plan scope is simply not part of the key.

**Contracts:** `invariant:sensei_code.authority.only_an_explicit_answer_satisfies_a_boundary` (critical), `invariant:sensei_code.workflow.context_never_widens_worker_scope` (critical).

### 7. [Medium · Strong signal] Credential stripping is Anthropic-only
`SessionOnlyEnv` lists only `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN` ([provider.go:339](internal/provider/provider.go#L339)). The Claude status branch relies on that stripping to claim "the account reported here IS the one a worker authenticates with" ([provider.go:204](internal/provider/provider.go#L204)). The ChatGPT/Codex branch ([provider.go:139](internal/provider/provider.go#L139)) reads stored Codex credentials and makes the same implicit claim with no equivalent stripping; `OPENAI_API_KEY` appears nowhere in the repository. The mechanism gap is proven; I did not verify Codex CLI's env-vs-stored-login precedence, so exploitability is unconfirmed.

**Contracts:** `invariant:sensei_code.provider.credentials_remain_provider_owned`, `invariant:sensei_code.provider.unknown_auth_is_not_connected`, failure mode `provider_auth_state_claimed_from_an_acknowledged_call_instead_of_confirmed_with_the_owner`.

### 8. [Low · Hypothesis] The architect resolution loop has no overall bound
`resolveArchitecture` is `for attempt := 1; attempt <= 2; attempt++`, but the escalate-and-certified path sets `attempt = 0; continue` ([engine.go:1179](internal/workflow/engine.go#L1179)) and rebuilds `prompt = certifiedResolutionPrompt(prompt, d)`, nesting the previous prompt. An architect that keeps returning `escalate` in a region Sensei certifies loops unbounded, spending provider turns and growing the prompt each round. Only context cancellation stops it. The human-answer paths share the shape but are bounded by the person.

## Awareness gaps (not defects)

- **`internal/admission` has no non-test consumer.** No file outside the package imports it, so the canonical chain is argv-constructible and unit-tested but never invoked by the running product. `invariant:sensei_code.admission.the_chain_is_senseis_and_only_it_establishes_admission` holds vacuously at runtime.
- **The checkout-relative field `gate.go` was waiting for now exists.** The live receipt this run published `binding.revision`, `binding.revision_status`, `binding.tree_digest_sha256` and `binding.graph_digest_status`. `sensei.WorkspaceStatus.Binding` decodes only `repository_domain` and `repository` ([contracts.go:481](internal/sensei/contracts.go#L481)), and `certifyStart` still discards `repositoryHead` on the stated grounds that no field answers the question ([gate.go:85](internal/workflow/gate.go#L85)). `WorkspaceStatus.Permits()` also never consults the `graph_authority` block it decodes.
- **Dead surfaces:** `sameCommit` and `short` ([gate.go:107](internal/workflow/gate.go#L107)), `certifiedStart.RiskClass`/`Invariants`/`GraphSourceCommit`, `Coverage.PatternOnly` (only self-referential), `WorkspaceStatus.CoverageState`. The certified start's risk classification reaches no caller.
- **Guarding tests for the highest-severity invariants are structural, not behavioral** — findings 2 and 4 both sit behind AST/source-text assertions that would keep passing.

## Positive boundaries that held

Only `admit-change` establishes admission and `Admitted()` is narrowly scoped to it ([admission.go:198](internal/admission/admission.go#L198)). PR argv contains no merge verb, and push plus commit capabilities are mechanically enforced. The reviewer is excluded from judging its own work by construction, inherits nothing, and binds to a revision. A reviewer accepting over a Sensei `block` does not conclude the candidate. Typed decoders fail closed on missing structured content and unrecognised enum members. The authority router takes no model text as input, and architect-supplied options cannot decide what choosing them means. `answeredConditions` fails closed on a store read error.

**One claim from the earlier audit I am withdrawing:** doctor returning overall OK with a provider at `UNPROVEN` is settled architect intent, not a defect — quoted in `doctor.go:29–42` (question.20de26bd75eab654 / answer.70ebacb875a8a1c5) and locked by `TestWarningsDoNotMasqueradeAsPassButDoNotBlockReadiness`.

## Verification

Sensei workspace receipt: composition `complete`, domain `github.com/globulario/sensei-code` from configured source, revision resolved `6516b459…`, tree digest resolved, graph authority current and stamped, seed current, live store 150,197 triples, coverage `SUFFICIENT`, no limitations. File-scoped preflight over 12 files returned `PREFLIGHT_STATUS_OK`, `SECURITY_RISK`, `CONFIDENCE_HIGH`, coverage anchors=11 / files=12 / indexed=12 / sufficient, blast=security, approval=human_approval_required. `go vet ./...` clean; `go test ./...` passes across all 32 packages. Findings 1 and 2 were reproduced with `go test -overlay=`, so no scratch file entered the worktree. `git status --porcelain` was empty at start and end.

## Limitations

No live publication, provider turn, or `gh` invocation was exercised. Typed `contract` queries returned zero rows for this domain, so the two-gate publication rule rests on the active intent YAML and the implementation rather than on a graph contract node. The runtime transaction stamp `.sensei/graph-authority.transaction.tsv` is missing (`embedded_transaction_matches_seed: false`), so the graph is authoritative by digest match to the validated artifact but carries no transaction stamp. Finding 7's exploitability is unverified. None of the findings matched a recorded forbidden-fix shape.

## Left undone, deliberately

The conversation asked for the audit to be written to `docs/audit/<date>.md` with an index. The architect's bounded plan does not contain that step and explicitly requires "no repository diff", so I did not create those files. That needs a new plan.

AWG: briefing(sensei-code architecture audit) | invariants: candidate.disposition_is_decided_and_evidence_outlives_removal, authority.only_an_explicit_answer_satisfies_a_boundary, resume.never_skips_a_human_decision, outcome.reporting_never_alters_the_result, sensei_boundary.refusal_carries_its_reason | uncertainty: Codex env-credential precedence unverified; no graph `contract` rows for this domain, so the publication rule rests on local intent plus implementation