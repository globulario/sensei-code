# Review Spine v1 — commissioning record

**Issue:** globulario/sensei-code#182 (R6, the final slice)
**Authorized base:** `03c1b3162ad72b8b608563e3037f78737b18042a`
**Branch:** `refactor/review-spine-r6-consolidation`
**Candidate:** the commit that carries this file
**Date:** 2026-09-18

Commissioning is not "the tests are green". This record says which sequences
were driven against the shipped components, with which identities, and — just as
importantly — **which parts of the matrix were not driven**.

---

## What was driven, and with what

Real components throughout: a real git repository and its snapshot projection
(`PublishSnapshot` over a real bare remote), a real HTTP GitHub App mailbox
(token minting, comment POST/GET, installation permissions), the real
`ReviewObligationStore`, the real `reviewstore.Store`, the real relay adapter,
the real attestation path and the real reviewer-body parser. No component under
test was replaced by a stub.

The one substitution is GitHub itself: an in-process HTTP server stands in for
`api.github.com`. Everything above the socket — App installation auth,
comment identity, publication identity, the response window — is the shipped
code path.

---

## A. The revision arc — `TestCommissionTheRevisionArc`

```text
C1 exists
  -> review obligation R1 published, snapshot pushed, obligation recorded
  -> reviewer answers REVISE on R1, canonically, naming the assigned provider
  -> the finding reaches the implementer's input verbatim
  -> R1 discharged by its own answer; nothing owed
C2 exists (different tree, different candidate digest)
  -> review obligation R2 published; R2 != R1
  -> reviewer answers ACCEPT on R2
  -> the REVISE of C1 is on the mailbox throughout and never qualifies C2
  -> ReviewStore holds C2's exact bytes, one record, delivered
  -> R2 discharged; nothing owed
  -> the owner overrides THAT review; the override names its digest, its
     request and C2's candidate digest
```

Asserted through the chain: request ids distinct; the recorded artifact's
`candidate_digest`, `candidate_tree` and `request_id` are C2's; the turn's
returned digest equals the stored digest; the C1 and C2 records are different
reviews; the override binds to the C2 candidate.

## A'. Supersession while the review is still owed — `TestCommissionASupersessionWhileTheReviewIsStillOwed`

The other half: C1's review goes **unanswered**, the candidate moves to C2, and
the frozen R4 ordering applies — successor published, successor recorded,
predecessor retired, and only then the withdrawal posted. Asserted: exactly two
requests ever published; the superseded request never acquires a review; the
survivor's recorded review is about C2's tree; the withdrawal on the mailbox
names the predecessor.

## B. Restart across a real process boundary — `TestCommissionARestartAcrossARealProcessBoundary`

A genuine fork/exec, not a cancelled goroutine. The test binary re-execs itself
as a separate OS process with its own pid.

```text
process 1730982   published request r-93dfc5deac3a065b and was SIGKILLed
                  (no deferred cleanup, no graceful shutdown)
                  the reviewer then answered while NOTHING was listening
process 1731006   reattached to r-93dfc5deac3a065b and consumed
                  sha256:c6afe2ec0635334bfbc3293ad6f3b3e40cdb28aded30cc744be70baf0e808d65
```

Asserted: the obligation survived process death; **no request was reminted**
(the mailbox carries exactly the original request id); the consumed review is
the exact artifact posted; the candidate identity the dead process published is
byte-for-byte the candidate the recorded review is about; the obligation is
discharged; the two pids differ from each other and from the parent.

The pids above are from one run and will differ per run; the assertions do not
depend on their values, only on their distinctness.

## C/E. Every preserved state is rediscoverable — `TestCommissionEveryPreservedStateSurvivesAndIsRediscovered`

The three ways a review turn ends without a verdict, each driven against the
real components and then **re-entered by a second, independently constructed
runner over the same workspace**:

| state | reported as |
|---|---|
| nobody answered | `roles.ErrReviewUnanswered`, same request |
| a review is held here and not yet delivered | `DELIVERY_PENDING` observation fault |
| the reviewer replied with something unusable | `MALFORMED_OR_UNATTRIBUTABLE` observation fault |

For each: the second runner reports the *same* condition, rediscovers the *same*
obligation, and every durable record is byte-identical before and after
(compared as bytes, not asserted).

The remaining observation states — `WRONG_TARGET` and `CONFLICT`, and a
corrected review consumed under the same obligation after a fault — are driven
against the same standing-request harness in
`internal/ghbridge/review_observation_test.go`.

## D. Transport parity — `TestAnOverrideBindsIdenticallyWhateverTransportDelivered`

The same canonical bytes delivered two ways — read off the mailbox, and carried
by a terminal and published by the App — produce the same `ReviewStore` record
and an owner override with identical digest, reviewer, request and binding. Only
the transport evidence differs. A staged relay is **not** attestable
(`TestAStagedRelayIsNotAttestable`).

