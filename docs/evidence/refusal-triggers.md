# What actually triggered the six refusals

All six were logged as *"Architectural authority reached a human-owned
boundary."* That is a route. Reading the payloads, there are **three
mechanisms**, and they behave very differently.

| task | trigger | RAW |
|---|---|---|
| `architect-2e095c4` | graph indexes this area but **no anchored rules apply** | INCORRECT |
| `setup-16ecbc3` | graph indexes this area but **no anchored rules apply** | INCORRECT |
| `setup-e645669` | coverage absent — only 1 of 2 planned files examined | CORRECT |
| `tui-be512db` | `human_approval_required` (blast radius security) | INCORRECT |
| `tui-ea046ba` | `human_approval_required` (blast radius security) | CORRECT |
| `session-4d32937` | `review_required` (blast radius service) | CORRECT |

## "I lack coverage" is disproportionate

`setup-e645669` planned two files:

```
internal/setup/setup.go   → 1 row   (a decision about TUI readiness reporting)
internal/setup/checks.go  → 0 rows  — entirely unknown to the graph
```

`checks.go` is the file the task is *about*. Sensei escalated to a human because
it had never indexed the central file.

That trigger is not narrow. **25% of non-test `.go` files in this repo (24 of
97) are named nowhere in the awareness sources**, so an ordinary two-file task
has roughly a 44% chance of tripping it. It fires on incompleteness of the
index, not on risk in the change.

Worse, the gap is defined so investigation cannot close it. The reason reads *"a
bounded knowledge gap was **not closed by investigation**"* — but the gap is
"the graph has no triples about this file", and reading a file does not add
triples. The only action that closes it is unavailable mid-task. That is the
path Repair 1 wires: derive coverage at query time so a derivable fact can close
the gap instead of a human.

## "No anchored rules apply" is worse

Two refusals fired because the graph **does** know the area and simply has no
rule about the change. That is the *absence* of governance reported as a
finding.

When no rule applies, an unruled change is an ordinary change. Escalating it
inverts a principle this project already holds: the human owns **value and
consequence**; the AI resolves **technical uncertainty** by investigation. "I
have no applicable rule" is not a value question — there is nothing for a human
to decide, and the only available answer is "go ahead".

## The security trigger discriminates path, not correctness

`tui-be512db` and `tui-ea046ba` fired on the *same* rule with *opposite* RAW
outcomes. Both touch `internal/tui/model.go`, which the credentials invariant
claimed. Repair 2 removed that file from the invariant, because the TUI does not
own credentials — the provider does.

So that trigger's 50% precision was **structural, not calibration**. It selects
on file path. Any two tasks touching `model.go` would produce the same split.

## A hypothesis, explicitly not a result

"No anchored rules apply" fired on two tasks and RAW got **both** wrong. Of the
three mechanisms it is the only one that tracked correctness.

**This is post-hoc slicing at n=2 and must not be reported as a finding.**
Discovering a sub-rule with 2/2 precision after seeing the outcomes is the
overfitting this campaign exists to resist. It is recorded here as a
**pre-registered hypothesis for the v2 run**:

> If "no anchored rules apply" is a genuine risk signal rather than noise, it
> should again land on tasks RAW gets wrong at a rate above the 27% base rate.
> If instead it fires broadly across the corpus, it is what it looks like on its
> face — the absence of a rule, escalated.

Repair 1 changes this path, so v2 tests both at once: whether the gap now closes
autonomously, and whether the cases it was closing were worth stopping for.

## What this does to the calibration claim

Recall stays 3/3 — every task RAW got wrong was refused. But the mechanism table
shows *why*, and it is not a system judging risk:

- 2 captures came from having **no rule**
- 1 capture came from a **file-path anchor** that also blocked a correct task
- the remaining 3 refusals asserted **nothing about the candidate at all**

Not one refusal claimed the change was unsafe, violated an invariant, or
bypassed an owner. The "well-calibrated refusal" reading is not supported by the
mechanisms, whatever the capture count suggests.

## Not changed

No production behaviour, routing rule, invariant or threshold was touched. The
standing instruction is to change nothing before the v2 run, and the two
candidate changes this analysis suggests — not escalating "no anchored rules
apply", and closing coverage gaps by derivation — are respectively **not made**
and **already made as Repair 1**, whose effect v2 is designed to measure.
