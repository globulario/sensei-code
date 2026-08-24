# Implementation brief: remaining observation-suite correctness findings

This branch is a holding brief for concrete correctness defects found by the repaired observation suite that should not be lost while larger architectural work proceeds.

## Findings to verify on the current head

Before fixing anything, reproduce each against the current branch/main. If a finding no longer reproduces, record the commit that invalidated it and do not re-fix it.

1. **Claim provenance fail-open**: only the literal source `inference` is rejected; empty/unknown/alternate reasoning labels may be treated as established provenance. This should be implemented in the dedicated provenance PR, not duplicated here.
2. **Consequence assessment control-flow dependency**: `AssessConsequences` is reached only from the consequence-blind-spot path; current candidate-edit staging masks the omission. This should be implemented in the dedicated consequence PR.
3. **Observation condition overclaim**: user-facing/route text must not claim "no file is written" when the lane merely prevents mutation of the governed checkout and reports disposable-workspace writes.
4. **Git read instrumentation can itself write**: `git status --porcelain` may refresh index stat data unless optional locks/index refresh are suppressed. This should be implemented in the dedicated observation-instrumentation PR.

## Purpose of this holding brief

Do not merge duplicate implementations from this branch. Its job is to make the concrete findings visible as a checklist until their dedicated PRs land.

After each dedicated fix merges, mark the corresponding item resolved with the merge SHA and remove this holding PR when all items are accounted for.
