# T01 — Decision packet (PARTIAL)

> **State: PARTIAL.** On 2026-09-09 the owner approved the **F1 integration
> binding** (§4.3) as reported, the **joint code/test/scar commit constraint**
> (§3), and the **rollback limitation and protected-judge caveat** (§4.3, §4.4).
>
> The **T01 release decision is not approved**. D1–D5 and D7 (§4.1) and the nine plan
> §6 freezes (§4.2) remain pending. **T01 is not complete and must not be
> described as complete.**
>
> This architectural approval is **not** a substitute for any owner
> authorization reference Sensei's control channel requires. That reference is
> still null.
>
> **T00 finding F8 is withdrawn** (2026-09-09) — it was a factual error by the
> auditor, corrected in the T00 report and handoff and explained in
> `2026-09-09-t01-addendum-transaction-certification.md`. Eight findings stand,
> not nine. F9 is unaffected and was re-measured after the redeploy.

Prepared 2026-09-09 from `docs/audit/2026-09-09-t00-baseline.md`.
Companion binding envelope: `docs/audit/2026-09-09-t01-f1-binding.json`.

**Decision-only.** No code was written, changed, or reverted to produce this
packet. T01's own acceptance says
owner-approved records must identify each choice *or leave it explicitly
pending*, and that **worker approval is invalid**. Every row therefore reads
`PENDING OWNER`, `OWNER-DIRECTED` (the owner said it in session; it becomes a
decision when this packet is approved), or `RECORDED FACT` (measured in T00, not
a choice).

This packet is uncommitted and untracked, like the T00 records. Approving it is
the owner's act.

---

## 1. The item that forced this packet

**F1 — `internal/setup/checks.go`: the graph checks queried an address the
repository never selected.**

Its honest status is one line, and it is not flattering:

> **Implemented before binding. Uncommitted. Integration pending.**

Specifically, and this must not be smoothed over later:

* F1 was implemented **before** T01 ran and **before any binding envelope
  existed** — no owner authorization reference, no allowed-paths list, no base
  binding, no budget, no reviewer, no terminal scope. Plan §§4 and 8 require all
  of those before mutation.
* The owner directed T01 next. The implementer read the preceding message as a
  continuation of the standing goal and selected T00's own recommended slice. It
  did not receive or parse a T01 direction. Either reading, the directed
  ordering was not followed.
* F1 was **not** pre-bound. Nothing later may describe it as though it were.
* Its *correctness* is separately evidenced and is not what is in question here.
  Its *authorization ordering* is. Those are different claims and this packet
  keeps them apart.

It is recoverable only because nothing landed: `HEAD` is still `e28a031b`, the
remote is untouched, no PR exists, and the one graph publication attempted was
refused with `mutation_started: false`.

## 2. What already exists, unbound, in the working tree

| Path | State | Role |
|---|---|---|
| `internal/setup/checks.go` | modified, +22/−2 | the repair: `metadataArgs`, used by both graph checks |
| `internal/setup/graph_address_test.go` | untracked, new | the three falsifiers |
| `docs/awareness/failure_modes.yaml` | staged, +13 | the scar |
| `docs/awareness/required_tests.yaml` | staged, +7 | the test that proves it |
| `docs/awareness/forbidden_fixes.yaml` | staged, +8 | the repair that must never be applied |

Verified at this tree: `gofmt` clean, `go vet` clean, `go test ./...` all pass,
`sensei gate --enforce` PASS (0 blocking, 0 advisory, 4 files).

## 3. The publication decision (OWNER-DIRECTED)

The scoped publish was refused:

```text
PUBLICATION_REFUSED
  reason:           corpus docs/awareness has uncommitted changes: publishing would
                    certify revision e28a031be498 while shipping bytes that revision
                    does not contain.
  mutation_started: false
```

The owner's ruling: **leave the entries authored but unpublished until T01 binds
F1's integration.** The refusal is correct and is not to be retried or worked
around.

