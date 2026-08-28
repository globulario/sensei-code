# S1 — receipt/restore consolidation (designed; NOT handed to the loop)

The first self-improvement specimen for sensei-code, designed now so it names
the self-knowledge Phase B3 must produce before it may run. Per the V2
direction and the Phase B split of 2026-08-28: S1 executes only after the
surfaces it touches hold mechanically established coverage and the
preservation obligations below are mechanically visible. Until then this is
a target, not a task.

## Objective, frozen

Reduce the three receipt/restore mechanisms the engine grew in one day to
one common lifecycle, without changing what any of them proves.

The three, today:

```text
plan provenance        PlanProposed payload (proposedPlan) · session.Interrupted.PlanRecord/PlanSource/PlanEventSource
                       · restorePlanBound: exact bound, source by emitter, SUPPLIED_PLAN_CONTEXT_UNAVAILABLE
prospective grants     ProspectiveGranted payload (prospectiveRecord) · Interrupted.ProspectiveRecord
                       · restoreProspectiveGrants: world match, one-to-one vs declarations
existing-test grants   TestEditGranted payload (testEditRecord) · Interrupted.TestEditRecord
                       · restoreTestEditGrants: world match, plan match, EXACT match vs side-effect-free recomputation
(and, adjacent)        premise receipts: engine-owned, issued at routing, answered by the closure round
```

Three payload types, three session fields, three restore functions, three
event kinds, three FindInterrupted cases — one law each time: *a predicate
survives restart, and a record is not authority.*

## Must preserve (each is an existing falsifier; S1 may not weaken one)

```text
typed authority distinctions      plan source / prospective (architectural anchor) / test-edit (operational) stay distinct types
                                  and are never summed or coerced          -- TestOperationalAuthorityIsSubtractedFromTheCoverageQuestionNotAddedToIt
world binding                     every record names the world; a record at another world restores nothing
                                                                            -- TestASuppliedPlanSurvivesResumeOrFailsClosed (world), TestATestEditGrant…SurvivesResume
record identity                   the exact bound/grant, never the rendered summary
                                                                            -- TestAResumedSuppliedPlanContinuesUnderTheExactBound
absence is positive               absent plan_source is the architect's only when the architect emitted it
                                                                            -- TestASuppliedRecordThatLostItsSourceIsNotTheArchitects
one-to-one restore                no missing / duplicate / extra / mismatched grants
                                                                            -- TestAResumeRecordMissingTheGrantForADeclaredSurfaceIsRefused
revalidation on resume            test-edit grants match a side-effect-free recomputation exactly
                                                                            -- TestARecordedTestEditGrantIsReEstablishedFromTheWorldOrRefused
repeated resume cannot mint       resume computes and compares; only routing records
                                                                            -- TestARepeatedResumeCannotMintTestEditAuthority
future-only                       a closure round's question cannot cover the run that wrote it
                                                                            -- TestDerivedCoverageExcludesTheWritingTask (coverageAtWorld)
authority reaches the worker      grants rendered into the implementation prompt, on every cycle
                                                                            -- TestTheImplementorIsShownTheProspectiveGrantItOperatesUnder, TestTheGrantSurvivesAReviewFeedbackCycle
inspection independent+terminal   post-creation / post-edit refutation is terminal, never a handoff
                                                                            -- TestAProspectiveSurfaceRefutationStopsBeforeHandoff, TestAGrantedTestEditIsInspectedAgainstItsExactGrant
premise identity                  engine-issued receipts; silence = unresolved; paraphrase buys nothing
                                                                            -- TestAParaphrasedPremiseDoesNotBuyAFreshClosureRound, TestAnUnansweredReceiptIsUnresolvedNotUnasked
```

## Shape of the consolidation (a direction, not the plan)

One `authorityRecord` lifecycle owned by the session layer:

```text
record(kind, world, payload)        routing writes; typed kind ∈ {plan, prospective, test-edit}
restore(kind, task, world, verify)  resume reads the ORIGINAL record; verify(kind) is the kind's own
                                    revalidation (exact bound / one-to-one declarations / recomputation)
                                    and refuses on any mismatch; never writes
```

with one `FindInterrupted` case keyed by kind, one event kind carrying a
typed payload, and the three restore functions becoming three verifiers.
Premise receipts stay separate: they are not restored across resume today
and S1 must not extend their lifetime as a side effect.

## Measure

```text
mechanisms                 3 → 1   (payload types, session fields, event kinds, FindInterrupted cases, restore entry points)
duplicated lifecycle code  lines removed, by diff
falsifiers                 unchanged in number and in what each fails for (no test edited to pass)
review findings            independent model review + mediated re-review count; a consolidation that ships a hole is a failure
proof strength             unchanged: no falsifier weakened, none deleted
```

## Preconditions before S1 may be handed to the loop (B3 outputs)

```text
anchors > 0 over internal/workflow/{suppliedplan,prospective,testedit,premise}.go, engine.go's resume/route/record sites,
             internal/session/store.go, internal/derived
the obligations above represented in the graph as invariants / required tests bound by exact name
             (sensei preflight names them; a renamed test satisfies nothing)
a frozen S1 plan (supplied-plan lane) with no inference claim
independent review by a different provider, mediated by the human; human merge authority
```

If B3 cannot ground one of these surfaces, that is the finding, and S1 waits.
