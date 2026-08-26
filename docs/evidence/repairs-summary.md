# The two repairs, separated by where they live

The distinction is the finding, not bookkeeping. **Sensei-code obeyed the
information Sensei gave it.** One defect was in sensei-code's own wiring; the
other was in the knowledge it was given.

## Repair 1 — product wiring defect. Fixed in sensei-code.

**Class:** existing mechanism, broken wiring.

`routePlan` built its `Action` without `DerivedCoverage`, so the router's only
coverage input was the graph's own. A gap could be reported, a closure round
could run, the architect could investigate — and no channel existed by which any
of that reached the decision. The product said so in its own event stream:

```
the knowledge gap did not close; escalating with it open
```

`Engine.derivedCoverage` already computed the evidence. `internal/derived`
already derived it. `relevance.go` already decided whether it resolves a gap.
The repair is one field.

**Accounts for:** 3 of 5 COLD refusals, all classified `JUSTIFIED_REFUSAL`.

**Safety preserved and pinned five ways:** a derivation that does not resolve the
gap still refuses; an unrecognised family still refuses; partial file coverage
still refuses; a provider-supplied relevance label still refuses; an approval
gate still outranks coverage. Mutation-verified.

## Repair 2 — governance-knowledge derivation defect. Root cause upstream.

**Class:** the graph semantically over-classified the code.

`internal/tui/model.go` is classified `SECURITY_RISK` /
`human_approval_required`, so transcript **scrolling** changes require human
approval. The file uses `internal/provider` only for identity and display, and
its login path execs `sensei-code login` without ever handling a token.

Removing the declared anchor did not clear it. A direct SPARQL query proves the
store holds **no** triple linking the invariant to the file, so the match is
derived at query time by the Sensei server — outside this repository.

**Accounts for:** 2 of 5 COLD refusals, both classified `UNNECESSARY_REFUSAL`.

**Done locally:** the over-broad declared anchor is removed, and the obligation
it carried is now proven by two regression tests rather than asserted by a
blanket gate. Strictly stronger — an approval gate asks a human whether an edit
is safe; the tests assert the property continuously and fail if the TUI ever
starts handling credentials.

**Not doable locally:** the query-time derivation. Filed as
**globulario/sensei#308** with the full falsification chain.

**Refused deliberately:** renaming `loginMenu`, `providerLoginFinishedMsg` and
`renderProviderLogin` would clear the gate immediately. That is tuning code to
escape a governance classifier — better benchmark number, worse repository.

## What the split says about the architecture

Sensei-code did not misbehave in either case. It was missing a wire in one, and
correctly obeying inaccurate knowledge in the other. A governed system is only
as good as the graph it consults, and this campaign found a defect on each side
of that boundary.

## Predictions for REPAIR_VERIFICATION

Recorded **before** the run, so they cannot be rationalised afterwards.

| task | COLD was | predicted | why |
|---|---|---|---|
| `internal-architect-2e095c4` | REFUSED | **may deliver** | coverage-gap refusal; Repair 1 supplies the channel |
| `internal-setup-16ecbc3` | REFUSED | **may deliver** | same |
| `internal-setup-e645669` | REFUSED | **may deliver** | same |
| `internal-tui-ea046ba` | REFUSED | **still REFUSED** | upstream classifier unchanged |
| `internal-tui-be512db` | REFUSED | **still REFUSED** | upstream classifier unchanged |
| the 4 that delivered | CORRECT | **still CORRECT** | no correctness path was touched |

"May deliver" rather than "will": Repair 1 supplies the channel, and whether a
derivation actually resolves each gap depends on whether a committed recipe
covers those files. **The channel existing is what was repaired; the evidence
arriving is a separate question this campaign has not answered.**

The safety criterion dominates all of it: **no `CORRECT → INCORRECT`
transition.** Trading fewer refusals for more wrong code would make the repair a
regression whatever the delivery number does.
