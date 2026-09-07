# A governed refusal over an uncovered region, and the authority gap it exposed

**Specimen, not a proposal.** This records one governed run and two read-only
probes. It contains no design and authorizes nothing. Where it states a design
implication it says so explicitly, and those statements are inferences from the
run rather than law the system implements.

The run did not format three files. What it produced instead was a durable
question, an honest UNKNOWN, and evidence that Sensei's authority algebra is
incomplete in a specific, nameable way.

## The frozen world

```text
Sensei Code (candidate base)   412913900c05cfb2da9f6df5a65a0f4f17c8a922
Sensei Code (revalidation)     9af5f294afa3f6624f99ed815de345f1051e86f2
graph_build_commit             fd350489a19394be847d770f2e8dd7b8f1b75303
live triple count              34,627
graph_freshness_state          GRAPH_FRESHNESS_STATE_CURRENT
seed_state                     SEED_STATE_CURRENT
build_provenance_state         BUILD_PROVENANCE_STATE_STAMPED
authority                      authoritative / current
```

The two Sensei Code revisions are deliberately both recorded. The task ran at
`4129139`; the recipe it produced was revalidated later at `9af5f294`. A recipe
has no standing at a revision other than the one it is derived at, so
collapsing these into one "the world" would misrepresent what was proved where.

## The objective

245 bytes, `sha256 b6b4ff4b0045ff194e341c8cd3d442a96da460ba83fe63f9645f221ebac2a3b5`:

> Format exactly `internal/ghbridge/architecture_runner.go`,
> `internal/ghbridge/marker_test.go`, and `internal/ghbridge/runner_test.go`
> with gofmt. Make no semantic or behavioral changes. Verify those files are
> gofmt-clean and run the relevant Go tests.

All three files were genuinely gofmt-dirty at the pinned world. The objective
was well-formed and the work was real.

## The chain

```text
2026-09-07T02:45:47Z  comment 5564324135  davecourtois
                      [sensei-code:objective-proposal] -> inert durable record

              (local operator authority, real terminal)

02:52:33              approval receipt submitted
                      nonce 0fcd9ea07cf23b790b85866469fc9766
                      task-1788749553905987265 created
                      provenance: submitted by a local operator with access to
                      this process; no human presence was established

02:52:34              graph binding: domain github.com/globulario/sensei-code,
                      build fd350489a193
02:52:35              comment 5564369094  globulario-sensei-code[bot]
                      [sensei-code:architecture-request] r-eb71ed92f6d3e686
02:55:01              comment 5564384579  davecourtois
                      [sensei-code:architecture]  (remote architect answer)
02:55:08              answer consumed; plan evaluated
02:55:08              comment 5564385415  bot
                      [sensei-code:architecture-request] r-b8b91bfa0defe826
03:01:52              comment 5564429824  davecourtois
                      [sensei-code:architecture]  (second answer)
03:02:01              escalation; terminal authority.required
```

Every request and response carried the same complete binding: task, request id,
objective digest, base `4129139…`, graph `fd350489…`. The mailbox posted as the
App (`globulario-sensei-code[bot]`, id 325661868); the architect answered as the
configured remote principal (`davecourtois`, id 1697116).

## Why it refused

```text
02:55:08  no test-edit authority: internal/ghbridge/marker_test.go:
            no planned file in its directory holds architectural coverage at
            the pinned world
02:55:08  no test-edit authority: internal/ghbridge/runner_test.go:  (same)
02:55:08  derived coverage: 0 anchor(s) over 3 planned file(s);
            route bounded-knowledge-gap
03:02:01  the knowledge gap did not close; escalating with it open
03:02:01  human-authority-required: a bounded knowledge gap was not closed by
            investigation
```

`PREFLIGHT_STATUS_EMPTY` / `UNKNOWN_IMPACT` / `CONFIDENCE_LOW` is a valid START:
no plan exists yet, so no file list can be named, and the file-scoped verdict is
deferred. Once the plan named three files, that deferred verdict came due and
found zero anchors over `internal/ghbridge`.

