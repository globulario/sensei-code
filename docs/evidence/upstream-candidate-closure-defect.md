# Upstream defect: domain closure requires candidates to publish, and candidates must not

Found on the **first external fixture**, within minutes. It blocks `sensei
import` for any foreign repository, which is exactly the §8.3 path.

## Symptom

```
$ sensei import golang/sync --domain github.com/golang/sync --depth basic
  ...ready (sources 4/4, root 0/0, claims 68)

$ sensei metadata
  Authority verdict:   not_authoritative
  Authority detail:    GRAPH_DOMAIN_CLOSURE_FAILED: 24 required identities missing …
                       24 identities were expected to project and NONE did —
                       the slice was published from input it never read
```

A non-authoritative graph makes sensei-code's start gate refuse. **No governed
run can begin on a freshly imported repository.**

## Mechanism, verified in source

`cmd/awg/cmd_domain_closure.go:expectedIdentities` walks the corpus and excludes
three things: `generated/` trees, managed principle-pack mirrors, and files whose
top-level key is not a governed class. Everything else becomes *required to
project*:

```go
for _, id := range ids {
        expected[id] = awarenessNS + seg + id
}
```

It never reads `status:`.

`sensei import` writes extracted candidates to
`docs/awareness/candidates/invariant_candidates.yaml`, whose top-level key is
`invariants:` — a governed class (`"invariants": "invariant/"`). So all 24
candidate identities are required to project.

They correctly do not. `import` states it outright — *"Never auto-promotes:
extractors write candidates/intents for you to review and promote yourself"* —
and Law 5 says a candidate is not a canonical claim.

**So the build requires the publication of exactly the things the design forbids
publishing.** 24 expected, 0 projected, domain fails closure.

## Confirmed by test

Holding the candidates out of the corpus the check reads, changing nothing else:

```
before   closure FAILED  — 0/24 projected, 24 missing   →  not_authoritative
after    closure PROVEN  — 0/0  projected,  0 missing   →  authoritative
         1498 triples both times, identical content
```

Nothing was weakened. The check stopped *requiring* the publication of things
that must not be published.

## Why sensei-code never hit it

sensei-code's candidates live in `contract_unknown_*.yaml` proposals, whose
top-level key is not a governed class, so `expectedIdentities` already excludes
them. Only the **extractor's** output uses `invariants:` as its top-level key.

The defect is therefore invisible to a repository that was hand-authored and
becomes visible the first time a foreign repository is onboarded — which is
precisely the class of defect §8.3 exists to surface, and it surfaced on the
first attempt.

## The law it breaks

This is the recurring shape, twice:

- **Every representation preserves its predicate.** The candidate representation
  carries `status: candidate`. The closure check reads the identity and drops
  the predicate that says it must not project.
- **Closed vocabularies are read by membership.** The *class* check reads
  membership correctly. The *status* check is absent entirely — so it fails open
  into "required".

## Proposed fix

In `expectedIdentities`, exclude identities whose entry declares
`status: candidate` (and any other non-canonical status), the same way managed
mirrors are already excluded. A candidate should be counted as
`Excluded` — *"declared non-authority; expected NOT to project"*, which is the
field that already exists for exactly this.

The narrower alternative — teach the extractor to write candidates under a
non-governed top-level key — treats the symptom, and would leave any
hand-authored candidate under `invariants:` still failing.

## Status: FIXED UPSTREAM

`globulario/sensei` branch `fix/candidate-status-domain-closure`, commit
`80392aaf`. An entry declaring a non-canonical status is recorded in `Excluded`
— *"declared non-authority; expected NOT to project"* — the field that already
existed for precisely this.

The non-canonical set is read by **membership** (`{"candidate"}`) so an
unrecognised status stays **required**. Listing the canonical statuses and
excusing everything else would fail **open**: a typo, or a status added later,
would quietly drop an obligation.

Five regression cases: candidate-only, canonical-only, mixed (including an entry
with *no* status, which keeps its obligation), cold start, and a nested
`status: candidate` that must not excuse the governed entry containing it.

**The workaround was removed and the fixture rebuilt from scratch**, candidates
back in the corpus:

```
before fix   closure FAILED  0/24 projected, 24 missing   →  not_authoritative
after fix    closure PROVEN  0/0  projected,  0 missing   →  authoritative
             1498 identical triples, candidates present both times
```

The original failure is preserved above so the diagnosis remains auditable, and
the campaign runs on a clean substrate rather than on a worked-around one.

Notable in passing: after the exclusion the domain expects **0** identities to
project. `golang/sync` has no hand-authored governed identities at all — only
generated structure. That is the genuine cold-start condition the experiment
wants.
