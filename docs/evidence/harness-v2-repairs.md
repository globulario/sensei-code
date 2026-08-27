# Harness v2 — the four repairs, and one disclosure that costs us

Instrument-only. No production behaviour, corpus, oracle, threshold, task
statement, or kill-criterion changed. The frozen manifest hash is byte-identical
across the void and the re-run: `sha256:72384ad9…`.

## 1. A specific structured terminal is authoritative

The engine names its own outcome through a documented exit contract — 0
complete, 1 failed, 3 awaiting authority, 4 stopped, 5 timed out, 6 observed.
Every code but 1 names something *specific*, and no phrase found in a
transcript may now overrule one.

```
exit 0,3,4,5,6  → structured_specific → the engine decides. Text cannot override.
exit 1, unknown → structured_generic  → the engine said only "failed";
                                        text may fill in the cause.
```

Text classification did not go away — it was put where it belongs. A genuine
outage that exits 1 with `connection refused` still scores INFRA_FAILURE.

A recognised phrase that loses is **recorded rather than discarded**
(`infrastructure_hint`). A run that timed out with `usage limit` in its
transcript still deserves a human's attention; suppressing the signal would
trade one blind spot for another.

## 2. The evidence a classification rests on is preserved

Harness v1 classified on the full stream and kept the last 20KB. The phrase that
condemned arm 1 was not in what survived, so the verdict could be neither
confirmed nor refuted — and an unfalsifiable verdict is not a measurement.

Now the complete transcript is written and hash-recorded (`raw_sha256`,
`raw_bytes`), and every classification carries the span that produced it:

```json
"classifier_evidence": {
  "signal": "backend is unreachable",
  "byte_offset": 418223,
  "context": "…400 bytes either side…",
  "decided": false,
  "overruled_by": "workflow.timed_out"
}
```

A reader can now see whether the phrase was a *report* of failure or the model
*discussing* one.

## 3. Quota admission asks whether the campaign can finish

The old gate asked "can one arm start?" The five-hour window had just reset, so
it said yes. The seven-day window — the one eleven arms would actually
exhaust — stood at 96%.

`AdmitCampaign` now projects every remaining arm against the **tightest**
window with a 30% margin, and re-gates before each arm, because quota moves
during a wave. Run through the real arm-1 transcript, the new gate refuses the
exact launch that happened:

```
11 arm(s) need about 28.6% of the seven_day window
(2.00% per arm ×1.30 margin) but only 4.0% remains
```

`per-arm` has **no default**. An invented constant is exactly the unfounded
number this campaign exists to avoid, so a missing estimate is a refusal, and
every arm now records `quota_before`/`quota_after` so the projection becomes
measured rather than assumed. The worst observed arm is used, not the mean — a
few cheap refusals must not hide the cost of arms that do real work.

## 4. The instrument is versioned and frozen per campaign

`harness_version` and `classifier_version` are stamped on every attempt, and
`harness.lock.json` pins the campaign. Repairing the harness now **costs a full
re-run by construction** — which is the correct price, and the reason arms 2–11
were not simply continued.

`CheckInstrumentUniformity` is the retrospective half: a report refuses to
average attempts taken under different instruments.

---

## The disclosure — this repair weakens a claim I made

**Defect #13 was already in proof-v6, and I did not notice.**

`internal-session-4d32937` ended `workflow.awaiting_authority` — exit 3, the
engine naming a **refusal** — and was scored INFRA_FAILURE because the word
`unauthorized` appears in the transcript of a task about **session handling**,
where it is ordinary vocabulary.

**The frozen FINAL_VERDICT does not move, and is not being rewritten.** Attempts
recorded before v2 carry no `terminal_source` and keep their original
precedence; a test pins all of it. The two-axis headline is untouched either
way, because REFUSED and INFRA_FAILURE are both NOT_EVALUATED for correctness
and both end-to-end failures:

```
engineering 4/4 · end-to-end 4/11 · CORRECT 4      ← unchanged
terminals:  REFUSED 5 → 6,  INFRA_FAILURE 1 → 0    ← the correction
```

But it **does** change the refusal-calibration argument I put to you, and in the
direction that hurts it:

```
                        precision   recall   p(random capture)
as frozen (5 refusals)    3/5 60%    3/3     0.061
corrected (6 refusals)    3/6 50%    3/3     0.121
```

Recall is still perfect — every task RAW got wrong was refused. Precision drops
to 50% against a 27% base rate, and the significance I quoted at p ≈ 0.06 is
really **p ≈ 0.12**. Directionally supportive, clearly not significant, and
weaker than I told you.

I am recording this because the repair produced it. An instrument fix whose only
disclosed effects flatter the hypothesis would be the same failure as the defect.

## Status

```
campaign            repair-verification-v1   VOID (harness v1)
arm 1               VOID_MEASUREMENT
campaign            repair-verification-v2   armed, identical corpus, harness v2
INCORRECT→CORRECT   unmeasured
kill test           unchanged
```

Blocked on the seven-day window, which resets **21:00 today**. The launcher
gates on capacity before the first arm and before every subsequent one, so a
wave that cannot finish will not start.
