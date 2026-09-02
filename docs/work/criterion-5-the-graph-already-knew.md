# Criterion 5, measured: the graph held both laws, at rank 1 of 1, and said nothing

> 5. Sensei surfaces at least one previously learned relevant law before a
>    human/Codex reviewer points it out

Campaign 001 and 002 both record **0**. This measures *why*, and the answer is
not the one the program's execution order assumes.

Measured 2026-09-02 against the authored corpus of `globulario/sensei` at
`5d1402d8`: 210 anchors carrying path scopes.

## Preregistration

The governing law for each defect was named from the contract the defect
violates, **before** any retrieval was run, and was not adjusted afterwards.

## The counterfactual: what a working briefing would have returned

For every file changed in the two PRs this session produced:

```
#334  cmd/awareness-mcp/main.go       5 direct anchors
      cmd/awg/json_output.go          0
      cmd/awg/authority_output.go     1 direct anchor:
        failure.sensei.a_decision_surface_reported_current_while_the_admitted_knowl

#335  golang/server/metadata.go       1 direct anchor:
        failure.sensei.cheap_health_surface_read_green_while_graph_could_not_govern
```

In both cases the governing law was **the only thing the graph says about that
file**. Rank 1 of 1. No ranking problem, no relevance judgement, no nuisance.

And #335's law does not merely describe the area. It describes the defect and
prescribes the repair:

> "Metadata reported freshness only while preflight checked freshness AND the
> closure proof… A store whose closure report vouched for a different
> publication therefore read 'authoritative, CURRENT' on the surface monitoring
> reaches for, and refused on the surface that governs. **The repair is one
> evaluation answering two surfaces, never a second evaluation that agrees
> today.**"

`#335` is that failure mode recurring in the domain dimension: metadata
evaluated the closure proof for `""` while every governing surface evaluated it
for the requested domain. A second evaluation that agreed today.

**Neither law reached the change.** The briefing surface refused three times
with marker mismatches and could not serve this domain locally at all.

## What prospective retrieval contributes here: nothing, correctly

`golang/prospective` was run on the same subjects to check whether the harder
mechanism was needed. It was not, and its behaviour is right:

```
subject = the three #334 files
  target is a DIRECT anchor on cmd/awg/authority_output.go
  -> excluded from prospective results BY DESIGN, and correctly so

subject = cmd/awareness-mcp/main.go alone
  0 candidates -- because all 5 of its anchors are direct

subject = #334 files with the anchored one removed (a synthetic case)
  target surfaced at rank 42 of 52, basis=resemblance, authority_eligible=false
```

Only the third is a retrieval measurement, and it is a **constructed** case: it
required deleting the file the law is actually anchored to. Real recall for
these two defects was not 0.34 or rank 42. It was **1 of 1, twice**, on the
direct path.

## The consequence for the program's ordering

Priority 6 exists because "Sensei often knows the law but does not bring it to
the change that needs it". That is exactly what happened — and **not for the
reason Priority 6 addresses.**

Nothing had to be inferred, ranked, or judged for relevance. The two laws were
authored, merged, directly anchored to the exact files, and unique on those
files. The only thing between them and the change was a decision surface that
could not serve.

```
criterion 5 failed here
  because criterion 1 failed
  and for no other reason that this session can find
```

So improving prospective retrieval would not have prevented either defect, and
measuring such an improvement today is impossible for the same reason: it would
be scored against a generation that does not contain the laws being retrieved.

**This is a claim about two specimens, not a proof about the corpus.** It does
not show retrieval is unnecessary in general — the 0.34 leave-one-out recall
recorded in `invariant.sensei.resemblance_may_guide_a_change_but_never_govern_it`
still stands for subjects with no direct anchor. It shows that the two defects
this program actually produced were both fully governed by direct anchors, which
is the cheapest possible case, and that case failed.

## Reproducing it

```go
anchors := corpusAnchors(t)                      // golang/prospective eval helper
Retrieve(Subject{Files: subject}, anchors, nil)  // prospective path
// direct path: entries whose protects.files contains a subject file
```

The direct-anchor half needs no code beyond reading `protects.files` out of
`invariants.yaml` and `failure_modes.yaml`.
