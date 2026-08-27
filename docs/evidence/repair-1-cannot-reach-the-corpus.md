# Repair 1 cannot change any task in the corpus

Found before the run, for free. **REPAIR_VERIFICATION must not be launched as
armed.**

## The finding

`derivedCoverage` loads `docs/awareness/derived_recipes.json` and covers a file
only when a recipe's `dir` contains it. That file holds **one recipe**:

```json
{ "kind": "field_access_under_lock", "dir": "internal/event",
  "type": "Bus", "field": "subs", "lock": "mu" }
```

The corpus touches eight packages. `internal/event` is not among them:

```
internal/agent  internal/architect  internal/assist  internal/decision
internal/gitx   internal/session    internal/setup   internal/tui

recipes: 1, covering [internal/event]
tasks reached: 0 of 11
```

Repair 1's channel is wired and **carries nothing here**. Running the campaign
would re-observe the proof-v6 refusals and credit the repair for a result it
could not have caused. Pinned as a tripwire in
`internal/derived/corpus_reach_test.go`, which fires the moment a recipe reaches
a corpus directory.

## Two different coverages, and only one is a defect

**Graph coverage** — `internal/setup/checks.go` has zero rows because nothing has
ever been recorded about it. The graph is authored through `sensei propose`, so a
file gains coverage when someone records a lesson about it. 24 of 97 non-test
`.go` files have none. That is not a bug; it is **cold start**, and it is a
property of any system whose knowledge is earned.

**Derived coverage** — the recipe mechanism is the system's *answer* to cold
start. Hit "no anchored rules apply", investigate, record the durable question,
and that question anchors coverage next time. The loop is real and the design is
sound.

It has produced **one recipe** across the project's history. The recipe's own
note says why it exists:

> *internal/event/bus.go answered `PREFLIGHT_STATUS_EMPTY` with 'no anchored
> rules apply'. A closure round read the code and found the discipline worth
> checking.*

So the loop has fired exactly once, on exactly the trigger the benchmark keeps
hitting. **The compounding hypothesis is at n = 1.**

## What this says about the escalation

Your reading holds and the data now supports it directly: **the task statement is
the human intent**. A human authorised the work by asking for it. A second human
decision is warranted only when the intent collides with an architectural rule —
not when Sensei lacks an index entry, and not when it has no rule to apply.

The three coverage-family refusals asked a human to supply something the human
does not have. `checks.go` is unindexed; the human knows nothing about it that
reading the file would not establish. The only available answer was "go ahead".

Repair 1 is the right shape — close the gap by derivation instead of escalation.
It simply has no derivations to close these gaps with.

## What this does NOT establish

- **Not** that Repair 1 is wrong. It is unreachable on this corpus, which is a
  different claim and a fixable one.
- **Not** that the refusals were justified. That question stands unresolved.
- **Not** anything about Repair 2, which changed an invariant anchor and does
  reach `internal/tui`.

## Options, none of them taken

The choice is the user's, and each has a cost worth stating plainly.

1. **Write recipes for the corpus packages.** Direct, and the most dangerous
   thing on this page: authoring recipes for the exact tasks about to be
   measured is tuning the subject to pass its own test. If done at all it must
   be done blind to which tasks refused, and recorded as corpus preparation
   rather than a repair.
2. **Run the campaign anyway, as a control.** It would confirm the refusals
   reproduce and measure Repair 2 in isolation. Honest, and expensive for what
   it returns.
3. **Change the routing rule** so "no anchored rules apply" no longer escalates.
   This is what your intent principle actually implies. It is a **production
   behaviour change**, currently forbidden before the run, and it would need its
   own evidence.
4. **Test the closure loop directly instead.** Ask whether Sensei, hitting "no
   anchored rules apply", can run a closure round and write a recipe *itself* —
   which is the compounding claim, currently at n = 1. This measures the
   mechanism that is supposed to solve cold start rather than measuring the
   corpus.

Option 4 is the one that tests the thesis. Option 1 would corrupt it.

## Status

```
REPAIR_VERIFICATION   ARMED but must not run — would misattribute its result
Repair 1 reach        0 of 11 tasks
Repair 2 reach        internal/tui — 2 tasks, unaffected by this finding
cold start            confirmed as the real obstacle
compounding loop      n = 1 recipe, produced by exactly this trigger
```
