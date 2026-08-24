# Implementation brief: measure cold start and compounding across real tasks

## Why this exists

The next project-level hypothesis is no longer whether Sensei can store knowledge or whether one technical gap can be closed. It is whether accumulated experience makes future engineering measurably better.

A new repository begins with thin project-specific awareness. That is acceptable only if Sensei-code can bootstrap useful awareness from repository reality and if later tasks reuse/revalidate what earlier tasks learned.

The benchmark question is:

> Does Sensei-code learn how to work on a project, such that human technical intervention and rediscovery fall as governed experience accumulates?

Do not answer this by counting graph triples or number of recipes.

## Benchmark shape

Build a repeatable harness/report for a sequence of ordinary tasks on one repository, followed by at least one previously unseen repository/project when the harness is stable.

For each task capture:

- starting awareness state relevant to the task (`OK` / inferred/context/empty as applicable, direct anchors, derived coverage);
- route chosen and every human-authority interruption;
- number and class of technical knowledge gaps;
- whether gaps were closed by existing knowledge, existing recipe revalidation, new derivation, experiment, or human technical answer;
- observation rounds and findings used;
- implementation/review cycles;
- wall time and provider cost when available;
- verification outcome;
- new durable artifacts learned (recipe, scar, contract question, adopted knowledge as applicable);
- whether a later task reused each artifact without rediscovering the same fact from scratch.

## Primary metrics

The primary metrics are not raw completion rate alone:

1. **Human technical intervention rate** per task.
2. **Rediscovery rate**: previously established/revalidatable project knowledge that the agent had to rediscover manually.
3. **Knowledge reuse rate**: prior durable knowledge activated/revalidated in later tasks.
4. **Closure yield**: technical gaps closed autonomously / technical gaps encountered.
5. **Repeat-failure avoidance**: whether a stored scar/forbidden fix/test prevents recurrence.
6. **Reasoning-path defects found before behavioral failure**, where observation detects an unsound route or overclaim that still produced the right output.

Track wall time/cost, but do not optimize them at the expense of truth/relevance.

## Cold-start specimen

For a repository with no project-specific Sensei knowledge, record the initial distribution of files/tasks that are anchored, contextual, inferred, or empty. Then run a small fixed task sequence without manually pre-populating architecture.

The allowed bootstrap tools are observation, repository evidence, existing generic derivation machinery, tests/runtime experiments, and explicit project authority already present in the repository. Thin awareness must not lower truth standards.

Expected shape:

```text
thin awareness
  -> observe/investigate
  -> derive or record honest unknowns
  -> ordinary work
  -> durable revalidatable knowledge
  -> later task reuses it
```

If the system repeatedly escalates ordinary technical unknowns to the human, record that as failure rather than supplying answers to make the run green.

## Compounding specimen

Choose at least one pair of tasks where task N creates a durable artifact K and task N+M later encounters a related situation.

The benchmark passes only if the later run demonstrably consumes/revalidates K and avoids some work the earlier run had to perform. Merely retrieving K into a prompt does not count; it must alter the route, required investigation, forbidden move, test selection, or other structured behavior.

## Guardrails

- Do not add derivation families merely to improve the benchmark score.
- Do not count true-but-irrelevant facts as useful learning.
- Do not convert human labels into operating architecture.
- Do not seed the unseen repository with project-specific answers before measuring cold start.
- Do not silently retry failed tasks until one passes and report only the winning attempt.
- Preserve refutations and `UNKNOWN` outcomes in the report.

## Output

Produce a machine-readable run artifact plus a concise markdown report so later versions can be compared against the same task sequence.

The report must distinguish:

```text
model capability improvement
project knowledge reuse
workflow/control-plane improvement
human intervention
```

Do not attribute all improvement to Sensei memory if the provider/model changed between runs.

## Success criterion

Across a real task sequence, at least one later task demonstrably benefits from knowledge created by an earlier task, while human technical intervention and rediscovery are measured rather than hidden. The benchmark is useful even if the result is negative.