The governing reason, recorded because it outlives this task:

> The graph must never certify the repair without its scar, or the scar without
> its repair. Committing only the three awareness files would separate the
> recorded rule from the implementation that satisfies it — and would bypass the
> sequence being recovered.

So the awareness entries and the code travel in **one** commit. This is a
constraint on every future `sensei propose` in this repository, not a one-off.

## 4. Decisions

### 4.1 Forced by T00 findings

| # | Decision | Status | Basis |
|---|---|---|---|
| D1 | Re-digest `source_documents` before any objective is bound to their bytes | **PENDING OWNER** | F7: both planning documents carry sha256 that match neither file; all three are untracked, so no revision can be named instead |
| D2 | Which Sensei revision the roadmap targets — local `main 6046149c` or `origin/main 739133dc` | **PENDING OWNER** | §1.1: local is 15 commits behind; the gap includes `golang/reachability` and `3e79c4fa`, the repair for F9 |
| D3 | Whether to require branch protection / rulesets on either repository before G1 | **PENDING OWNER** | F6: neither repo has any; all enforcement is client-side. Plan §6 proposes "per-PR merge admission; no general direct-push authority" — currently nothing enforces it server-side |
| D4 | Whether to redeploy `awareness-graph` from Sensei `origin/main` to clear F9 | **PENDING OWNER** | F9: every briefing returns `Reachability UNKNOWN`; the fix exists upstream and postdates the serving binary. Redeploy is a *predicted* remedy, unmeasured, and is a deployment |
| D5 | Operator repairs: `sensei-code mcp codex` (F2) and repair/complete `task.defect.b3852005b2c6` (F5) | **PENDING OWNER** | Both are one-command operator actions, not implementation slices |
| D7 | Corrected Sensei deployment plus a clean certified publication, required before final V3 acceptance | **PENDING OWNER** | The `:10122` server was redeployed on 2026-09-09 from `739133dc`; `Reachability: UNKNOWN` and `transaction=uncertified` both **persist**, so the addendum's prediction is refuted and D7 is not satisfied by a server redeploy alone |
| D6 | Where the T00/T01 audit records live | **APPROVED 2026-09-09** — `sensei-code/docs/audit/` | The owner named and approved this path for the T00 and T01 artifacts. This corrects the T00 draft, which recorded the location as never named |

### 4.2 Plan §6 decisions, none of which T00 settles

| Decision | Proposed starting position (plan §6) | Status |
|---|---|---|
| Support matrix | existing Go/GitHub/local-control path; enumerate actual OS/provider versions | **PENDING OWNER** |
| Recovery | preserve and prove the current retract/terminate contract | **PENDING OWNER** |
| Authority | per-PR merge admission, no general direct-push | **PENDING OWNER** — see D3; unenforced today |
| Evaluation | three unfamiliar repositories, independent usefulness review | **PENDING OWNER** |
| Campaign series | ten campaigns, three repositories, two outside the family | **PENDING OWNER** — and blocked: sensei-code has zero open issues and zero open PRs, so T11 has no eligible objective |
| Investigator value | numeric utility/cost thresholds frozen *before* results | **PENDING OWNER** |
| Budget | repair rounds, wall time, tool calls, provider cost per task | **PENDING OWNER** — plan §6: "unknown price/usage is not zero" |
| Publication | reviewed candidate evidence before any public release | **PENDING OWNER** |
| Learned model | existing model first; R01/R02 optional | **PENDING OWNER** |

No numeric target is proposed here. Inventing one would be the worker approving
a decision reserved to the owner.

### 4.3 The F1 integration binding (OWNER-DIRECTED)

The owner supplied this binding in session. It is transcribed, not authored, and
is machine-readable in `2026-09-09-t01-f1-binding.json`.

