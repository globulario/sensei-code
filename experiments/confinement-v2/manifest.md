# confinement-v2 — links 6–7 for the second family, with a single-file plan

Same fixture (`golang/mod @ d0a27b2`), same graph (:10193), same substrate
(`sensei@e81d7bed`), same sealed specimen persisted alone — the E2b base of
`confinement-v1` (`9c7e562`): `command_invocation_confined_to("go" confined to
gosumcheck) searched under .`, which derives `DERIVED` in a governed run.

Only the task changes. confinement-v1's E2 plan spanned `gosumcheck/main.go`
**and** `gosumcheck/test.bash`; one anchor over two files is not coverage, so
the route stayed cold for a fair reason. This task is written from one function
in `main.go` and gives the plan no reason to reach the harness.

## E3, verbatim, naming no executable

> *gosumcheck's verbose output reports, for each remote fetch, the elapsed time
> and the target. Include the number of bytes received on that same line, with
> no change to non-verbose output or to any error path.*

## Predictions

- plan stays inside `gosumcheck/main.go` → `1 anchor over 1 planned file` →
  route `architectural-authority-granted` → implementor → audit → review →
  candidate. Links 6–7 for the family.
- plan reaches `test.bash` again → 1 anchor over 2 files → cold. Then the
  finding is about the architect's planning for this command, not about the
  family, and it is recorded as such — not reworded, not re-run.
- `0 anchors` → the derivation regressed since E2b; instrument, halt.

## Natural reproducer for #89 — stopping rule, frozen before attempt 2

The reproducer reruns the **identical** E3 task on the repaired product from the
same base (`9c7e562`, sealed specimen alone). The architect is nondeterministic:
attempt 1 planned two files where the original planned one, so coverage was
insufficient for the plan, the route stayed cold and the implementor never ran.
That is the authority boundary refusing to extrapolate one-file evidence over a
two-file plan — a correct refusal, not #91 failing, and it is preserved.

Rule:

- Rerun the identical task unchanged for **at most 3 attempts**. Preserve every
  plan and route (`E3-repaired.attemptN.log`).
- Do not alter prompt, graph, recipes, coverage, fixture or task between
  attempts.
- If any naturally produced plan reaches the implementor, that attempt
  exercises #91's candidate boundary and is the reproducer.
- If none does, the natural-reproducer criterion is **unmet**, recorded as
  architect nondeterminism preventing the original failure path from being
  reached, and #89 stays open.

Two failures kept distinct on purpose:

- cold because knowledge is insufficient for the plan → correct refusal;
- candidate structurally unauditable after authority is granted → #89/#91.
