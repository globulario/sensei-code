# C2 pre-freeze measurement — all four planned files, individually

Per M29: freeze only after the exact planned set is shown examined at the
graph the run will use. `sensei preflight -addr localhost:10122 --domain
github.com/globulario/sensei-code --mode compact -json --file <f>`, producer
sensei `f79f96f9`, graph `42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64`,
CURRENT (`graph.metadata.json`).

```text
file                                status     anchors  indexed  sufficient  examined
internal/workflow/engine.go         OK         3        1/1      true        yes
internal/workflow/testedit.go       OK         1        1/1      true        yes
internal/workflow/engine_test.go    DEGRADED   0        1/1      true        yes (examined, no rule; coverage blind spots only)
internal/workflow/routine_test.go   EMPTY      0        1/1      true        yes (examined, no rule)
```

Contrast with C1's refused pair, measured at the same graph:
`testedit_test.go` DEGRADED indexed 0/1, `suppliedplan_test.go` EMPTY indexed
0/1 — "no files examined". sha256 (first 16) per file:
    graph.metadata.json  598789c20560bc41
    internal_workflow_engine.preflight.json  1bcad5f76cda70b9
    internal_workflow_engine_test.preflight.json  bdc06627d37ae9ac
    internal_workflow_routine_test.preflight.json  119e9a229ddd8c5d
    internal_workflow_testedit.preflight.json  e6fe41f9d8a34b58
