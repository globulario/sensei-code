# Implementation brief: consequence assessment must not depend on blind spots

## Why this exists

A live self-audit found that `AssessConsequences` is currently reached only inside the consequence-blind-spot path. The current behavior is masked because ordinary candidate edits arrive with a bounded stage that happens to produce the expected route.

That is a right answer resting on the wrong dependency.

The law for this slice is:

> Consequence assessment is a property of the proposed action. The presence or absence of a blind spot must not decide whether consequence assessment runs.

Blind spots may affect the assessment. They must not own the control-flow edge that makes the assessment exist.

## Required behavior

For every action that can reach an authority/grant decision:

1. Establish the engine-owned stage. Provider text must not choose or clear it.
2. Establish consequence assessment for that action, or return `CannotEstablish` if the engine cannot classify the stage/consequence boundary.
3. Apply explicit approval gates independently. Derived coverage or a bounded consequence assessment must never clear an approval gate.
4. Evaluate premise/knowledge gaps independently of consequence authority.
5. Only then permit a technical grant.

Do not duplicate Sensei governance semantics inside sensei-code. This slice is about orchestration ordering and required evidence, not inventing a second policy engine.

## Required attacks

Pin route-level tests for at least these cases:

- candidate edit, no blind spots, bounded consequences -> may grant if all other gates permit;
- publish/outward action, no blind spots -> human authority / unacceptable consequence as current policy requires;
- migration/deployment-like action, no blind spots -> must still be assessed;
- unknown/unclassified stage -> `CannotEstablish`, never silent boundedness;
- explicit approval gate -> human route even when consequence assessment is bounded;
- identical action with and without an unrelated blind spot -> the consequence result must not disappear or change merely because the blind-spot list changed;
- consequence-bearing blind spot -> may tighten/escalate the assessment but must not be the trigger that makes assessment run at all.

Include one mutation test or equivalent control-flow regression that would fail if `AssessConsequences` is moved back underneath a blind-spot branch.

## Live proof

Replay the known governed edit-stage specimen and one outward/publish specimen with their blind-spot lists removed from the fixture/input while preserving stage and approval data.

Expected: the edit remains bounded where justified; the outward action still escalates. If removing blind spots changes whether consequence assessment exists, the slice is not complete.

## Non-goals

- Do not add a new human prompt.
- Do not make severity or high-risk-file membership itself a consequence verdict.
- Do not let provider-declared plan language clear an engine-owned stage.
- Do not solve the still-unadopted `dq.consequence_blind_spot_authority` by silently encoding one answer here.

## Success criterion

The engine can explain the consequence basis for every route that depends on consequences, and that basis remains present when blind-spot metadata is absent. A future change to blind-spot production cannot accidentally remove consequence assessment from the workflow.
