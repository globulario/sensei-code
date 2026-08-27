# Re-selection walk under criterion (6)

Mechanical, in the already-defined chronological order. No candidate was chosen
for how well it suits Sensei-code, and every verdict is recorded.

## Corpus v1 — all ten preserved

| task | verdict | reason |
|---|---|---|
| `internal-tui-ea046ba` | **ADMITTED** | scroll behaviour observable through `Update`/`View`, exported at the base |
| `internal-tui-be512db` | **ADMITTED** | same surface as its seed |
| `internal-mcpconfig-110678d` | `ORACLE_INELIGIBLE` | lands in `package main`; the same commit also changes `internal/mcpconfig`, so a probe cannot attribute what it observes |
| `internal-mcpconfig-21c5a6b` | **ADMITTED** | `Describe(repoRoot)` is hermetic; it reads a repo root a probe can build |
| `internal-behavioral-a453be8` | `ORACLE_INELIGIBLE` | package absent at base; contract is types + constructor + error values + a JSON-RPC handshake. Specify it all and the task is "implement this design", leaving no room for a structurally distinct ALTERNATE |
| `internal-behavioral-055fe6b` | `ORACLE_INELIGIBLE` | same: a prescribed API and wire protocol |
| `internal-doctor-853fbe3` | `ORACLE_INELIGIBLE` | contract is subjective **text quality** — an oracle either prescribes wording or cannot separate WRONG from ALTERNATE |
| `internal-doctor-7a56cd2` | `ORACLE_INELIGIBLE` | depends on `provider.StatusFor` with no seam; a probe would test incidental machine state |
| `internal-gitx-a4fa351` | **ADMITTED** | gated: REFERENCE / WRONG / ALTERNATE all separated |
| `internal-gitx-6460efd` | **ADMITTED** | gated: same |

**5 admitted, 5 oracle-ineligible.** Five replacements needed.

`internal-mcpconfig-21c5a6b` is admitted rather than dropped. Its seed failed
(6), which under a strict reading of the package-pairing clause would exclude the
whole package — but the task itself satisfies all six criteria, and discarding a
measurable task on a pairing technicality would shrink the corpus without making
it more truthful. It enters unlinked.

## The walk — first candidates satisfying criteria 1–6

Walked in chronological order over the 33 remaining eligible commits.

| # | commit | package | verdict | reason |
|---|---|---|---|---|
| 1 | `5dfffad641` | `internal/assist` | **ADMIT** | `SenseiCaller` is an interface: a fake observes which domain the preflight was scoped to. Hermetic. |
| 2 | `6cf23e8490` | `internal/decision` | **ADMIT** | `Write` returns exported `ErrNotLinked` before any CLI call. Hermetic. |
| 3 | `da311a5627` | `internal/publish` | **ADMIT** | `CommitArgs`/`PushArgs` are pure; `ErrPushNotGranted`/`ErrCommitNotGranted` are returned from config alone, before any network. |
| 4 | `01f3fe1738` | `internal/agent` | **ADMIT** | `Activity(name, line) string` is an exported pure function. |
| 5 | `cde6bf9212` | `internal/workflow` | reject | `Engine.Note` is exported, but delivery is observable only by driving a real `Engine` with providers — incidental environment state. |
| 6 | `4d3293745d` | `internal/session` | **ADMIT** | `FindInterrupted(events) []Interrupted` is pure over an event slice. |

**Five admitted at position 6.** Corpus reaches ten tasks.

## The tension this exposes

Those five are in five different packages, so they add **no linked specimens**.
The corpus would be ten tasks with **two** linked pairs (tui, gitx) against a
required minimum of **four**.

Continuing the walk reaches `internal/setup`, which has three eligible commits
(`e6456697ce`, `10a4f32192`, `16ecbc3e50`) around the exported
`InspectQuick(ctx, Options) Report` — a natural linked pair or triple.

Taking them requires one declared clarification, because "the first five that
pass" and "at least four linked" cannot both be satisfied by five admissions:

> Admit in chronological order every candidate that passes all six criteria and
> the three-specimen gate, until the corpus contains **at least ten tasks AND at
> least four linked specimens**.

That is the existing constraint applied rather than a new preference, it stays
mechanical, and it is declared before any admission is finalised. It yields a
corpus slightly larger than ten, which is a minimum rather than a target.

Reordering the walk to reach `internal/setup` sooner would be curation, and is
not done.

## Still owed

Every admitted replacement needs a contract probe and REFERENCE / WRONG /
ALTERNATE specimens, and must clear the gate before entering the frozen corpus.
Two of the ten are gated today. No campaign arm runs until all are.
