# Self-improvement program: repository-grounded inventory

The program document asks for this before any change is proposed. Every number
here was measured on 2026-09-01, not estimated.

## How to reproduce, and against what

Two repositories are measured, and most of the counts are in the OTHER one.
That was not stated and made several paths look nonexistent:

```
globulario/sensei        52b4ae5c   items 1, 2, 3, 7   (golang/server, docs/awareness)
globulario/sensei-code   main       items 4, 5, 6      (internal/, experiments/)
```

The commands, so the numbers can be re-run and disagreed with:

```bash
# 1  surfaces reconstructing governed subject state          [sensei]
grep -c "GetDirectInvariants()\|DirectInvariants" golang/server/*.go
grep -n "isPrimaryStatus(n.GetStatus()" golang/server/*.go | grep -v _test

# 2  knowledge identity chain                                [sensei]
git log -1 --format="%h %cI" -- docs/awareness/
sensei briefing --file golang/server/preflight.go | grep -E "Build commit|Build time"

# 3  dangling baseline composition                           [sensei]
awk -F"\t" '"'"'!/^#/ && NF>=2 {split($1,a,"#"); print a[2]}'"'"' \
  docs/awareness/dangling_refs_baseline.tsv | sort | uniq -c

# 4  causal envelope of acceptance-critical records          [sensei-code]
grep -rn '"'"'CallTool("awareness_preflight"'"'"' internal/ | grep -v _test

# 7  semantic-compression candidates                         [sensei]
grep -rn "primaryOnly(\|capNodes(" golang/server/*.go | grep -v _test | wc -l
```

Two of the seven items produced a worse answer than expected, and one produced
a better one. Those are marked.

---

## 1. Surfaces independently reconstructing governed subject state

Counted over `golang/server/*.go` and `cmd/awareness-mcp/*.go` (non-test):
anchor references, governance-verdict assignments, lifecycle predicate uses.

| surface | anchor refs | verdict assignments | lifecycle uses |
|---|---|---|---|
| `preflight.go` | 5 | **21** | 18 |
| `briefing.go` | 7 | 8 | 7 |
| `change_impact.go` | 3 | 5 | 4 |
| `knowledge_scoring.go` | 1 | 0 | 4 |

Three surfaces decide; a fourth owns part of the lifecycle answer.

**The sharper measurement is the lifecycle predicate itself.** Four call sites,
**three different semantics**:

```
briefing.go:148         isPrimaryStatus(n.GetStatus(), nodeScore{})            authored status only
change_impact.go:73     isPrimaryStatus(n.GetStatus(), s.scoreNode(ctx, ...))  status + promotion
preflight.go:145        isPrimaryStatus(n.GetStatus(), s.scoreNode(ctx, ...))  status + promotion
knowledge_scoring.go:130 isPrimaryStatus(n.GetStatus(), sc)                    its own score
```

`briefing.go` is deliberately narrower for a stated latency reason, which makes
it a documented divergence rather than an accident — but it is still a third
answer to "is this knowledge live", reached by a different route.

This is Priority 2's evidence, and it is stronger than "twenty bugs": the same
question has four owners.

## 2. Knowledge identity chain — **the reachability gap, measured**

```
authored HEAD           52b4ae5c   2026-08-31T23:17Z
corpus last touched     87c5fdfd   2026-08-31T23:09Z
seed artifact commit    87c5fdfd   9351 lines
LIVE store build        96f19456f5fb   2026-08-20T16:24:25Z
```

**The live decision surface is eleven days behind the authored corpus.**

Everything merged today — the #317 cap law and its three entries, the #319
transport law and its twelve Test nodes, every repair in #318 and #320 — is
authored, reviewed, merged, and **unreachable by any decision Sensei makes**.

Nothing reports this. The briefing prints `Authority: authoritative (current)`
because the live store matches *its own* expected marker; "current" is measured
against the published artifact, never against the corpus that has moved past it.
That is Priority 1 exactly: absence of publication is not reported at all,
neither as stale nor as unavailable.

## 3. Dangling-reference baseline composition

233 tolerated entries:

| class | count |
|---|---|
| `ForbiddenFix` | 161 |
| `Test` | **72** |

The 72 `Test` entries are proof edges. Split by form:

| form | count | verifiable? | result |
|---|---|---|---|
| `path_test.go:TestName` | 38 | yes | **all 38 resolve on disk** |
| bare `TestName` | 34 | **no** | needs alias resolution |

**Better than feared, and precisely located.** These are dangling at the
*graph-citation* rung — cited with no Test node defining them — not at the
referent rung. The referents exist. So the baseline is not currently hiding dead
proof edges in `sensei` the way two live ones were found in `sensei-code`.

The 34 bare-name entries are the real gap: **no checker can resolve them**, which
is the limitation stated when the `sensei-code` checker shipped. They are
unverifiable rather than verified-good.

## 4. Causal envelope of acceptance-critical records

Preflight is the decisive evidence-producing call. Sites that issue one, and
whether the request is recorded beside the result:

