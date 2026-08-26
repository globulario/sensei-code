# Encounter 1 NOT started — admission refused

```
gate run   2026-08-26 15:44:17Z
request    4 governed runs (Encounter 1 and 2 × control and treatment), margin 1.30
verdict    REFUSED
reason     "no rate_limit_event was found in the probe output, so remaining
            campaign capacity could not be established"
```

Nothing was run. Nothing was touched. The frozen state stands.

## The refusal is instrument blindness, not a quota verdict

These are different findings and must not be conflated:

| | |
|---|---|
| capacity is **insufficient** | a fact about the provider |
| capacity **could not be established** | a fact about the gate |

This is the second. The gate failed **closed**, which is the correct direction —
it refused rather than assuming a full tank — but it did not measure anything.

## Instrument defect #14 — the capacity gate is provider-blind

The probe ran and **completed**:

```
kind: task.created · mode.selected · context.consulted
      agent.started · architect.spoke · agent.finished
      workflow.completed
source: architect, sensei, system
```

So the **architect** provider (ChatGPT/codex) demonstrably has capacity right
now. But `rate_limit_event` is emitted only by `"source":"claude"` — verified
against the single real observation in the record, the proof-v6 arm-1 transcript
— and a conversational probe never reaches an implementor. No Claude agent ran,
so no reading exists.

The gate therefore:

1. reads capacity for **one provider only**, and
2. only when **that** provider actually runs.

A governed workflow here uses ChatGPT as architect and Claude as implementor.
The gate can be blind to the architect entirely, and blind to the implementor
whenever the probe is cheap enough not to invoke one. A probe expensive enough to
read the implementor's budget is a probe that spends it.

## What is actually known

| | |
|---|---|
| architect provider (ChatGPT) | **has capacity** — probe completed 15:45Z |
| implementor provider (Claude) | **unknown** — last read 96% seven-day at 04:03Z, expected reset 21:00 local |
| can the four-run pair complete? | **unestablished** |

The experiment needs both: the architect plans, the implementor implements. One
provider's capacity does not admit the pair.

## Proposed repair, not applied

Capacity must be established **per role**, and a role whose budget cannot be read
must refuse rather than be assumed available:

- read a reading per configured role, not one global reading
- prefer each provider's own reported limits; where a provider reports none,
  record `UNREADABLE` for that role rather than treating silence as headroom
- admit only when **every** role required by the campaign has provable capacity

The narrower alternative — invoke a minimal implementor task to force a reading —
spends the budget being measured and still says nothing about the architect.

Not applied. The instruction was to run the gate and, if it refused, stop and
record. It refused. This is recorded.

## State

```
Encounter 1        NOT STARTED
fixture            frozen, unchanged, authoritative
experiment state   frozen.json, unchanged
substrate          sensei @ 80392aaf, unchanged
```
