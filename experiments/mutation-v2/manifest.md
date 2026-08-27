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

## The scan (one run, tool built with go1.26.0, sealed at `5f72ab8`)

| fixture | subjects | DERIVED | REFUTED | UNRESOLVED |
|---|---|---|---|---|
| groupcache @ 7477803 | 55 | 55 | 0 | 0 |
| mod @ 9c7e562 | 111 | 107 | 4 | 0 |
| sync @ 2efa68e | 3 | 3 | 0 | 0 |

Global independent selection over the ordered corpus:

```
violated   mod         module/module.go   Version.Path
           counterexample to confinement: its 1 mutation site is
           modfile/rule.go:216 (package modfile), outside owner package module
positive   groupcache  http.go            HTTPPoolOptions.BasePath
           all 1 mutation site(s) inside the owner package (http.go:105)
envelope   NO_SUBJECT
```

Read plainly:

- The v1 "violation" (`HTTPPoolOptions.BasePath`) is v2's first positive
  subject: the constructor default is the owner package exercising its own
  authority. That is the counterexample v1 closed on, now on the right side.
- `module.Version.Path` written from `modfile` is a counterexample to
  confinement and is not called a defect here: `module.Version` is a plain
  exported value type that `modfile` fills in while parsing a `go.mod`; it
  may well be intentionally caller-mutable. The relation is false for it;
  whether the design is wrong is a separate, human question.
- Envelope `NO_SUBJECT`: no field address is taken outside its owner package
  anywhere in the corpus, and all packages load. **Tool limit, stated:** the
  hand-derivation detects only address-escape and load failure as
  UNRESOLVED; it has no detector for reflection, `unsafe`, or writes reached
  through an interface. Their absence from the scan is the absence of a
  detector, not evidence of their absence in the corpus. The envelope side
  of the triangle therefore has no natural specimen in this corpus under
  this tool, and none is fabricated; the recipe must still declare those
  limits in its own `Limits()`.
- Every verdict, including the 165 passed over, is in `selection.json`.

The recipe is designed after this point.
