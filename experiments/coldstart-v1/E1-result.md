# Encounter 1, both arms — `NO_PROPOSAL` × 2

```
                control                      treatment
launched        01:24:34Z  base 60442f4      01:30:21Z  base eab5708
terminal        01:28:41Z  exit 3  247s      01:33:58Z  exit 3  217s
binding         HELD (0 divergence)          HELD (0 divergence)
link 1          gap identified               gap identified
link 2          proposed_recipe: null        proposed_recipe: null
receipt         NO_PROPOSAL · v3 · round 1   NO_PROPOSAL · v3 · round 1
closure claim   "existing accesses to both   "both protected by mu"
                 occur while Weighted.mu is
                 held"
```

The outcome is `NO_PROPOSAL`, twice, under the same prompt, from a
nondeterministic investigator that took two different routes to the closure
round (control: router-detected gap; treatment: architect asked to escalate,
router closed instead). **The result is stable.** It is not rewritten.

## What is established

**The socket works.** Gap → closure round → receipt, on a foreign repository,
on the right graph, with every product repair holding. Receipts recorded a
non-event correctly both times, which is what they exist for.

**The investigator finds the relationship.** In both arms, from its own inputs,
it wrote the `field_access_under_lock` fact as a repository-verified claim:

> *Weighted stores the held weight in `cur` and queued acquisition requests in
> `waiters`, with both protected by `mu`.*

**It does not ask it as a question.** Both times `proposed_recipe: null`.

## Where link 2 fails, precisely

The prompt offers a recipe *"IF YOU CANNOT CLOSE THIS GAP."* The investigator's
sense of *closed* is *I have verified my premises from the repository* — and by
that sense it closed the gap both times (control: `proceed`; treatment:
`escalate` on a genuine API-design question, not on ignorance).

The router's sense of *closed* is *the graph vouches for this region*. Its gap
is **coverage of an unindexed file**, and no repository claim can close that —
only a derived anchor can, and only a recipe yields one.

So the one artefact that could close the router's gap is presented to the
investigator as a fallback for a situation it never believes it is in. The
investigator did exactly what it was told. **The defect is in what it was
told.** It is the same shape as the three sensei-code refusals, now reproduced
on a cold repository with the reason isolated.

The treatment arm adds one more thing: its escalation, *"which externally
visible API should expose the semaphore state?"*, is a plausible value question
for a human — the investigator distinguished *I don't know the code* from *this
is a design choice*, which is the distinction the objective/technical/
consequence lanes want. It still wrote no question, because the prompt did not
connect "escalating" to "leave the checkable part behind".

## What this does NOT establish

- Nothing about compounding. Encounter 2 was not run: with no recipe in either
  arm, both would be cold by construction and would measure nothing.
- Nothing about whether a proposed recipe would derive, be relevant, or change
  routing — links 3–7 remain untested.
- Nothing about the vocabulary's expressiveness — the relationship is
  expressible; the failure is upstream of expression.

## The correction, not applied

Tell the investigator what kind of gap it is closing and what closes it:

> This gap is **graph coverage**. Verifying premises from the repository does
> not close it — the graph does not read your claims. It closes only when a
> derivation over this region succeeds later. If you found a relationship worth
> checking mechanically, propose it **whether or not you consider the gap
> closed**, and whether you proceed or escalate.

That is a prompt change: `gap-closure-prompt/v3 → v4`, a restart, and a fresh
Encounter 1 in both arms. It does not touch vocabulary, admission, the
future-only rule, or anything the investigator could gain authority from.

**Not applied.** A prompt correction after observing the outcome is exactly the
move that needs a decision, not a reflex — the line between *telling the
investigator what kind of gap it faces* and *telling it what to answer* is the
line this experiment exists to respect, and the wording above is on the right
side of it only if a reader agrees.

## Onboarding frictions found by this fixture, cumulative

1. domain closure required candidates to publish — **fixed upstream**
2. capacity gate was provider-blind — **fixed**
3. `.sensei-code/sessions/` self-dirties a foreign checkout — gitignore
4. `.sensei-code/candidates/` likewise — gitignore
5. consequence lane read `Release` as a deployment — **fixed**
6. architect bound to the wrong graph via global config — **fixed**
7. `task_briefing`: "no task session exists in this repository" — recorded