The architect did not claim the gap was closed. Repository evidence cannot
manufacture graph coverage, and it declined to say otherwise.

## What the run produced instead

A durable, mechanically falsifiable question — committed at `27488c1`,
`docs/awareness/derived_recipes.json`, file `sha256 89e5ae3b…9954edc`:

```text
kind    state_mutation_confined_to_owner
dir     internal/ghbridge
type    ArchitectureRunner
field   Binding
search  internal/ghbridge
```

and an inference receipt — committed at `9af5f29`,
`docs/awareness/derived_receipts.jsonl`, file `sha256 e0f4237d…8ea6ed8`,
`outcome: RECORDED`, `closure_round: 1`, `closure_budget: 1`.

The receipt carries this, and it is the part most worth preserving verbatim:

> the investigator is a hosted large language model: sampling is
> nondeterministic, the served weights are not addressable by the caller, and no
> model artifact digest is obtainable. Re-running this configuration may produce
> a different question, and identical output is not evidence of a deterministic
> path

The system recorded that its own architect is not reproducible, and attached
that limitation to the artifact the architect produced.

## No mutation

```text
candidate                  never touched
the three files            still gofmt-dirty
working tree               porcelain 0
terminal state             authority.required  (preserved)
```

`authority.required` is neither success nor failure. It is a stop at a boundary
the run was not permitted to cross.

## Revalidation of the recipe

Run at the later pinned world, read-only.

```text
binary        /home/dave/Documents/github.com/globulario/sensei/bin/sensei
              sha256 87e1cb762e4c070642ff3dd29139cb7c0ef4430c41433a097344ed19d81c8979
              go version -m: path github.com/globulario/sensei/cmd/awg, mod (devel)
              NO vcs.* stamps -- its source commit is not recoverable from the
              artifact, and the SHA-256 is the only exact identity available

command       sensei derive -json
                -repo-root <sensei-code>
                -revision 9af5f294afa3f6624f99ed815de345f1051e86f2
                -kind state_mutation_confined_to_owner
                -dir internal/ghbridge -type ArchitectureRunner -field Binding
                -search internal/ghbridge

exit status   3
result        UNKNOWN
subjects      null
detail        no write to ArchitectureRunner.Binding found under
              internal/ghbridge; nothing to establish
derivation    derive.state_mutation_confined_to_owner / v1
produced_at   2026-09-07T03:29:55Z
inputs        11 files independently observed
```

Verified unchanged across the run: tree porcelain `0`, `docs/awareness` tree hash
`6a162fa8…e2136821`, oxigraph store `5,349,710` bytes, graph build `fd350489…`,
triple count `34,627`.

UNKNOWN yields zero coverage. The consumer's rule is explicit: *failure is
silence rather than coverage*, and *a recipe costs one derivation and buys
nothing on its own*.

### Why UNKNOWN, mechanically

`ArchitectureRunner.Binding` has no `*ast.AssignStmt` write anywhere under
`internal/ghbridge`. All four occurrences are `KeyValueExpr` inside composite
literals:

```text
resolver.go:114            Binding: spec.Architecture,
architecture_runner.go:41  Binding: r.Binding,
architecture.go:173,184    Binding: binding,
```

`state_mutation_confined_to_owner` counts `AssignStmt`, `IncDecStmt`, and `&e.F`
escapes as writes. `CompositeLit` appears in that derivation only to resolve
what type a name binds to, never as a mutation site — and deliberately so. Its
own contract records the reason: *"a constructor in the owning package filling
its own options struct is the owner exercising authority, not a bypass
(experiments/mutation-v1 in sensei-code closed on exactly that counterexample)."*

The verdict was correct. The proposition was simply outside the family's
vocabulary.

## Probe 1 — no derivation kind expresses initialization-only state

Three kinds are registered, and only three:

```text
field_access_under_lock          every ACCESS to a field occurs while a named
                                 lock field of the same struct is held
command_invocation_confined_to   every INVOCATION of a named executable within
                                 a scope originates from a named owner package
state_mutation_confined_to_owner every observable WRITE to a named exported
                                 field originates from the declaring package
```

