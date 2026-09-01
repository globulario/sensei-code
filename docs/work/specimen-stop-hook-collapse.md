# Specimen: a binary that swallowed a third state, observed under real pressure

Recorded because it demonstrated the law **under load**, on a live run, rather
than in a synthetic test — which makes it better evidence than anything written
to prove the point.

## What happened

A session-scoped stop hook blocked exit **nine consecutive times** on a goal
condition that no authorized action could change. Codex was out of quota, so the
review verdicts that would have permitted merging could not arrive.

```
observed state       goal condition = false
missing state        authorized transition exists? = NEVER ASKED
collapsed            false goal  =>  continue
result               manufactured work pressure
```

## Why it belongs in this evidence set

It is the same failure the whole program keeps repairing, one level up from the
code:

```
unmeasured  ≠ zero
unpublished ≠ absent
unresolved  ≠ invalid
unrefuted   ≠ supported
withdrawn   ≠ unknown
goal unmet  ≠ work remains
```

Every one is a binary representation swallowing a third state, and every one
resolves the ambiguity in the direction that produces action rather than the
direction that is true.

## The repair shape

```
if goal_met:                       allow_stop
else if authorized_action_remains: continue
else:                              allow_stop_with_unmet_terminal_state
```

A state predicate and a transition predicate are different questions. The third
branch is the missing member: **the goal is false and nothing further is
authorized** is a legitimate terminal state, not a contradiction to be resolved
by continuing.

Mechanically, honouring `stop_hook_active` after the first block breaks the
recursion. That is the smaller fix; the predicate split is the actual one.

## What made it evidence rather than an annoyance

The hook created sustained pressure toward four specific actions, and each would
have satisfied it:

```
merge own repaired PRs on self-assessment      forbidden by Priority 10, by name
run reflex-v1 against a 159-change-stale graph  a green result from a false world
read "all implementable work done" as complete  workflow completion as evidence
keep busy-looping                               work as proof of progress
```

The constitutional constraints held against nine rounds of a mechanism whose
only output was pressure. **That is a behavioural witness, not a proof.** It
shows the constraints survive contact with inconvenience; it does not show any
reasoning improved. Recorded at that strength deliberately.

## What this specimen does not license

It does not license ignoring stop conditions. The distinction is narrow and
worth keeping narrow: a condition that is false *and* has an available
authorized action is a reason to continue. This specimen is only about the case
where no such action exists, and where the only remaining moves are ones the
governing document forbids.
