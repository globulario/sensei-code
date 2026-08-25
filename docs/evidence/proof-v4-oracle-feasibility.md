# Which of the ten tasks can actually be given a behavioural oracle

Assessed before writing probes, because writing eight and discovering the
problem at the eighth would have cost days.

**Result: 4 of 10 admit a sound behavioural contract. The corpus, not the
method, is the limit.**

## The criterion

A task can be given a behavioural oracle when there is an observable contract
reachable through a surface that either

- already exists at the pinned base, or
- the task statement can NAME without also specifying the design.

The second half matters. Naming `Repo.CandidateDiff(ctx) (string, error)` is a
requirement and leaves the implementation free — the ALTERNATE specimen proved
it, arriving via `ls-files` where the reference used `--intent-to-add`. Naming
an entire package's types, constructor, error values *and* wire protocol is not
a requirement; it is the design, and an ALTERNATE could then differ only
cosmetically.

## The four that work

| task | contract | why it works |
|---|---|---|
| `internal-gitx-a4fa351` | `CandidateDiff` shows created files | **gated** — REFERENCE/WRONG/ALTERNATE all separated |
| `internal-gitx-6460efd` | a read-only measurement must not write `.git/index` | **gated** — same |
| `internal-mcpconfig-21c5a6b` | `Describe(repoRoot)` must report the configured allowlist, not assume one | hermetic: reads a repo root a probe can build |
| `internal-tui-ea046ba` / `be512db` | scroll keys must change what `View()` renders | observable through `Update`/`View` |

## The six that do not, and why

**`internal-doctor-853fbe3`** — the contract is *"a check that reports a missing
tool must name the fix"*. That is **text quality**. An oracle for it either
prescribes wording — shape-bound again, the exact defect proof-v4 removed — or
is too loose to separate WRONG from ALTERNATE. There is no third option.

**`internal-doctor-7a56cd2`** — an authenticated provider must not be reported
PASS. The behaviour depends on `provider.StatusFor`, called directly by
`doctor.Run` with **no seam**. A probe cannot construct "authenticated but
capability unknown" without the real provider, so it would test the machine it
runs on rather than the candidate.

**`internal-behavioral-a453be8`** and **`055fe6b76a`** — the package does not
exist at the base. Its contract is `Config`, `Outcome`, `Client`, `New`,
`ErrNotConfigured`, `Decision`, `CheckAction` **and** a JSON-RPC handshake. To
make it oracle-able the statement must specify all of it, at which point the
task is "implement this design" and an ALTERNATE differs only cosmetically. A
task with no room for a structurally different correct answer cannot exercise
the gate that makes this benchmark trustworthy.

**`internal-mcpconfig-110678d`** — the change lands in `package main`. Its only
observable surface is the built CLI, and the same commit also changes
`internal/mcpconfig`, so a probe cannot attribute what it observes.

## What this says about the selection rule

The v1 rule selected for *"changes a `_test.go` and a non-test `.go` in one
package"*. That is a proxy for "has a regression test", and it turns out to be a
poor proxy for **"has an observable contract"**. Six of ten passed the proxy and
fail the real requirement.

The rule needs a further criterion, applied by inspection before a task enters
the corpus:

> **(6) Oracle-ability.** The task must have an observable contract reachable
> through a surface present at the base, or nameable without specifying the
> design. Tasks whose contract is text quality, or which require a dependency
> seam that does not exist, or which are whole-package designs, are excluded.

43 commits passed criteria (1)–(4). Ten were drawn; six fail (6). Replacements
must be drawn from the remaining 33 under the same chronological rule, and each
must clear the three-specimen gate before entering the corpus.

## What this is not

Not a reason to weaken the gate. Every one of these six could be admitted by
writing a looser oracle, and every such oracle would return numbers that mean
less than they appear to. The corpus getting smaller before it gets larger is
the gate working.
