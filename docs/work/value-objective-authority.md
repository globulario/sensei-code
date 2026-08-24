# Implementation brief: separate requested objective, technical premise, and consequence authority

## Why this exists

A live self-improvement task asked Sensei-code to consolidate `internal/` because it had "too much surface area". The architect accepted that premise, produced a consolidation plan, and emitted no human question or options. The engine later stopped the plan for consequence reasons and for an inference-sourced load-bearing technical claim.

The observed problem is not simply that the architect should ask more questions. Three different things were collapsed:

```text
requested objective/value
  "reduce internal surface area"

technical premise
  "these packages can be consolidated without changing observable behavior"

consequence judgment
  "this amount of architectural disruption is acceptable"
```

They must not inherit authority from each other.

## Required investigation before design

Trace how a task objective enters the system today from:

- interactive human submission;
- unattended/headless submission;
- an agent-created follow-up task;
- observation-to-repair handoff when that exists.

Identify which provenance is actually established at each entrypoint. Do not infer "human objective" from which Go function was called; the `RequestedByHuman` defect already disproved that approach.

## Required behavior

- A value/objective explicitly supplied through an actually human-authorized interactive task may be treated as the requested objective within its stated scope.
- An unattended or agent-submitted task must not be stamped as human value authority.
- A requested objective does not establish technical premises needed to achieve it.
- Technical premises still require repository/graph/derivation/experiment evidence through the existing epistemic machinery.
- A technical plan satisfying the objective does not establish that its external/unbounded consequences are acceptable.
- Consequence/approval authority remains independently assessed.
- The architect may propose technical interpretations of an objective, but those proposals do not upgrade the objective's provenance or consequence envelope.

Do not build a universal moral/value ontology. This slice is about provenance and separation of already-existing authority dimensions.

## Required specimens

Pin at least these:

1. Interactive human objective: "reduce internal surface area without changing observable behavior". The objective may be accepted as requested, while "these five packages can merge safely" remains a technical proposition that must be established.
2. Unattended AI submits the same task. The engine must not record the objective as human-owned merely because the wording is identical.
3. A bounded candidate edit that clearly satisfies an authorized objective may proceed if technical and consequence gates pass.
4. An outward/publish/migration consequence still reaches the appropriate authority boundary even when the objective is human-supplied.
5. An architect introduces a new optimization criterion not present in the task, such as "minimize package count at all costs". It must remain an architect proposal/assumption, not silently become the user's value.

## Human-question behavior

Do not add routine permission prompts. A question is warranted only when the requested objective or acceptable consequence envelope is genuinely underdetermined and the unresolved choice belongs to human/value authority.

If the task objective is already sufficient, do not ask the human to restate it merely to satisfy a schema.

## Non-goals

- Do not ask humans to certify ordinary technical truth.
- Do not let a provider label its own statement `human_value` or equivalent.
- Do not make every refactor a human-authority event.
- Do not weaken consequence routing to increase autonomy.
- Do not resolve the unadopted consequence design question by side effect.

## Success criterion

Sensei-code can state, for a planned change, which parts came from the requested objective, which are technical claims that still need evidence, and which consequence decisions remain authority-sensitive. No actor gains authority over one lane merely by controlling another.