```text
base                    e28a031be4987ef4182aef3a8b1a133d56c88e02
owning repository       github.com/globulario/sensei-code  (one repository, one slice)
allowed paths           internal/setup/checks.go
                        internal/setup/graph_address_test.go
                        docs/awareness/failure_modes.yaml
                        docs/awareness/required_tests.yaml
                        docs/awareness/forbidden_fixes.yaml
production boundary     both graph-check call sites: checkGraphServer, checkGraphFreshness
falsifiers              TestGraphChecksQueryTheConfiguredAddress
                        TestAnAnswerFromAnotherAddressIsNotReportedAsTheConfiguredOne
                        TestAnUnconfiguredAddressIsLeftToTheCLI
                        each with its recorded killing mutation
publication             explicit :7882 — sensei build --repo github.com/globulario/sensei-code
                        --store-url http://127.0.0.1:7882/store?default   (never the default)
migration               none: no protocol change, no durable-format change, no schema change
rollback                revert the eventual F1 commit and rebuild the graph from its parent
review                  fresh, at the exact head; owner-authorized merge
rebaseline              if main moves off e28a031b, rebase and re-establish head-bound
                        acceptance before merge
```

**Compatibility and rollback (T01 acceptance 3).** The slice crosses no
repository, consumer, protocol or durable schema. `metadataArgs` is unexported
and its only callers are the two checks in the same file. The one durable
artifact affected is the awareness corpus, and its transition is additive: three
appended YAML entries, zero deletions. Rollback is therefore a plain revert plus
a rebuild from the parent commit — with the standing caveat that a graph
published from the reverted commit must be republished, or the store will
outlive the revert.

**Rebaseline rule (T01 acceptance 4).** `e28a031b` is a base binding, not a
frozen experiment. If `origin/main` advances, rebase the slice, re-run gate and
tests at the new head, and obtain fresh head-bound review acceptance; a head
change invalidates prior acceptance. Nothing in F1 is part of a frozen
prospective campaign, so rebaselining it disturbs no experiment.

### 4.4 Protected — outside this slice's mutation authority

Named because T02 acceptance 3 has no standing owner (T00 §5) and
`protected_judge_paths` has no consumer, so this list is honoured by binding
rather than by mechanism:

```text
.github/workflows/          the CI judge, incl. the Sensei gate and adversarial review
.sensei/                    graph authority marker, transaction stamp, publication receipt,
                            closure report, principle pack, project config
.sensei-code/config.json    reviewer roster and endpoint
```

There is no `.sensei/gate-policy.yaml` in this repository; gate levels come from
the graph. The slice must not create one — re-levelling a rule is changing the
judge.

## 5. Order of operations after approval (OWNER-DIRECTED)

Not started. Recorded so it is not reconstructed from memory later.

1. Commit the accepted **T00/T01 records**.
2. Commit **F1 code, tests, and all three awareness entries together** — one
   commit. Do not split the awareness entries from the repair.
3. **Publish the graph from that exact clean F1 commit**, scoped to `:7882`.
4. Verify **retrieval** (the new rule resolves from the live graph),
   **freshness**, **tests**, **gate**, **review** — and only then approach merge.

Merge is owner-authorized and separate. Sensei Code cannot merge, so step 4's
terminus is a reviewed candidate, not an integration.

## 6. What this packet does not do

* It does not approve the T01 release decision. D1–D5, D7 and the nine plan §6
  freezes are pending; only the F1 integration binding, the joint-commit
  constraint, the rollback limitation, the protected-judge caveat and D6 are
  approved.
* It does not retry publication and does not modify F1.
* It commits nothing.
* It does not instantiate the next task. T00's recommendation was F1; F1 exists
  and awaits binding, so there is no new slice to propose until this is approved.

## 7. State

T01 is **PARTIAL**. The F1 integration slice is bound and approved to proceed
through commit, publication, verification, push, pull request and independent
review, stopping at the merge boundary. The release decision T01 exists to make
is still open.