**CORRECTED: it is one of SEVEN, not one of six.** Codex found a site this
inventory missed, and it is arguably the most acceptance-critical of them:
`internal/assist/packet.go` stores its preflight in a digest-bound
`ContextPacket`, and `internal/assist/handoff.go` compares a handoff's changed
files against that packet's scope **to accept or reject it**. Repaired in the
same PR as the rest of the class.

| site | records request |
|---|---|
| `engine.go:2279` (`routePlan`) | **yes** — #137 |
| `assist/packet.go:134` (**missed by this inventory**) | no → now yes |
| `engine.go:639` (start gate) | no |
| `engine.go:945` | no |
| `engine.go:2569` | no |
| `engine.go:4250` | no |
| `assisted.go:402` | no |

**One of six.** The program document anticipated this: #137 is an exemplar, not
the end of the class. `awareness_audit_diff` (`engine.go:1426`) and
`awareness_edit_check` (`routine.go:360`) were not checked for the request half
and belong in the same audit.

## 5. Review outcome → future candidate ranking

**No such path exists** — but the packages I searched were the wrong ones, and
the conclusion survives only because the right ones were checked afterwards.

`internal/derived` stores INFERENCE outcomes, `internal/decision` records
architectural decision proposals, and `internal/architect` implements reporting
and commands. None of them owns implementation-review verdicts. Those are
persisted through `internal/taskstate`, `internal/runreceipt` and
`internal/workflow`.

Re-checked there: those packages retain review outcomes and **nothing consumes
them to rank or generate anything**. The answer is unchanged; the reasoning that
reached it first was wrong, and a right answer from the wrong evidence is worth
recording as such.

Priority 8 asks whether the loop exists before asking to improve it. It does not.
That is a clean answer and it means Priority 8 is a build, not an audit — and it
is correctly ordered last.

## 6. reflex-v1 sealed state

| # | subject | state |
|---|---|---|
| 1 | `code_symbol.go` | **aggregate-touched** |
| 2 | `controlstate_provider.go` | sealed |
| 3 | `intent_triggers.go` | sealed |
| 4 | `provenance.go` | sealed |
| 5 | `query.go` | **aggregate-touched** |
| 6 | `runtime_evidence.go` | sealed |
| 7 | `surface_limits.go` | **contaminated — removed** |

Four fully sealed, two aggregate-touched (grep counts only, disclosed with what
they revealed), one removed. Both disclosures are in
`experiments/reflex-v1/EXCLUSION.md` with the mechanism recorded.

**The mechanism is itself a finding.** The exclusion list lives in a document and
in nothing that runs. A deliberate sweep respected it; a debugging step did not;
an aggregate count did not. Each time the list was correct and nothing consulted
it. **Prose is not a guard** — which is the same shape as Priority 3's baseline
and Priority 1's silent staleness.

## 7. Semantic-compression candidates

| mechanism | call sites |
|---|---|
| `primaryOnly` | 18 |
| `capNodes` | 17 |
| `sortBySeverityID` | 6 |
| `isPrimaryStatus` | 5 |
| `ofClass` | 5 |
| `subjectAnchorIDs` | 4 |
| `mergeAnchors` | 2 |

Seventeen `capNodes` call sites is the surface area over which "a cap must not
reach a decision" has to hold, and it is enforced today by reading each one.

---

# Proposed sequence

The document's order is A→H. One deviation is proposed, with a reason.

**A. Publication reachability visibility — first, and it changes what everything
else can claim.** Until a decision can state which corpus generation produced it,
every other priority is verified against a surface eleven days stale. Concretely:
carry the authored-corpus revision into the published artifact, and have
briefing/preflight report the *distance* between the corpus and the generation
they consulted. Report staleness as staleness — never as absence of law, and
never by rebuilding automatically.

**B. `GovernedSubjectState`.** The four-owner lifecycle measurement is the case
for it. Mutation tests target the owner, not each consumer.

**C. Proof-edge hardening — narrower than expected.** The 38 path-form entries
resolve; the work is the 34 bare-name ones, which need alias resolution before
they can be classified at all. Ratchet: no new dangling proof edges, and the
count may not rise.

**D. Causal envelope — five more preflight sites plus audit_diff and edit_check.**

**E. Prospective retrieval.** Blocked on A: retrieval against a stale generation
would measure the wrong corpus.

**F. reflex-v1**, once A makes the law reachable. n=6, two marked.

**G. Feedback adaptation** — a build, not an audit.

**H. Convergence campaigns.**

## Deviation

**Do C's bare-name resolution during A, not after B.** Both need the same thing:
a canonical way to resolve an authored identity to the artifact it names. Doing
them separately builds that resolution twice, which is the defect this whole
program is about.

## What this inventory does not establish

It does not show that any of these are the *most* valuable changes, only that
each is real and measured. It does not touch the four fully sealed subjects. And
it makes no claim about semantic relevance anywhere — every number here is a
structural fact, and the places where structure runs out are marked as such
rather than filled with a boolean.
