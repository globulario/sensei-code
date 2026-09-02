# F: the observation surface, frozen

**Frozen by the owner on 2026-09-02, before publication and before any F result
existed.** Recorded here so the freeze is checkable against the commit date
rather than asserted afterwards.

## The decision

> **F's primary observation surface is `briefing` only.** Not `preflight`, and
> not "either one counts."

F tests **prospective transfer of experience to the worker** — not whether
Sensei can manufacture a governance consequence. `briefing` is the surface
whose job is to put relevant knowledge in front of the agent. `preflight`
answers a different question, about admissibility, risk and authority.

Allowing both would give Sensei two chances to succeed and would blur
**retrieval** with **governance**. Those are different claims and only the
first is what F exists to test.

## The frozen interpretation

```
prior governed law exists
  → published, and criterion 1 proves it reachable
  → untouched F subject
  → BRIEFING is queried
  → did the relevant prior law surface prospectively?
```

## Two failure conditions, fixed in advance

**1. Preflight cannot rescue a briefing miss.** If `briefing` does not surface
the law but `preflight` later notices something related, **F fails its primary
criterion.** That observation is diagnostic evidence afterwards; it cannot
retroactively convert the experiment into a success.

**2. Volume is not transfer.** If `briefing` surfaces a pile of vaguely related
material but not the relevant law, that is **not a pass.**

The second condition is the one most likely to be argued with after the fact,
so it is worth stating why it is not a technicality. Measured prospective
performance on this repository's own corpus is **recall 0.34 at 11.4 candidates
per subject**. At that density, a surface that returns many plausible anchors
will often contain *something* a motivated reader can call relevant. Counting
that as transfer would make F unfalsifiable, and an unfalsifiable experiment is
exactly what the rest of this program has spent its time eliminating.

## The criterion-1 witness

`invariant.a_verification_mechanism_must_prove_its_falsifying_path_exec`,
landed via #329.

It is an unusually clean reachability probe because its entire provenance is
known: authored on a branch, reviewed at its exact head, composition proven a
fast-forward, landed with the merged tree identical to the reviewed tree, and
verified present in `main`'s corpus afterwards. If publication works, this
exact id becomes queryable. If it does not appear, criterion 1 still fails and
the failure has a name.

## The fixed sequence

```
1. publish the exact reviewed/landed main state
2. query #329's exact invariant and prove criterion 1
3. establish that briefing is reading that published world
4. run F on the sealed subjects, BRIEFING ONLY
5. accept the result exactly as measured
```

## Contamination status

The sealed subjects remain uninspected. Verified on 2026-09-01, after a sweep
that touched 114 skip sites across the repository: **zero commits touched
`experiments/`** — not on `main`, not on any of the branches that landed during
the campaign.
