# B3 re-measurement after N3/N4 — machine-bound

Same seven `sensei preflight -addr localhost:10122 --domain github.com/globulario/sensei-code --mode compact -json --file <f>`
calls as `../b3-baseline/`, plus `sensei metadata -json`, producer `sensei-f3` (`f79f96f9`), run from the
subject copy at `6c36961` after N4. Comparable to the baseline only with both differences stated:

- source: `6c36961` vs `7ae7236e` — Go sources byte-identical; only `docs/awareness/derived_recipes.json` differs (3 recipes vs 1)
- graph: `42e6e12c…` (164,506 triples, post-#108) vs `def94857…` (158,349) — the two B3 invariants were published between

```
surface                            baseline   now      risk                    conf     direct_invariants   why it moved
internal/workflow/engine.go        OK         OK       ARCHITECTURE_SENSITIVE  HIGH     3
internal/session/store.go          OK         OK       ARCHITECTURE_SENSITIVE  HIGH     3                   (+1: REPEATED_RESUME lists it)
internal/workflow/suppliedplan.go  EMPTY      EMPTY    LOW_RISK                LOW      0
internal/workflow/prospective.go   EMPTY      OK       ARCHITECTURE_SENSITIVE  MEDIUM   1                   REPEATED_RESUME_CANNOT_MINT (#108)
internal/workflow/testedit.go      EMPTY      OK       ARCHITECTURE_SENSITIVE  MEDIUM   1                   REPEATED_RESUME_CANNOT_MINT (#108)
internal/workflow/premise.go       EMPTY      EMPTY    UNKNOWN_IMPACT          LOW      0                   its recipe (N1) derives DERIVED but preflight does not read recipes
internal/derived/derived.go        EMPTY      OK       ARCHITECTURE_SENSITIVE  MEDIUM   1                   FUTURE_ONLY (#108)
```

Still EMPTY: `suppliedplan.go`, `premise.go`. Every surface that moved, moved by a human-committed invariant, not by a derived recipe.

sha256 (first 16) per file:
    graph.metadata.json  c6623782d5f5bd2b
    internal_derived_derived.preflight.json  579dba3729f7737d
    internal_session_store.preflight.json  9c7dc0dcd6bca1b5
    internal_workflow_engine.preflight.json  579001c7db9b2af3
    internal_workflow_premise.preflight.json  5152921c100418c3
    internal_workflow_prospective.preflight.json  51dcd17580930e10
    internal_workflow_suppliedplan.preflight.json  4c077b1d3b80f559
    internal_workflow_testedit.preflight.json  54fd3b2def92ac40
