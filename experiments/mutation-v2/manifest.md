# mutation-v2 — Family 3: mutation confined to its owner package

Restart of `mutation-v1` (closed inconclusive: contradictory selection rule,
and an authority surface that measured method style rather than ownership).
Same pinned fixture corpus, same graphs, same substrate.

> Is owner-controlled state mutated only through surfaces that possess
> authority to mutate it?

## The relation, frozen before any fixture is opened

```
state_mutation_confined_to_owner(T.F)

  subject     exported struct type T declared in package P, exported field F,
              with at least one statically resolvable mutation site in the
              repository (non-test .go); a field nothing writes is true and
              useless and is not a subject
  owner       the declaring package P
  authority   package P -- any function, method, or constructor in P

  mutation    a write to <e>.F where the static type of <e> is T or *T:
              assignment, op-assignment, ++/--; and taking &<e>.F

  DERIVED     every resolvable mutation site lies inside P
  REFUTED     at least one resolvable mutation site lies in a package Q != P
              -- a COUNTEREXAMPLE TO CONFINEMENT, not by itself an
              architectural defect: an exported structure may be
              intentionally caller-mutable; the relation being false says
              nothing about whether the design is wrong
  UNRESOLVED  mutation authority cannot be bounded statically: a field
              address taken outside P (write authority handed across the
              boundary and not followed), reflection, unsafe, an interface
              path the reader cannot resolve, or a package that could not
              be loaded
```

## Selection, global and independent, frozen before the tool ran

```
fixtures, fixed name order:
  1  github.com/golang/groupcache  @ 7477803   graph :10191
  2  github.com/golang/mod         @ 9c7e562   graph :10193
  3  github.com/golang/sync        @ 2efa68e   graph :10190

subjects: stable path order over non-test .go, declaration order within a
file, field order within a type -- one global sequence across the corpus

violated  = first natural REFUTED   with >= 1 mutation in the sequence
positive  = first natural DERIVED   with >= 1 mutation in the sequence
envelope  = first natural UNRESOLVED with a named reason in the sequence

each role searches the entire ordered corpus independently;
NO_SUBJECT only if that role is absent across the entire corpus;
no fixture is edited; nothing observed in mutation-v1 pre-selects anything
```

The sealed tool is `selection/mutscan/main.go` (sha256 in `selection.json`),
built with the go1.26.0 toolchain the corpus requires. It is sealed by the
commit that carries this manifest, **before** it is run; the scan is a later
commit. Every verdict is recorded; the sealed verdicts are pre-registration
of the mechanism's answer and are never shown to the investigator.

## Gate

Family 3 succeeds only if the Sensei recipe, designed after sealing,
reproduces the sealed verdicts on the sealed subjects, and the full chain
(question → recipe → derivation → coverage → routing → execution
consequence) is then observed on a governed task. A recipe rewritten until
the violated subject derives is a campaign failure, recorded as such.
