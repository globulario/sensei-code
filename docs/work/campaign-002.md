# Campaign 002 — the first trend point, and it is not converging

Campaign 001 was a baseline with nothing to converge from. This is the second
campaign, so `Judge` can compare for the first time. It cannot, and the reason
is worth more than the number would have been.

Measured 2026-09-02 across `globulario/sensei`, one session.

## Numbers

```
independent findings                unmeasured   review has not concluded
  repeated                          unmeasured
  novel                             unmeasured
human transfers of old lessons      unmeasured   but see below; it is not zero
laws autonomously surfaced          0            MEASURED, again
mechanisms removed                  2
GUARD proof strength delta          +12
GUARD fail-closed delta             +7
GUARD constitutional moves          0            with one reversal, declared
```

`Judge` returns **unmeasured**: `independent findings were not measured in both
campaigns`. Campaign 001 counted 26 Codex findings. This campaign produced six
pull requests across the two repositories, and **no independent reviewer read
any of them**: `Hosted adversarial review` is SKIPPED on every check run in this
repository for want of a provider key, and the reviews attached at merge were
written by the same actor that wrote the changes. Recording 0 would claim no
findings exist when nobody has looked — the exact substitution this package
types `Measured` to prevent.

That is also the honest reason this campaign cannot be compared to 001: 001's 26
findings came from a reviewer that was not the implementer, and this campaign
has no such number at all. The two are not a trend of one metric; one of them is
missing the metric.

## Laws autonomously surfaced: 0, for the second time

Two defects were found in this session:

- six surfaces suppressed the reachability report exactly when the graph could
  not state its own revision (sensei#334);
- `Metadata` scoped its counts to the requested domain and its authority verdict
  to the home domain (sensei#335).

**Neither was surfaced by Sensei.** Both were found by reading code.

Worse than that, and this is the finding: the two laws that located them —
*"unavailable must not become absent"* and *"a name is not a scope"* — were
carried in by the implementer from prior sessions. Sensei holds both. It holds
the first as the governing contract of
`failure.sensei.a_decision_surface_reported_current_while_the_admitted_knowl`,
which the #334 defect violates *by name*. It was never brought to the change.

So `human transfers of old lessons` is typed unmeasured rather than counted, but
it is **certainly at least 2**, and the transfers were of laws the graph already
held. That is criterion 5 failing in the most direct way available: the law
existed, in this graph, describing this exact shape, and a human moved it.

### And retrieval could not have run

The live briefing surface refused three times with marker mismatches and could
not serve `github.com/globulario/sensei` locally at all. The briefing that
governed both changes was read out of the authored YAML by hand.

That is criterion 1 measured a third way. It is also why "laws autonomously
surfaced: 0" is not yet evidence about retrieval *quality*: retrieval did not
run. Nothing was ranked badly; nothing was ranked.

## Measured afterwards: the graph held both laws directly

`criterion-5-the-graph-already-knew.md` measures the counterfactual, and it is
sharper than "retrieval did not run". For both defects the governing law was
**directly anchored to a file that was changed, and was the only thing the graph
says about that file** — rank 1 of 1, twice. #335's law even prescribes the
repair it needed ("one evaluation answering two surfaces, never a second
evaluation that agrees today").

So `laws autonomously surfaced: 0` is not a retrieval score. Nothing needed
inferring or ranking. The cheapest possible retrieval case failed, because the
surface could not serve.

## One deterministic gate did fire, and it is not the same thing

`yaml2nt`'s dangling-reference ratchet — added by #323 for exactly this class —
caught a defect in **this session's own change**:

```
dangling Test reference: ...:TestMetadataAuthorityAnswersForTheDomainItWasAsked
  (no aw:authoredIn — no schema defines this anchor)
1 new dangling reference(s)
```

The test existed and passed. The governed node defining the anchor did not.
`reference ≠ referent` caught in new work, by a check written for it, before any
reviewer.

**This is not criterion 5 and must not be counted as it.** A gate that refuses a
defect after it is written is not a law surfaced to the change that needed it.
It is the *outcome* the program's closing sentence describes — "the next defect
of a class already learned becomes harder to write" — reached by the
deterministic route rather than the prospective one. Recording it in the
`AutonomousSurfaced` column would make the primary capability look present
because a different mechanism worked.

## Guards

| guard | value | note |
|---|---|---|
| proof strength | **+12** | 8 new test functions, 3 surfaces added to an existing drift detector, 1 vacuous-pass guard; none removed |
| fail-closed | **+7** | 6 surfaces now report reachability `unknown` where they reported nothing; metadata now refuses authority for an unproven requested domain |
| constitutional moves | **0** | one reversal, declared below |

Every new assertion was mutation-tested against the pre-change source. Eleven
falsifiers, eleven KILLED. No falsifier was deleted, no Unknown became an empty,
no severity was lowered, nothing was published to make a line green.

### The declared reversal

sensei#334 reverses two existing assertions:

```
TestAnMCPAuthorityWithoutABuildCommitCarriesNoAssessment  -> ...ReportsUnknown
TestAResponseWithNoBuildCommitCarriesNoAssessment         -> ...ReportsUnknown
```

They asserted that a graph stating no build commit carries *no* reachability
key. Rewriting an assertion to fit changed behaviour is on the forbidden list
unless the specification actually changed, so this is recorded here rather than
absorbed:

- the authored contract says an unestablished generation is reported as
  unestablished and **never as absence of law**; an absent key is absence;
- `reachability.Assess` answers that input as `Unknown` in its first case, so
  the surfaces were discarding an answer, not declining to invent one;
- the direction is toward **more** refusal, not less. The change adds an Unknown
  state to five outputs and removes none.

`ConstitutionalMoves` is recorded as 0 on that reading. **A reviewer may
disagree**, and if the original intent is upheld the correct value is 1 and this
campaign is `void_guard_moved`. The number is stated with the argument attached
so that remains checkable rather than asserted.

## Mechanisms removed: 2

1. **"Is the reachability question askable?"** — decided independently in three
   consumers, each with its own guard and its own comment justifying it. Now
   decided once, in the owner. Four call-site nil checks went with it.
2. **Metadata's authority scoping** — the only surface computing its verdict
   through `graphAuthorityFromSnapshot` (closureDomain `""`) instead of the
   shared scoped path. Now the same path as query, resolve, briefing and impact.

## What this campaign says about the program

Criterion 3 was re-measured and now **passes** in both repositories: 353 proof
edges in `sensei` and 109 in `sensei-code`, none dead; the 233-row baseline
matches today's live dump exactly. `#323` and `sensei-code#141` closed it.

Criterion 1 and criterion 5 are unchanged, and they are the same problem seen
from two ends. Publication is an owner action that has not been taken, so no
amount of retrieval work can be measured yet. **Every improvement to prospective
retrieval is currently unfalsifiable for the same reason**: it would be measured
against a generation that does not contain the laws being retrieved.

That is the honest ordering. Not "retrieval is next" — retrieval is *blocked*,
and the block is one deliberate human action, not a missing mechanism.

And per the counterfactual above, for these two specimens retrieval was not even
the mechanism that was needed. Improving it would have prevented neither defect.
