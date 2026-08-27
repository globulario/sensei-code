# confinement-v2 — links 6–7 for the second family, with a single-file plan

Same fixture (`golang/mod @ d0a27b2`), same graph (:10193), same substrate
(`sensei@e81d7bed`), same sealed specimen persisted alone — the E2b base of
`confinement-v1` (`9c7e562`): `command_invocation_confined_to("go" confined to
gosumcheck) searched under .`, which derives `DERIVED` in a governed run.

Only the task changes. confinement-v1's E2 plan spanned `gosumcheck/main.go`
**and** `gosumcheck/test.bash`; one anchor over two files is not coverage, so
the route stayed cold for a fair reason. This task is written from one function
in `main.go` and gives the plan no reason to reach the harness.

## E3, verbatim, naming no executable

> *gosumcheck's verbose output reports, for each remote fetch, the elapsed time
> and the target. Include the number of bytes received on that same line, with
> no change to non-verbose output or to any error path.*

## Predictions

- plan stays inside `gosumcheck/main.go` → `1 anchor over 1 planned file` →
  route `architectural-authority-granted` → implementor → audit → review →
  candidate. Links 6–7 for the family.
- plan reaches `test.bash` again → 1 anchor over 2 files → cold. Then the
  finding is about the architect's planning for this command, not about the
  family, and it is recorded as such — not reworded, not re-run.
- `0 anchors` → the derivation regressed since E2b; instrument, halt.

## Natural reproducer for #89 — stopping rule, frozen before attempt 2

The reproducer reruns the **identical** E3 task on the repaired product from the
same base (`9c7e562`, sealed specimen alone). The architect is nondeterministic:
attempt 1 planned two files where the original planned one, so coverage was
insufficient for the plan, the route stayed cold and the implementor never ran.
That is the authority boundary refusing to extrapolate one-file evidence over a
two-file plan — a correct refusal, not #91 failing, and it is preserved.

Rule:

- Rerun the identical task unchanged for **at most 3 attempts**. Preserve every
  plan and route (`E3-repaired.attemptN.log`).
- Do not alter prompt, graph, recipes, coverage, fixture or task between
  attempts.
- If any naturally produced plan reaches the implementor, that attempt
  exercises #91's candidate boundary and is the reproducer.
- If none does, the natural-reproducer criterion is **unmet**, recorded as
  architect nondeterminism preventing the original failure path from being
  reached, and #89 stays open.

Two failures kept distinct on purpose:

- cold because knowledge is insufficient for the plan → correct refusal;
- candidate structurally unauditable after authority is granted → #89/#91.

## Natural reproducer, Series B — post-#312 instrument, frozen before B1

Instrument changed, so this is a new series, not attempt 4. Instrument:
sensei-code `main @ 1a385be` (#92 supplied-plan lane, #93 prospective
authority merged) with #91's candidate boundary rebased on top
(`fix/89-rebased @ 95369a7`, conflict-free apart from one struct field
merge), built with `-buildvcs=false`. Same fixture `golang/mod @ 9c7e562`
(sealed specimen alone), same graph `:10193`, same `SENSEI_BIN`
(`sensei-main`), same env (`SENSEI_CODE_BENCHMARK=1`). Task byte-for-byte the
E3 text. Before B1 the fixture's untracked `derived_receipts.jsonl` (a derive
output left by the earlier attempts) is moved aside, so the start gate sees a
clean tree; nothing committed to the fixture changes.

Rule: identical to the frozen attempt rule above — at most 3 invocations,
nothing altered between them, every plan and route preserved as
`E3-seriesB.N.log`. Two new things are observed, in this order:

1. **Does the architect, told nothing about #312, declare
   `prospective_surfaces` for the `main_test.go` it keeps wanting?** The JSON
   contract now carries the field with a one-line instruction and nothing
   else. If it does and the grant is admissible, coverage should read
   `2 anchor(s) over 2 planned file(s)` with one PROSPECTIVE anchor and the
   route should leave the gap. If it plans the file without declaring it,
   the route stays cold exactly as attempts 1–3 — recorded as the natural
   path not taken, not reworded.
2. **If authority is granted, does #91's boundary hold?** Expected: the
   implementor builds the command, the binary appears, the boundary excludes
   it (`candidate.artifact_excluded`) or refuses structurally
   (`candidate.not_auditable`), the two-line diff is reviewed, and no
   implementor is retried on an unauditable candidate. If the post-creation
   inspection of `main_test.go` refutes, that is terminal and recorded.

Outcomes and what they mean: route cold (no declaration) → #89 still unmet,
#312 natural path untested; route granted + boundary holds + review reached →
#89's natural witness met; route granted + old monster (oversized candidate
retried) → #91 does not hold, recorded; exit 3 → the question preserved.

