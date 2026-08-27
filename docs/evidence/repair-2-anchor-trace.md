# Repair 2 — where the credential anchor actually comes from

**The declared anchor was real and is narrowed. It is not the dominant cause,
and the dominant cause is upstream of this repository.**

## What was traced, in order

**1. The declared anchor.** `sensei_code.provider.credentials_remain_provider_owned`
listed `internal/tui/model.go` in `protects.files`. Removed, rebuilt, republished
(34,236 triples). **The gate still fired.** So the declaration was not what
produced it.

**2. The `required_tests` I added.** Hypothesis: pointing them at
`internal/tui/credentials_test.go` re-created the link. Tested by removing them,
committing, rebuilding. **The gate still fired.** Reverted; hypothesis wrong.

**3. Package reference.** Hypothesis: referencing `internal/provider` inherits
the gate. Falsified directly —

```
internal/doctor/doctor.go     ARCHITECTURE_SENSITIVE  approval=none
internal/workflow/engine.go   ARCHITECTURE_SENSITIVE  approval=none
internal/agent/agent.go       ARCHITECTURE_SENSITIVE  approval=none
```

all use `internal/provider` and none inherits it.

**4. A recorded decision joining invariant × file.**
`decision.sensei_code.add_a_read_only_tui_readiness_report_command` lists the
credentials invariant in `related_invariants` and `internal/tui/model.go` in
`source_files`. Plausible — and falsified: other `source_files` of the same
decision do **not** inherit the gate.

```
README.md                     LOW_RISK                approval=none
internal/setup/setup.go       LOW_RISK                approval=none
```

**5. A stored anchor of any kind.** Queried the live store directly:

```sparql
SELECT ?p ?o WHERE {
  ?s ?p ?o .
  FILTER(CONTAINS(STR(?o), "internal/tui/model.go"))
  FILTER(CONTAINS(STR(?s), "credentials_remain_provider_owned"))
}
-> {"bindings": []}
```

**No triple links the invariant to the file.** The narrowing took effect in the
store, and the server still reports the invariant as a direct anchor for that
file. **The match is therefore derived at query time by the Sensei server, not
read from a stored relationship.** That is the load-bearing finding, and it is
established rather than inferred.

**6. Reference expansion.** Hypothesis: importing `internal/provider` pulls in
invariants protecting it. Falsified — `internal/doctor/doctor.go` and
`internal/workflow/engine.go` both import it and neither lists the invariant.

**7. Symbol naming — the surviving hypothesis.** The file declares:

```
internal/tui/model.go:loginMenu
internal/tui/model.go:providerLoginFinishedMsg
internal/tui/model.go:renderProviderLogin
```

and it is the only property distinguishing it from `doctor.go`, which imports
the same package and is not gated. Preflight's own blind spot reads *"anchored
entity in security/auth/rbac/pki/jwt/cert namespace"*, and Sensei's
`risk_classify.go` matches `securityKeywords` — including `"credential"` — against
the *"concatenated lowercase id title summary haystack of anchored entities"*.

**Stated as the surviving hypothesis, not as proven.** Four alternatives are
falsified with evidence above; this one is not, and confirming the exact rule
needs the Sensei maintainer. The actionable conclusion does not depend on which
derivation it is: **no stored triple produces this, so it cannot be repaired
from `sensei-code`.**

## What this means for the repair

The mandate said: if the anchor is generated rather than declared, fix the
source of the derivation, not the generated output. **That source is the Sensei
graph builder's symbol-namespace classification, which lives in the `sensei`
repository, not in `sensei-code`.** It cannot be repaired from here, and
patching the generated SCIP index would be patching the output.

Repair 2 as delivered is therefore **partial, and its limit is stated rather
than discovered later**:

- **Done:** the over-broad *declared* anchor is removed, and the obligation it
  carried is now proven by two regression tests rather than asserted by a
  blanket gate. That is strictly stronger — an approval gate asks a human
  whether an edit is safe; the tests assert the property continuously and fail
  if the TUI ever starts handling credentials.
- **Not done, and not doable here:** the symbol-namespace derivation. The two
  `internal/tui` tasks are expected to **still refuse** in REPAIR_VERIFICATION.

Predicting that now is the point. A verification run that shows those two still
refusing is **not** a failed repair — it is the predicted consequence of a cause
this repository cannot reach.

## The honest classification, revised

The proof-v6 diagnostic classified these two refusals `UNNECESSARY_REFUSAL`, on
the evidence that a scrolling change touches no credential path. **That
classification stands** — the refusal still protects nothing.

What changes is the *repair address*. It is not sensei-code's awareness data. It
is either:

- **upstream:** the namespace classifier should not treat a symbol named
  `renderProviderLogin` in a view layer as a credential-owning entity; or
- **local and cosmetic:** renaming those three symbols would clear the gate,
  which would be tuning code to satisfy a benchmark and is **not done**.

The second option is worth naming precisely because it is available and would
have "fixed" the number. Renaming a field to escape a governance classifier is
the kind of change this whole campaign exists to refuse.

## Recommendation

File upstream against the Sensei graph builder with this trace attached. Until
then, the two `internal/tui` refusals remain a known, understood, and
correctly-classified delivery cost.

## Correction (2026-08-27) — the "surviving hypothesis" was wrong

The anchor was **not** derived at query time. Two errors in this trace, both
mine:

1. The SPARQL filtered `?s` on the invariant and `?o` on the file. The stored
   edge runs the other way — `<file model.go> aw:implements <invariant>` — and
   `ImpactForFile` reads exactly that arm. The "no stored triple" result was a
   query in the wrong direction.
2. The served dev graph was built 2026-08-18, before `45e484f` removed
   `model.go` from the invariant's `protects.files`. A fresh isolated build of
   today's corpus (28,788 triples, closure PROVEN 36/36) has **no** credentials
   edge on `model.go`; the live store still has it. Stale publication, not
   derivation.

`globulario/sensei#308` is closed with this finding. The real gap it exposes is
freshness semantics: a store built from an older corpus reports
`GRAPH_FRESHNESS_STATE_CURRENT` because freshness is measured against the
published artifact rather than the corpus it was built from. That is why a
removed anchor kept refusing tasks for a week.
