# REPAIR_VERIFICATION halted after arm 1 — two blockers

Wave started 03:40:55Z 2026-08-26, halted after `internal-gitx-a4fa351`.
No verification result exists. Nothing about the repairs was measured.

## Blocker 1 — instrument defect #13: a timeout wearing an outage's label

Arm 1 recorded:

```
terminal   workflow.timed_out
wall       1320s          (exactly the frozen 22m budget)
infra      "backend is unreachable"
candidate  none created
oracle     NO_RESULT
```

Both fields are set, and `WorkflowTerminal` checks `Infrastructure` **first**, so
the arm scores **INFRA_FAILURE** and the timeout disappears.

The preserved transcript shows the implementor **working productively** at the
cutoff — probing `git diff --binary` on non-UTF8 bytes, per-worktree ref
visibility, base-relative vs `HEAD`-relative diffs, and confirming Go's
`encoding/json` corrupts `\xe9` to `�`. Real investigation of the actual
task, still going when the clock ran out. Nothing in the retained evidence looks
like an outage.

### The mechanism

```go
o := ArmOutcome{Raw: tail(string(out), 20000)}   // only the LAST 20KB is kept
...
if code != 0 {
    if why := infrastructureReason(string(out)); why != "" {   // scans the FULL output
        o.Infrastructure = why
    }
}
```

The classifier reads a 22-minute transcript; the ledger keeps its last 20KB. So:

1. **It can match anywhere.** `"backend is unreachable"` is an unanchored
   substring. This task is *about* MCP-boundary transport of diff evidence, so
   the model discussing that exact failure mode is entirely ordinary. Same defect
   class as the `403`-inside-a-commit-hash bug already fixed — a phrase matched
   out of context.
2. **The evidence is discarded.** The phrase is **not** in the saved transcript.
   An INFRA_FAILURE verdict is therefore **unauditable**: I cannot show it is
   wrong, and the harness cannot show it is right.

### Why this one is not tolerable

I cannot rule out a genuine transient — the MCP could have been unreachable while
Bash tools kept working. That is exactly the problem. **The instrument destroyed
the evidence needed to tell the two apart.**

And the failure is *not symmetric*. INFRA_FAILURE reads as "the environment
broke, not the product's fault." TIMEOUT reads as "the product was too slow" and
counts against it. A classifier that silently converts the second into the first
**biases the measurement in Sensei-code's favour** — the one direction this
campaign cannot afford, and precisely what the two-axis contract was frozen to
prevent.

Arm 1's record is marked `VOID_MEASUREMENT`. It is not a result.

## Blocker 2 — there was never enough quota to finish

The transcript carries the provider's own rate-limit event:

```
seven_day  utilization 0.96   resets 2026-08-26 21:00
five_hour  utilization 0.29   resets 2026-08-26 03:10
```

The **five-hour** window had reset at 23:40, which is what the quota probe saw
and why it passed on the first try. The **seven-day** window was at **96%**.

Ten remaining arms at up to 22 minutes of Opus each cannot fit in 4% of a weekly
budget. The wave would have exhausted quota partway and recorded the tail as
INFRA_FAILURE — a wave measuring its own environment, which is the proof-v5
disaster repeated with a different cause.

The gate checked the wrong window. It asked *"can I start?"* when the question
is *"can I finish?"*

## Also observed, not yet a finding

Arm 1 consumed the entire 22-minute budget against a proof-v6 COLD median of
365s. One arm is not a trend, and this task's RAW/COLD history must be compared
before it means anything. Recorded so it is not lost.

## What must happen before REPAIR_VERIFICATION runs

1. **Anchor the infrastructure classifier** and stop it overriding a terminal the
   engine already reported. A run the engine says `timed_out` is a TIMEOUT.
2. **Preserve the evidence a classification rests on.** If a phrase sets
   INFRA_FAILURE, its surrounding context must be in the ledger, or the
   classification is not admissible.
3. **Gate on the binding window.** Require enough of the *seven-day* budget to
   run every scheduled arm, not merely enough to start one.

All three are instrument repairs. **No production behaviour changes**, and the
frozen manifest, corpus, oracles, and thresholds are untouched.

## Status

`INCORRECT → CORRECT` remains **unmeasured**. The kill test in
[`decision-rule-frozen.md`](decision-rule-frozen.md) stands exactly as written.
Earliest honest re-run is after the seven-day window resets at **21:00 today**.