### B1 — 21:54:57Z–21:58:30Z, exit 1

- The architect, told nothing, planned `main.go` + `main_test.go` again **and
  declared** `prospective_surfaces: [{path: gosumcheck/main_test.go, package:
  main, role: go-regression-test, dependencies: [fmt, io, net/http, os,
  strings, testing]}]`. Grant admissible against `main.go` at `9c7e562`:
  `prospective.granted`, `derived coverage: 2 anchor(s) over 2 planned
  file(s); route architectural-authority-granted`. Attempts 1–3 read
  `1 anchor over 2 files → bounded-knowledge-gap` on the same bytes. The gap
  #312 named is closed on its natural witness; the implementor ran on this
  task for the first time.
- Claude produced a 3,985-byte candidate: `main.go` +4/−2 and a 108-line
  `main_test.go`. It verified with `go vet . && go test .` — **no `go build`,
  no binary in the tree**, so #91's boundary was not reached.
- Post-creation inspection: **`prospective surface refuted: main_test.go
  imports "bytes"`** — the file also imports `net/http/httptest` and
  `regexp`; none are in `main.go`'s imports or the `testing` allowance. The
  authorized shape was not the produced shape. Terminal: `workflow.failed`,
  one implementor only, no review asked. The cycle-3 rule held in the wild.
- Validation noted `gofmt -l cmd internal` fails identically against the base
  (fixture has no `cmd`/`internal`): infrastructure, not the candidate.
- **Product finding (not altered for B2):** `implementationPrompt` never
  shows the worker the declared prospective shape. The grant bounds the
  worker; the worker cannot see the bound, so a competent test that reaches
  for `httptest` is refuted for exceeding a declaration it was never given.
  Filed as a follow-up, not fixed mid-series.
- `E3-seriesB.1.log` keeps 19 non-output events (77 dropped);
  `E3-seriesB.1.candidate.patch` is the refuted candidate.

Under the rule, B2 runs unchanged.

### B2 — 22:00:12Z–22:05:48Z, exit 1

- The architect declared the same surface again (2/2 naturally), with the
  same dependency list. Route first read `bounded-knowledge-gap` on full
  coverage (`2 anchor(s) over 2 planned file(s)`) because the plan carried
  one `inference` claim: *a package test can isolate ReadRemote … using only
  imports already present in main.go plus testing* — the envelope the
  contract line states, turned into a premise the architect admitted it had
  not verified. No architect thread exists in the fixture; B1 was not
  remembered. The closure round ran one read-only investigation
  (22:02–22:04), the re-plan carried no inference claim, the grant was
  re-issued, and the route became `architectural-authority-granted`. The
  architect-lane closure the supplied-plan lane deliberately lacks, seen
  working on the natural task.
- Claude produced a 4,190-byte candidate: `main.go` +4/−2, `main_test.go`
  112 lines. Verified with `go vet ./ && go test ./` — again **no build, no
  binary, #91 not reached**.
- **`prospective surface refuted: main_test.go imports "bytes"`** — the
  file imports `bytes`, `errors`, `regexp` beyond the envelope;
  `var buf bytes.Buffer` captures stderr, the canonical idiom. Terminal, one
  implementor, no review. Two independent worker sessions, identical
  refutation: the finding is about the instrument, not the worker. What is
  established is an information asymmetry — the architect knows the bound,
  the grant is issued against it, the worker is shown only the plan and
  chooses its own imports, and the fence catches the excess. NOT established:
  that no output-capturing test can exist inside the envelope; the workers
  have simply not produced one. Diagnosis (worker lacks the grant / the
  declaration is too narrow / the role's dependency policy is
  underspecified) waits for B3. Recorded, not altered.
- `E3-seriesB.2.log` keeps 25 non-output events (150 dropped);
  `E3-seriesB.2.candidate.patch` is the refuted candidate.

Under the rule, B3 runs unchanged; it is the last.

### B3 — 22:06:30Z–22:09:35Z, exit 1

- Declared naturally, 3/3, with the tightest list yet (`io, net/http, os,
  strings, testing`); plan step 2 says *intercepted HTTP transport and
  captured stderr*. Granted on the first route, no closure round.
- Claude: 3,548-byte candidate, `main.go` +4/−2, `main_test.go` 102 lines,
  verified with `go vet . && go test .` — no build, no binary, #91 not
  reached.
- **`prospective surface refuted: main_test.go imports "bytes"`** — plus
  `errors`, `regexp`; `var buf bytes.Buffer` at line 59 captures stderr.
  Terminal, one implementor, no review.
- `E3-seriesB.3.log` keeps 19 non-output events (76 dropped);
  `E3-seriesB.3.candidate.patch` is the refuted candidate.

## Series B result

