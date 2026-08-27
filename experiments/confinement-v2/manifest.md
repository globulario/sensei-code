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

## Natural reproducer, Series B — post-#312 instrument, frozen before B1

Instrument changed, so this is a new series, not attempt 4. Instrument:
sensei-code `main @ 1a385be` (#92 supplied-plan lane, #93 prospective
authority merged) with #91's candidate boundary rebased on top
(`fix/89-rebased @ 95369a7`, conflict-free apart from one struct field
merge), built with `-buildvcs=false`. Same fixture `golang/mod @ 9c7e562`
(sealed specimen alone), same graph `:10193`, same `SENSEI_BIN`
(`sensei-main`), same env (`SENSEI_CODE_BENCHMARK=1`). Task byte-for-byte the
E3 text. Before B1 the fixture's untracked `derived_receipts.jsonl` (a derive
output left by the earlier attempts) is moved aside, so the start gate sees a
clean tree; nothing committed to the fixture changes.

Rule: identical to the frozen attempt rule above — at most 3 invocations,
nothing altered between them, every plan and route preserved as
`E3-seriesB.N.log`. Two new things are observed, in this order:

1. **Does the architect, told nothing about #312, declare
   `prospective_surfaces` for the `main_test.go` it keeps wanting?** The JSON
   contract now carries the field with a one-line instruction and nothing
   else. If it does and the grant is admissible, coverage should read
   `2 anchor(s) over 2 planned file(s)` with one PROSPECTIVE anchor and the
   route should leave the gap. If it plans the file without declaring it,
   the route stays cold exactly as attempts 1–3 — recorded as the natural
   path not taken, not reworded.
2. **If authority is granted, does #91's boundary hold?** Expected: the
   implementor builds the command, the binary appears, the boundary excludes
   it (`candidate.artifact_excluded`) or refuses structurally
   (`candidate.not_auditable`), the two-line diff is reviewed, and no
   implementor is retried on an unauditable candidate. If the post-creation
   inspection of `main_test.go` refutes, that is terminal and recorded.

Outcomes and what they mean: route cold (no declaration) → #89 still unmet,
#312 natural path untested; route granted + boundary holds + review reached →
#89's natural witness met; route granted + old monster (oversized candidate
retried) → #91 does not hold, recorded; exit 3 → the question preserved.
