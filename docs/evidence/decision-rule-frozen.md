# The kill test — frozen before REPAIR_VERIFICATION

Recorded before the run, like every other threshold in this campaign, so no
outcome can be rationalised afterwards.

## The question, finally sharp

Not *"does Sensei-code have good architecture"* and no longer *"can it produce
correct code"* — both answered. The open question is:

> **When RAW would make a mistake, can Sensei-code use its extra knowledge and
> governance to turn that mistake into a correct result?**

Current evidence: `INCORRECT → CORRECT = 0`.

But those three RAW-wrong tasks **never got their chance**. All three stopped at
the coverage-gap path that Repair 1 just connected. The next run tests exactly
the cases that matter, which is unusually lucky.

## A correction to my own framing

I wrote that *"governance that never catches an error is ceremony."* That
understates what was measured. Refusal is not only cost — it is prevented
unsafe delivery, and here it was **not random**.

```
RAW wrong on                             3 / 11  =  27%   (base rate)
refusals landing on a RAW-wrong task     3 /  5  =  60%   (precision)
refusals capturing ALL RAW-wrong tasks   3 /  3  = 100%   (recall)

P(a random choice of 5 refusals captures all 3)  =  0.061

candidates shipped wrong:   RAW 3      COLD 0
```

Every task RAW got wrong was refused. Not one was missed. At p ≈ 0.06 that is
**suggestive of calibration and short of significance** at n=5 — but it is not
nothing, and I dismissed it too quickly.

The cost is visible in the same numbers: two refusals blocked work RAW got
right. Precision 60%, not 100%.

So the honest statement of what COLD did is **not** "it withheld." It is:
*it shipped zero wrong candidates while RAW shipped three, and it paid for that
with two false blocks and seven undelivered tasks.*

## What the ideal looks like

```
uncertainty
   ↓
detect risk
   ↓
investigate
   ↓
├─ enough evidence  → produce a CORRECT answer
└─ insufficient     → refuse, and say why
```

Stronger than RAW's *always try something* and stronger than a linter that only
says no. Today Sensei-code demonstrably does the lower branch. **The upper
branch is what has never been observed**, and Repair 1 is the wiring that makes
it reachable.

## The decision rule

| observation | verdict | next |
|---|---|---|
| `INCORRECT → CORRECT` on any of the three, no regressions | **first evidence governance changes the engineering outcome** | holdout corpus of unseen *risky* tasks |
| the three deliver but **INCORRECT** | *Repair 1 improved autonomy and failed to demonstrate a correctness benefit **on this corpus*** — a bad result, not a dead premise | stop feature expansion; small holdout before judging the thesis |
| delivery improves, gains stay **zero** | value is safety/refusal, not improved solving | one small holdout, then decide whether that narrower product is worth its cost |
| holdout also shows zero gains **and** materially slower | the architecture is not paying for itself | the criticism stands and should be acted on |
| any `CORRECT → INCORRECT` | governance steering away from correct solutions | **halt** — no wiring repair addresses that |

One `INCORRECT → CORRECT` is an **existence proof**, not a victory. Three with no
regressions is a major result. Neither generalises until a corpus these repairs
were not designed from says so.

## Why this is a good place to be

The project has a **kill test** instead of an open-ended roadmap. The next run is
not polishing. It is close to a verdict on whether the core idea has teeth, and
it costs about two hours.

The one thing that would waste time from here is adding architecture before
running it.