None expresses "assigned nowhere / set only at construction / never reassigned".
There is no substitute: `field_access_under_lock` requires a lock field that
does not exist here, and `command_invocation_confined_to` is about executables.

## Probe 2 — `level-1-routine` is not a second authority currency

Six routes exist:

```text
architectural-authority-granted
human-authority-required
bounded-knowledge-gap
observation-no-authority-needed
cannot-establish-authority
level-1-routine
```

`level-1-routine` is the nearest candidate and is not one. Its own rule is
**"Relax interruptions, never evidence"**, it is described as *"a narrowing of
architectural authority, never a way around a human boundary"*, and its
qualifying conditions include, in order, *graph authority is certifiable*,
*preflight is ok*, and *coverage is proven rather than absent*.

It presupposes coverage. The run under study fails its second and third
conditions before reaching any other. Level-1 is a discount on ceremony for
changes Sensei already understands, not a different way of establishing
authority.

Both probes were read-only and verified to have mutated nothing.

## The finding

**Observed.** Current edit authority ultimately depends on architectural
understanding. Every route is an expression of it, including the one that looked
like an exception.

**Inferred design gap.** Transformations whose output can be mechanically proven
equivalent have no independent authority route. A change that is provably
incapable of introducing new meaning must presently be justified by
understanding what it means.

The second statement is an inference from this run. It is not implemented, not
specified, and not authorized by this document.

## Design implications — NOT implemented law

Three constraints emerged from the probes rather than being imposed on them.
They are recorded so a future design starts from evidence, and they carry no
authority here.

1. **A model may only narrow the permitted transformation, never assert
   safety.** Level-1 already refuses to let a model call its own change small
   ("smallness is computed, never claimed") and consults a model's statement
   only where it restricts. A declared "gofmt only" shrinks the permitted set;
   the machine would still have to prove membership.

2. **Such a route must not be able to widen itself.** Level-1 carries a
   `governancePath` exclusion list — `gate.go`, `authority.go`, `routine.go`,
   `internal/candidate`, `internal/authority`, `internal/broker` — because *"a
   routine tier that could fast-path an edit to its own qualifying conditions is
   a tier that can widen itself."* A transformation route needs that guard at
   least as strictly: a formatting change to its own gate is still a change to
   its own gate. (`internal/ghbridge` is not on that list.)

3. **Any mismatch collapses to normal governance.** One byte outside the
   canonical output of the declared transformation and the proof is gone.

A fourth question, following Level-1's own law, likely answers itself: relaxing
the authority SOURCE would not relax the evidence. Tests, candidate isolation,
audit and review would remain.

## Explicitly not established

- that a transformation-authority route is a good idea
- that `field_assigned_only_at_construction` should be registered — it was
  considered and set aside, because introducing an architectural vocabulary in
  order to get a formatter through a gate is suspicious coupling
- that the recipe's proposition is false; it is true of the code and
  unexpressible in the registry
- that this generation's coverage of `internal/ghbridge` should be published;
  no graph mutation was performed or is implied

## Loose ends at the time of writing

- `globulario/sensei` PR #345 (`fd350489`) is an unmerged draft. `:10122` serves
  it; `:10121` still runs the older binary with the abbreviated stamp.
- The control process is a `nohup` with no unit file, and now guards
  considerably more than when it was first started.
- The pre-existing `internal/control` `testClock` data race is unchanged.
- `task-1788749553905987265` remains at `authority.required`, deliberately. It
  is the witness of the uncovered world. If coverage is ever established and the
  same three-file change later proceeds, the preserved refusal beside the later
  success is what distinguishes learning from an override.

## Why this specimen is worth keeping

The run did not fail to format three files. It discovered that its authority
algebra was incomplete, refused to fabricate the missing premise, left behind a
question that can be re-asked mechanically at any future world, and stopped.

The region whose formatting drifted unnoticed is the region the graph cannot
see. The durable output of a run asked to fix the formatting is a question about
the blindness.
