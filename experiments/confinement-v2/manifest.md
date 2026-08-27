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
