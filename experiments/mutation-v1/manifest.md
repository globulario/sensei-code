# mutation-v1 — Family 3: mutation authority, a composition family

> Is owner-controlled state mutated only through surfaces that possess
> authority to mutate it?

Locks (Family 1) were a state/control-flow relation over one file; command
confinement (Family 2) an ownership/boundary relation over the repository.
Family 3 composes: **state identity + ownership + mutation sites + reach +
the authorized boundary**. It cannot be answered by one AST pattern; it must
be answered across relationships.

## The relation, frozen before any fixture is opened

```
state_mutated_only_by_owner(T.F)

  subject   an exported struct type T declared in package P, with an
            exported field F (Go's own rule already confines unexported
            fields to P; the question is what the language does NOT decide)
  owner     package P
  authority the methods declared on T (or *T) in P

  mutation  any write to <e>.F where the static type of <e> is T or *T:
            assignment, op-assignment, ++/--, or taking &<e>.F
            (an address that escapes is a write authority handed out)

  DERIVED    every mutation site in the repository (non-test .go) lies inside
             a method of T -- the owner's authority surface
  REFUTED    at least one mutation site lies outside P, or inside P but
             outside T's methods, with the write statically resolvable
  UNRESOLVED a write reaches F through something the reader cannot
             resolve statically: reflection, unsafe, an interface the field
             is reached through, a field address that escapes into a
             non-owner package, or a package the reader could not load
```

Truthfulness is not relevance: a DERIVED verdict over a field nothing
mutates is true and useless. A subject counts only with **at least one
mutation site**.

## Selection, mechanical, sealed before the recipe exists

Fixtures already onboarded (declared identity, isolated graph, pinned), in
**name order**:

```
1  github.com/golang/groupcache  @ 7477803   graph :10191
2  github.com/golang/mod         @ 9c7e562   graph :10193
3  github.com/golang/sync        @ 2efa68e   graph :10190
```

Rule, applied by the sealed hand-derivation tool (`selection/mutscan`,
sha256 recorded in `selection.json`), stable path order over non-test `.go`,
declaration order within a file, field order within a type:

- the **violated subject** is the first `(T.F)` whose verdict is `REFUTED`
  naturally — no fixture is edited to produce one;
- the **positive subject** is the first `(T.F)` whose verdict is `DERIVED`
  with ≥ 1 mutation site;
- the **envelope subject** is the first `(T.F)` whose verdict is `UNRESOLVED`
  and whose detail names the reason;
- all three are sought in the first fixture that yields a violated subject;
  positive and envelope may come from the same fixture; if any of the three
  is absent across all fixtures, that role is `NO_SUBJECT` and nothing is
  fabricated.

Every passed-over `(T.F)` verdict is recorded in `selection.json`. The
sealed verdicts are pre-registration of the mechanism's answer and are
**never shown to the investigator**.

## Gate

Family 3 succeeds only if the recipe, written after sealing, reproduces the
sealed verdicts on the sealed subjects: `DERIVED` on the positive, `REFUTED`
on the violated, `UNRESOLVED` on the envelope — and then the full chain
(question → recipe → derivation → coverage → routing → execution
consequence) is observed on a governed task. A recipe rewritten until the
violated subject derives is a campaign failure, recorded as such.

## Disclosure: the freeze commit landed after the first scan

The manifest and the tool were written, the tool was built and its sha256
computed (`d136e99b643073e82b5db33c2f417b5dfc9620e3895292b8529361195a05c183`)
before any fixture was scanned, but the commit that was meant to seal them
failed on a relative path and the scan ran before the failure was noticed.
The tool's bytes are unchanged (the sha256 in `selection.json` is the one
computed before the scan and matches the committed file); nothing in the
manifest or the tool was edited after seeing a result. Recorded rather than
re-ordered, per the standing rule that a completed step is amended with a
disclosure, never rewritten.