---

## F. The whole loop, through the actual engine — `internal/acceptance/review_spine_loop_test.go`

The drives above start at *"a candidate exists and a review is owed"*. This one
starts at the objective and runs the engine itself:

```text
objective -> architect -> implementer C1 -> reviewer REVISE
          -> automatic repair C2 -> re-entry -> reviewer ACCEPT
```

`workflow.Engine` is the product's. Only the architect and implementer are
stubs, for the reason the neighbouring tripwire gives: a deterministic provider
makes a failure the state machine's rather than a model's. **The reviewer is not
stubbed** — `ghbridge.Resolver` serves the reviewer role over a mailbox the test
stands up, which answers REVISE for the first candidate and ACCEPT for the next,
inside its own handler.

Re-entry is real rather than simulated: the mailbox withholds the first answer,
so the waiter times out, the run ends `WAITING_REVIEW`, and the task is picked
up again through `session.FindInterrupted` + `Engine.Resume` — the same path
`sensei-code resume --task` takes.

### It does not run in this environment, and here is exactly why

```text
the governed lane cannot start: workspace composition partial
  limitation (blocking): graph_authority is present but not authoritative
                         (freshness, seed, or build-provenance state is not current)
  build_provenance_state:            BUILD_PROVENANCE_STATE_DEV
  embedded_transaction_matches_seed: false
  certified_awareness_graph_commit:  6046149cebeff2198f847372c4ece739666857db
  local awareness-graph checkout:    087041f
```

`workflow/gate.go` refuses a governed run on a workspace Sensei will not
certify. That refusal is the product working. Certifying it means rebuilding the
graph with a resolved awareness-graph checkout and re-stamping this machine's
certified commit — **graph authority and publication identity, which the R6
brief lists as a non-goal (§25) and which is the next milestone**.

The test therefore asks that question through the *same surface the gate uses*
and skips naming the exact limitation, rather than asking a different instrument
and guessing, or quietly passing. It is apparatus that runs the day the
precondition is met; today it measures the precondition.

**This is the one frozen R6 gate that is not met**, and the architect has ruled
on it (PR #190 comment `5734086214`): graph authority is not to be re-stamped to
make R6 green, because that would contaminate the milestone that fixes it. R6
lands with the whole-loop commissioning **provisional**, and Review Spine v1 is
not claimed to be fully commissioned until this test actually runs:

```text
repair the R6 edges -> accept + merge R6
                    -> fix graph authority / freshness / publication identity
                    -> run this whole-engine commissioning
                    -> extend it through admission/publication
                    -> close Review Spine v1 commissioning
```

---

## What was NOT commissioned

Stated so nobody reads more into a green run than it earned.

**The whole loop, end to end, in this environment** — see F above for the exact
blocker and who owns the decision.

**Admission and publication.** The arc ends at a discharged obligation and an
accepted candidate; landing and Sensei admission are not driven here, and the
loop test above stops at the engine's own terminal.

**Live GitHub.** The mailbox is an in-process HTTP server. Nothing here proves
behaviour against api.github.com's real rate limits, eventual consistency or
comment ordering.

---

## Human interventions during commissioning

**None.** No step required a person to carry information between agents. The
reviewer's finding reached the implementer's input as the verdict text the
runner returned; the repair produced a new candidate whose obligation the owner
minted; the stale review was rejected by identity rather than by someone
noticing it was stale; and the restart reattached to the standing request
without anyone naming it.

Dave's authority is unchanged and unbypassed: the owner attestation in the
revision arc is an explicit human override, recorded as one, and it satisfies no
independent-review obligation.

---

## Mutation results

27 semantic mutations against the surviving rules; **27 killed**, each by a named
assertion. See the R6 report on PR for the table. Five of them cover the repairs
from the two review rounds: a recovery path that trusts any author, one that
trusts an unauthenticatable receipt, one that takes the first matching receipt
rather than the publisher's, a publication that names the retrying caller rather
than the principal that staged the delivery, and a new App-published obligation
recorded with no publisher.

One **harness fault** was found and fixed mid-campaign: two edits to the same
file recorded the second backup from already-mutated content, so restoring put
the first edit back on disk and left it there for later mutants. The campaign was
re-run in full after the fix, against a verified-clean tree.

## Structural subtraction

| | base | candidate |
|---|---|---|
| durable semantic review stores | 2 | **1** |
| review response grammars | 2 | **1** |
| review obligation owners | 1 | **1** |
| relay semantic stores | 1 | **0** |

Reviewer-spine production code lines (comments and blanks excluded), across the
twelve files this slice touched: `3423 -> 3685` (+262), of which the one-way
historical migration is +219. Excluding that transitional file the spine is
**+43 lines** — one store instead of two, one grammar instead of two, and a
third pinned principal (the publisher) that the authority repair added.