Three invocations, nothing altered between them, rule frozen before B1.

| | attempts 1–3 (pre-#312) | B1 | B2 | B3 |
|---|---|---|---|---|
| architect declares `prospective_surfaces` | n/a (field absent) | yes | yes | yes |
| derived coverage | 1/2 → gap | 2/2 | 2/2 (after one closure round) | 2/2 |
| route | cold, exit 3 | granted | granted | granted |
| implementor ran | never | yes | yes | yes |
| built a binary / #91 boundary reached | — | no | no | no |
| post-creation inspection | — | REFUTED `bytes` | REFUTED `bytes` | REFUTED `bytes` |
| retried / reviewed after refutation | — | no | no | no |

**Established.** The natural #312 mechanism, end to end and three times: the
architect, told only the contract line, declares the surface it needs; the
predicate grants CREATE authority against the covering file's facts at the
pinned world; ordinary routing accepts it; the implementor runs; the created
file is inspected against the original grant; a mismatch is terminal, with
no reinterpretation and no second implementor. Every one of those is the
repaired instrument behaving as designed on a foreign repository.

**Observed, systematically.** Three independent natural plans, three grants,
three independent worker sessions, three identical refutations on `bytes`
(with `regexp` and `errors`/`httptest` alongside): the dependency
envelope the architect authorizes is not the dependency set the worker
selects, because the worker is handed the plan and not the grant. The
authorization currently acts as a post-hoc fence — and the fence held all
three times — rather than also as an implementation constraint. This is a
missing relationship between architectural authorization and implementation
context, not worker noise.

**Not established.** That no output-capturing test can exist inside
`S imports + testing` (nobody has tried to write one); which of the three
diagnoses is right — the worker lacks the grant, the declaration is too
narrow, or the role's dependency policy is underspecified — is the next
decision, taken after the series, not inside it.

**#89.** The natural reproducer is still **unmet, not failed**, for a new
reason: the implementor now runs, but all three workers verified with
`go vet`/`go test` and never built the binary, so the candidate boundary
#91 repairs was never reached. #91's synthetic regression remains the only
witness. The original monster required a worker that runs `go build` in the
tree; three workers did not.

**#312 is the natural path's success and its next question at once.** The
mechanism works; the grant and the worker do not yet share a vocabulary.

## Natural reproducer, Series C — post-M2.1 instrument, frozen before C1

Instrument changed (#96 merged: the implementor is shown the prospective
CREATE grant it operates under, on cycle 1 and on revision cycles), so this
is a new series. Instrument: sensei-code `main @ 842b533` with #94's three
candidate-boundary commits cherry-picked on top (`series-c-instrument @
b0d4491`, one trivial call-site merge), built to a path outside any repository.
Same fixture `golang/mod @ 9c7e562`, same graph `:10193`, same
`SENSEI_BIN`, same env, task byte-for-byte the E3 text, derive receipts
moved aside before each invocation as in Series B.

Rule: unchanged — at most 3 invocations, nothing altered between them, every
plan, grant, candidate and verdict preserved as `E3-seriesC.N.*`.

The question is one line:

> **Does showing the worker the already-authorized envelope change 3/3
> `bytes` refutations into an admissible candidate?**

Outcomes and what they mean:

- admissible candidate → review reached: Series B's diagnosis (grant
  visibility) was the causal defect; the role policy is not yet implicated.
  Whether the candidate then builds a binary decides whether #94's boundary
  is naturally exercised.
- worker reports the envelope as unsatisfiable and leaves the file uncreated
  (the M2.1 instruction) → the refusal is honest and becomes architect
  evidence; the declaration/role policy is implicated.
- refuted again on an import outside the shown envelope → the worker
  disregarded a bound it was shown; recorded as such, not reworded.
- route cold / exit 3 / timeout → recorded as in Series B.

Prediction: the shown envelope (`flag, fmt, golang.org/x/mod/sumdb, io, log,
net/http, os, os/exec, strings, sync, testing, time`) is satisfiable for
this task — stderr can be captured with `os.Pipe` + `io.ReadAll` and
matched with `strings` — so at least one of three workers should produce
an admissible candidate.

### Finding recorded mid-C1, before its outcome: closure-budget laundering through paraphrase

Observed during C1's architecture resolution (22:23–22:31Z and continuing):
three successive `bounded-knowledge-gap` routes, each on an `inference`
claim about the same evolving uncertainty — *how the regression test will
drive `ReadRemote` deterministically* — worded differently each round
(`clientOps.ReadRemote … len(data)` → `regression strategy: replacing the
default transport …` → `replacing http.DefaultClient.Transport …`), the
third arriving as an architect `escalate` the engine correctly declined to
turn into a Level-3 event.

