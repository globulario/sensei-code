# Implementation brief: measure whether observation needs a second read-only round

## Why this exists

The ten-audit suite showed high apparent precision and only one obviously depth-limited audit (`testname`: 22 of 30 relevant files inspected, 35 findings). Do not add a read-only worker because the architecture has an empty box for one. Make the second round earn its existence.

## Experiment

Use the original round-1 `testname` task and findings F1 unchanged. Run a second read-only inspection with only this additional instruction:

```text
Continue the same read-only audit.
These findings were already produced by round 1: <F1>
Do not repeat them.
Inspect the remaining relevant scope and anything round 1 may have missed.
Report only additional findings supported directly by repository evidence.
It is valid to find nothing.
```

Do not hint at expected defects.

## Score F2 separately

Record:

- additional findings;
- verified true;
- true fact with overstated implication;
- false;
- unverifiable;
- duplicates/rephrasings of F1;
- new relevant files inspected;
- duration/cost;
- governed repo mutation;
- provider workspace writes;
- leftover worktrees/processes.

Primary metric:

```text
verified novel yield = verified new F2 / total F2
```

Secondary metric: additional relevant scope actually inspected.

## Decision rule

- Near-zero novel verified yield -> do not add a permanent worker; one-turn observation is sufficient for now.
- Repeated substantial novel verified yield with maintained precision -> a continuation/read-only worker has empirical justification.
- More findings with lower precision, duplication, or speculation -> more turns are not more knowledge; do not build the worker.

If one specimen is positive, repeat on at least two additional naturally depth-limited audits before turning the mechanism into architecture.

## Guardrails

- No mutation authority in round 2.
- No new prompt tricks between specimens.
- Permission to find nothing must remain explicit.
- Do not count repeated F1 findings as depth gain.
- Do not infer usefulness from token count or number of findings alone.

## Success criterion

The PR ends with measured evidence deciding whether a second read-only stage is needed. Code for a permanent worker is added only if repeated measured incremental yield justifies it.
