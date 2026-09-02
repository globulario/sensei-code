# Campaign 001 — baseline, not a trend

The first campaign has nothing to converge from. Recording it as *converging*
would be the flattering error, so `Judge` returns `unmeasured` for it and this
document says the same.

Measured 2026-09-01 across `globulario/sensei` and `globulario/sensei-code`.

## Numbers

```
independent findings                26      (Codex, this session, across 5 PRs)
  #318  17    #320  6    sensei-code#132  3    #135 0    #136 0
repeated                            unmeasured
novel                               unmeasured
human transfers of old lessons      unmeasured
laws autonomously surfaced          0        MEASURED, and it is zero
mechanisms removed                  2
GUARD proof strength delta          +38
GUARD fail-closed delta             +9
GUARD constitutional moves          0
```

## The one number that is measured and zero

**Laws autonomously surfaced: 0.** Not unmeasured — measured. Across an entire
session in which the graph held the cap law, the lifecycle law and the
representation law, Sensei surfaced **none of them** to a change that was about
to violate them. Every one of the 26 findings came from a human or from Codex.

That is the capability the whole program targets, and this is its baseline.

## What is deliberately unmeasured

`repeated` vs `novel` requires deciding whether a finding instantiates a class
already in the graph, and `human transfers` requires deciding whether a lesson
the human carried was one the system already held. Both are relevance
judgements — the third rung — and typing them as unmeasured is the honest
alternative to a plausible number nobody could check.

They are not zero. Zero would claim none occurred, and several plainly did:
the cap law recurred across at least three surfaces, and the owner carried the
"could this evidence have failed" question into the work repeatedly.

## Guards

| guard | value | note |
|---|---|---|
| proof strength | **+38** | falsifiers and mutation checks added; none deleted |
| fail-closed | **+9** | new Unknown/refusal members: reachability states, transport outcomes, examination states, basis set |
| constitutional moves | **0** | required |

No falsifier was deleted. No Unknown was turned into an empty. No severity was
lowered. Self-review was never counted as independent review, and one PR was
explicitly **not** merged on a verdict belonging to an earlier commit.

## Mechanisms removed

1. **Lifecycle predicate** — four call sites with three semantics, now one
   owner (`golang/subjectstate`), with change-impact wired as the first
   consumer. Preflight and briefing remain, so this is 1 of 3.
2. **A redundant direct-anchor guard** in prospective retrieval, found because
   a mutation reported a hole that was duplication.

## The unflattering count this campaign should carry forward

**Six of my own falsifiers did not cross the seam they claimed to test.** In
each case a mutation survived and chasing it exposed the gap:

```
a coverage assertion that asserted the wrong predicate
a fixture whose stub answered "no" regardless, so class breadth was untested
a block scanner that read 102 of 103 bindings and reported success
a negative control whose condition could not occur -- three times
an "uncapped" copy that aliased the mutated object
an aliasing test that appended to a full-capacity slice
```

The instrument that caught all six was mutation testing, not review and not
reading. If campaign 002 wants one number to improve, this is the one.