`spendClosure` keys its budget on `taskID + condition`, and the condition
is the premise's prose. So the intended law — *one closure attempt per
unresolved architectural premise* — is enforced as *one per exact spelling*.
A nondeterministic architect re-stating the premise funds a fresh
investigation each time. What actually bounds the loop is `maxRounds = 8`
in `resolveArchitectureIn`.

```text
per-condition closure budget:            porous (identity = wording)
global architecture-resolution budget:   effective (8 rounds)
```

The mechanism's identity model is weaker than its stated law: condition
identity ≠ condition wording. A durable fix needs a stable identity for the
uncertainty (subject + question kind + scope + pinned world) rather than the
architect's latest prose. Not touched during Series C: no normalisation, no
budget change, no steering. The finding stands whichever way C1 ends; the
three endings read differently — settle-and-route (C1 still measures M2.1),
human branch (uncertainty survived paraphrase-funded rounds), or round-8
termination (the local budget did no containment at all).

### C1 — 22:23:35Z–22:36:33Z, exit 0, ACCEPTED

- Architecture resolution: four architect turns. Rounds 1–3 each routed
  `bounded-knowledge-gap` on a differently worded inference about the same
  uncertainty (the closure-budget finding above); round 3 was an architect
  `escalate` the engine closed instead of raising. Round 4 carried no
  inference claim; the surface was declared (dependencies `fmt, io,
  net/http, os, strings, testing`), granted, and routed
  `architectural-authority-granted`.
- Claude, **shown the grant for the first time**, produced a 4,911-byte
  candidate in 33 s: `main.go` +5/−2 and a 143-line `main_test.go`
  importing exactly `fmt, io, net/http, os, strings, testing` — every one
  inside the shown envelope — capturing stderr with `os.Pipe()` +
  `io.ReadAll`, the idiom the envelope admits, where B1–B3 reached for
  `bytes.Buffer` blind. Verified with `go vet && go test`; no build, no
  binary.
- Post-creation inspection: **passed** (first time on this task). Broker
  validation: vet/build/test pass, gofmt infrastructure-failure identical
  against base. Sensei diff audit: `pass`, 2 files, 0 findings.
- Independent review (Codex, fresh session, bound to `f79a0419ce74`):
  **ACCEPT** — "only the success-only verbose line changes … transport-
  intercepted tests cover verbose/non-verbose success plus transport,
  HTTP-status, and body-read error silence … brokered go vet, build, and
  full tests passed."
- `decision.recorded`: *not recorded: no governing invariant to link the
  decision to* — correct; `gosumcheck/main.go` has no direct invariant.
  Candidate retained, unpublished, not admitted: *landing it is the human's
  decision.*
- `E3-seriesC.1.log` keeps 49 non-output events (2847 dropped);
  `E3-seriesC.1.candidate.patch` is the accepted candidate.

## Series C result

One invocation answered the frozen question.

| | B1–B3 (worker blind) | C1 (worker shown the grant) |
|---|---|---|
| declared naturally | 3/3 | 1/1 |
| route | granted | granted (after 3 paraphrase-funded closure rounds) |
| test imports | `bytes` + others outside the envelope, 3/3 | exactly inside the envelope |
| capture idiom | `bytes.Buffer` | `os.Pipe` + `io.ReadAll` |
| post-creation inspection | REFUTED 3/3 | passed |
| audit / validation / review | never reached | pass / pass / **ACCEPT** |
| built a binary | no | no |

**Established.** Grant visibility was the causal defect. Same role, same
allowance, same task, same fixture, same base, same graph; the only change
between Series B and Series C is that the worker is shown the authority it
already operates under — and the worker went from 3/3 refutations to an
accepted candidate on the first try. The `go-regression-test` policy is
not implicated by any evidence in this record; `bytes` was a convenience,
not a need. The law M2.1 states — *authority that constrains execution must
be visible at the execution boundary* — is confirmed by the one experiment
that could refute it.

**Established, separately.** The per-condition closure budget is porous to
paraphrase (recorded above, before this outcome was known); the global
8-round cap is what bounds resolution.

**#89.** For the first time the natural task reaches audit, validation,
independent review, and a terminal outcome on the instrument carrying #94's
repair. What has still never happened naturally is a worker running
`go build` in the tree: four workers across two series verified with
`go vet`/`go test`, so the specific artifact-exclusion path has no
natural witness and the original 9 MB monster has not reappeared. Whether
#89's natural criterion is read as "the repaired pipeline reaches
audit/review/terminal on the natural task" (met by C1) or as "the artifact
exclusion itself is naturally exercised" (unmet, and possibly no longer
reachable with current workers) is a human decision, not an experimental
one. C2 and C3 are not run: the frozen question is answered, and further
invocations would be fishing for an accidental binary.
