# Implementation brief: connect observation findings to the normal governed repair loop

## Why this exists

`sensei-code observe` has now demonstrated that it can inspect its own code, produce source-tagged findings, and discover real defects while holding zero mutation authority. What is not yet demonstrated is self-repair:

```text
observe defect
  -> evidence
  -> candidate repair
  -> normal governance
  -> implementation
  -> verification
  -> independent review
  -> complete/refused
```

The dangerous shortcut is to let an observation lane mutate because it found something true. Do not do that.

The law for this slice is:

> Observation may discover work. It may not perform the work. A repair must begin as a new governed change whose provenance points back to the observation.

## Required architecture

Preserve `StageObserve` as a terminal, non-mutating lane. The repair path must be a separate governed submission.

Introduce the smallest durable/structured handoff needed to carry a finding into a new task. The exact type/name is not prescribed, but it must preserve at least:

- stable finding identity;
- observation task/run identity;
- repository/world revision inspected;
- finding text or structured proposition;
- concrete evidence references (file:line, command/test receipt, graph reference as applicable);
- source/epistemic status (observed vs inference/unverifiable);
- affected files/entities;
- the original task objective/provenance;
- whether the finding is eligible to become repair work.

Do not make the finding itself authoritative. It is evidence/provenance for the next task, not admission.

## Eligibility

Only findings that the observation lane reports as evidence-backed/observed may enter the automatic repair handoff. Inference-only or unverifiable findings may be retained/reported but must not silently become change objectives.

If the user's original objective explicitly authorizes repair (for example, "audit and repair verified defects"), the system may create the new governed repair task without asking a technical question. If the original objective authorizes observation only, observation remains terminal. Do not manufacture human authorization after the fact.

Headless/unattended provenance must remain unattended. No flag may assert that a human approved the finding.

## Repair workflow

A handed-off repair must enter the same normal change workflow as any other task:

1. fresh start gate / repository world;
2. briefing/preflight/coverage;
3. premise checking;
4. consequence and approval-gate assessment;
5. close technical gaps where the existing machinery can establish them;
6. candidate worktree;
7. implementation worker;
8. tests/build/static checks;
9. Sensei diff/admission evidence as currently required;
10. independent reviewer;
11. governed completion or honest refusal.

The handoff may not bypass any of these because the defect was found by Sensei-code itself.

## First self-repair specimen

Use one real verified defect found by the observation suite, preferably a narrow defect whose expected behavior can be independently asserted. Do not hand the implementation to the agent in the task wording.

The benchmark should start from a task shaped like:

```text
Audit <scope> and repair any verified, bounded defect you find without changing unrelated behavior.
```

The technical fix must be discovered by the system. The human may define the objective and consequence envelope, but must not supply the technical answer after observation.

## Required attacks

- inference-only finding -> cannot auto-create repair work;
- observed finding from stale world -> new repair task must re-check current world and may refuse if the defect disappeared;
- observed finding touching an approval-gated/outward action -> still reaches human authority;
- finding claims one file but repair plan expands scope -> normal consequence/coverage rules see the expanded scope;
- repair fails verification/review -> must not retroactively mark the observation false or force completion;
- observation workspace remains discarded; repair uses a fresh candidate worktree;
- a finding created by Sensei-code does not become a Sensei admission/receipt merely because its source is internal.

## Measurement

For the first successful specimen record:

- observation run id and exit 6;
- finding id/evidence;
- whether any human technical answer was supplied after the finding;
- repair route decisions and knowledge-gap closures;
- candidate verification results;
- independent review result;
- final outcome;
- whether any durable lesson/recipe/scar was reused on a later related task.

## Non-goals

- Do not auto-fix every observation finding.
- Do not merge observation and mutation into one stage.
- Do not invent a general value calculus.
- Do not weaken approval gates to make self-repair succeed.
- Do not call self-repair proven if a human supplies the actual fix after the observation.

## Success criterion

At least one real defect discovered by `observe` is converted into a separate normal governed repair task and reaches verified candidate/completion without a human supplying the technical repair, while unsupported findings and authority-gated consequences still refuse or escalate normally.
