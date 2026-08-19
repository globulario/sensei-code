# Open items

## P0 acceptance record

```text
P0 control-plane slices:        9/9 implemented (P0.1-P0.8, plus P0.9 discovered by the canary)
unit/structural tests:          green
integrated governed acceptance: SUPERSEDED — see below
blocked on:                     sensei-code#13, which prevents a governed run today
known enforcement gap:          per-provider process sandbox (see below)
upstream dependencies:          globulario/sensei#171, #172, #173, #174, #175
```

**The acceptance is superseded, not merely stale.** The PASS at `1bc39f29a7a2`
happened and is described faithfully below, but it was obtained under a
deployment later shown to be wrong about custody — and that same
misconfiguration was masking an ordering defect, by writing this repository's
governance into another repository and so keeping this tree artificially clean.
The run passed partly *because* of the fault it failed to reveal.

Three things changed afterwards and none has been re-established end to end:
custody was corrected (`sensei-code#10`), the candidate base was pinned before
the workflow mutates its own repository, and mutual authority exclusion between
two servers sharing one store was discovered (`sensei-code#13`).

So the mechanisms are proven and the composition is not. Anyone reading this
record should treat P0 as implemented and unaccepted until a governed run
reaches ACCEPT under the topology actually in use. Recording it as PASS would be
the same overstated receipt this system exists to refuse, written by the system
about itself.

The accepted run, `task-1786929227938945137`, went the whole distance without
bypass: certifiable start, authority escalated once and answered, resolution
proposed to Sensei's review queue, plan approved, candidate cut from an exact
base, capability guards installed, worker implemented, four validation checks
executed by the broker and bound to the diff digest, `awareness_diff_audit`
returning `decision: pass · availability: available`, and a reviewer ACCEPT that
cited the governing invariant by name. The pull-request rendezvous was declined:
publication stayed human-owned even on an accepted candidate.

**One precision, so the record is not read as more than it is.** P0.8
cross-agent handoff was proven in an *earlier* run — Claude exhausted its
bounded cycles, state transferred, and Codex continued the same candidate rather
than restarting. The final accepted run converged in a single cycle and
therefore exercised no handoff. Both facts are true; they come from different
runs, and the acceptance record should not imply one run demonstrated
everything.

### The accepted candidate

Retained rather than deleted, pending a decision on whether to adopt it.

```text
branch      sensei-code/task-1786929227938945137
worktree    ../.sensei-code-worktrees/task-1786929227938945137
base        1bc39f29a7a2 (clean)
content     internal/tui/model_test.go, +103, one file added
diff digest 17b59fda5bc0
audit       awareness.diff_audit/v1 · decision: pass · availability: available
            digest 6d3ad6032999da1f408763da949800984df9aabe081e076565d77f6edf16a597
            changed_files_count 1 · findings_count 0
evidence    gofmt -l cmd internal   exit 0
            go vet ./...            exit 0
            go build -buildvcs=false ./...  exit 0
            go test ./...           exit 0
```

The work is uncommitted in its worktree, so deleting the branch would discard
it. It is a legitimate test — it proves the invariant "An unrecognised command
is answered, never handed to the architect as work" at the real TUI boundary —
but adopting it is a publication decision, and the run's own pull-request
rendezvous was declined precisely because publication is human-owned. Adopting
it here would take by hand the step the machine correctly refused to take by
itself. Remove the branch once it is either adopted through review or judged not
worth keeping.

**What the canary has not yet covered**, and what the next acceptance expansion
should add: a change that touches production code rather than tests, a candidate
that legitimately fails its checks, and a candidate the audit blocks. Passing on
one task shape is evidence about that shape.

Work that is deliberately incomplete, recorded here so a later reader does not
mistake a partial implementation for a finished one. Each entry says what is
done, what is not, and what "done" would require.

An item leaves this file when it is closed, not when it is tidy.

## Authority resolutions written into the wrong physical authority root

`globulario/sensei-code#10`, upstream `globulario/sensei#175`.

A Level-3 resolution in this repository is proposed through `awareness_propose`
and the candidate lands in the **services** corpus. Characterised with a
controlled reproduction; no fix applied during investigation.

The mechanism is sharper than it first appeared, and the first description of it
here was wrong. It is not a wrong home domain: `-home-domain` governs untagged
knowledge nodes, and the server's own flag documentation says `-repo-domain` is
"NOT home-domain". Custody is decided solely by `-awareness-dir`. So this is
correct semantic labeling routed into the wrong physical authority root.

What makes it durable is that three signals agree while the write location
disagrees — the request domain, the stored content and the generated id all say
sensei-code, and only the filesystem says otherwise. `Propose` returns
`accepted: true` and a relative path, so a caller cannot notice.

Established: `domain` shapes content and the id slug (`domainHint`) but never the
destination; the server write model is single-repository by design; shared
multi-domain custody is unsupported; the server never loads `domains.yaml` and so
cannot route by domain today; per-domain servers can share one Oxigraph for reads,
with the global graph marker needing resync after any per-domain build.

**Disposition.** Immediate fix is deployment-owned: point sensei-code at an
awareness server whose `-awareness-dir` is this repository's corpus. Upstream
follow-up is mismatch *rejection*, not registry-based routing — making Propose
resolve domains would turn one server into a multi-repository write router,
which is a different architecture adopted to fix a bug.

**Resolved by deployment isolation**, not by a workaround: the topology below is
what Sensei's single-write-root design already implies.

```text
services      :10120   -awareness-dir services/docs/awareness      (untouched)
sensei-code   :10121   -awareness-dir sensei-code/docs/awareness
                       -repo-domain / -repo-root: sensei-code
Oxigraph      :7878    shared, read-only from both servers
```

Verified by `internal/acceptance/authority_root_test.go`, which is executable
rather than a one-off: it digests the services corpus, proposes through the
configured endpoint, resolves the returned relative path against this
repository's awareness directory, requires the file to declare the sensei-code
domain, requires the services corpus to be byte-for-byte identical afterwards,
and requires the server to remain authoritative. That last check matters on its
own: writing to the right corpus while going non-authoritative would be correct
custody and an unusable graph.

**Operational gap, deliberately not blocking.** The `:10121` server is not yet
durable across reboot. After a restart sensei-code points at a dead port and
fails closed, which is inconvenient and does not violate the authority
boundary.

## Upstream (Sensei): two defects found while commissioning

`globulario/sensei#173` — `propose --kind decision` corrupts a scaffolded
`decisions: []` on first append, producing YAML that cannot parse. A governed
file that cannot be parsed is absent from the graph, and nothing in the propose
path re-reads what it wrote, so every decision recorded here was silently
missing until a rebuild was finally required.

`globulario/sensei#174` — `task_status` fails with ENOENT when `.sensei/tasks`
does not exist, which is the ordinary state of a repository that has not used
tasks. An empty answer is being reported as a failure to answer.

## Governed runs are on hold until globulario/sensei#176

`sensei-code#13`. Two servers sharing one Oxigraph store cannot both hold
authority: a build of one registered domain recomputes the global marker and
regenerates the proof set only for that domain, leaving every other domain
vouching for a publication that no longer exists. Demonstrated in both
directions.

The fix is upstream and is a property of publication, not of deployment:

> A build of one registered domain must not invalidate the authority proofs of
> other registered domains in the same graph store.

Separate stores would keep custody and fracture the graph; one shared server
would keep the graph and reinstate the custody defect proved in
`sensei-code#10`. Neither trade is worth making, and the second would restore a
green light by hiding the class of bug that invalidated the earlier acceptance.

**Until #176 lands, do not start a governed run in this repository.** Each one
takes authority away from the services server that another workstream depends
on. This is a constraint of the shared substrate, not a policy choice. Assisted
mode is unaffected: it reads, and says so when the graph cannot vouch for
itself.

## Candidate worktrees reach a stated disposition

`globulario/sensei-code#12`. **Implemented.**

Every governed task created a worktree and a branch and nothing ever resolved
them; seven accumulated in one day of acceptance runs, six dead and one holding
real unpublished work, with nothing telling them apart. The cost was never disk.
Recovery reads candidate and task state from disk, so undifferentiated leftovers
turned "resume the interrupted task" into archaeology.

A candidate now carries a terminal `Resolution` in the same durable record as
its base: a disposition from a closed vocabulary — `retained`, `adopted`,
`rejected`, `superseded`, `resumable`, `disposed` — with the reason for it and
the evidence that survives it. Two properties are enforced rather than intended:

- **Evidence is recorded before anything is removed.** Base, changed paths, diff
  size and audit verdict are written first, so a cleaned-up candidate leaves a
  record rather than a gap. A disposal whose git step then fails records that
  the worktree is still present, which is a fact somebody can act on.
- **Retention is a decision with a reason.** An accepted, unpublished candidate
  is `retained` because landing it is the human's decision — not because nobody
  deleted it.

Automatic removal is reached only by a candidate whose own recorded evidence
says it produced no work. Anything holding work is `resumable`, with the reason
attached, for a person to dispose of deliberately. Deleting on exit would have
performed the deletion half of the publication decision the system correctly
refuses to make.

`/candidates` lists what exists and why, and reports a candidate nobody decided
about as **unresolved** rather than describing it as retained.

**The existing backlog is deliberately untouched.** The eight candidates from
before this landed — including the accepted `task-1786929227938945137` — are
reported as unresolved, which is exactly what they are. Disposing of them is a
human decision about unpublished work, and making it automatically here would be
the same mistake in a new coat.

## Architect conversation parity is implemented except its governed-run acceptance

`globulario/sensei-code#9`. **Sections A, C, D, E, F and G are implemented.**
What is not done is the acceptance that needs a governed run, listed at the end
so the rest is not read as complete.

Landed:

- **Durable conversation identity** (`internal/continuity`). The architect
  process starts fresh every turn, so continuity is recorded — but what is
  recorded is identity, not content: which architect, which provider handle,
  which repository base, how many turns. There is deliberately no field that
  could hold a decision. A local file that could carry one would be a second,
  weaker governance store, and the first time it disagreed with Sensei the
  disagreement would be invisible.
- **Stated reconstruction.** A thread that cannot be resumed — a different
  architect, or a provider that issued no handle — produces a specific reason,
  and the turn tells the architect it is reconstructing from the session record
  and Sensei rather than from remembered dialogue. A repository that moved under
  a continuing conversation is reported as well; that is not a loss of
  continuity but it is a fact the architect must have.
- **The evidence drawer** (`assist.Consulted`). Every assisted turn emits which
  sources it consulted and what state each was in, using the same typed
  vocabulary as the context packet. It is emitted on healthy turns too: a
  provenance surface that only appears when something is wrong is one nobody
  learns to read. A source that failed appears as a source that failed, because
  a retrieval failure that silently drops out is model memory wearing the
  graph's clothes.

Also landed:

- **Relevance-driven retrieval and a stated budget (§C, §F).**
  `internal/retrieval` selects lookups from what the question names — paths go
  to `awareness_briefing`, governed ids to `awareness_query` in `by_id` mode —
  and nothing else. Reading the question routes a lookup and decides nothing:
  a target the graph has never heard of is reported as asked-and-empty, never
  widened until something comes back. Only typed read surfaces are reachable,
  through an adapter that cannot name a writing tool. The budget is small and
  is disclosed with the targets it dropped, because a turn that consulted four
  sources out of nine reads exactly like complete coverage.

- **Standing project context, derived (§D).** `internal/project` assembles
  recent task outcomes, unanswered authority questions and recorded decisions
  from the session record, every turn. Nothing is stored: a maintained summary
  is a second store of architectural claims, and it wins by being convenient the
  moment it disagrees with Sensei. It carries references and says so, in case a
  reference is otherwise read as a finding.
- **Read-only investigation (§E).** `internal/investigate` is a closed allowlist
  of git subcommands that refuses everything else by type, rather than a prompt
  asking the architect to behave. What it cannot read it states, because a blank
  field reads as nothing-to-report. Its paths are the ones the graph retrieval
  selected, so repository and graph evidence are about the same subject.

Not done:

- **Acceptance tests 1, 4-9, 11-12** from the issue remain unwritten; several
  need a governed run, which is on hold for `#13`.
- Remote ChatGPT-thread resume stays opportunistic, as the issue allows: the
  architect surface reports no resumable handle today, which is exactly the case
  the continuity record reconstructs from.

## `sensei build` publishes to a flag, not to the project's configured store

Found the expensive way on 2026-08-19, and recorded because the surface is
plausible enough to catch the next person.

`.sensei/config.yaml` names a store:

```yaml
store:
    query_url: http://localhost:7878/query
    store_url: http://localhost:7878/store?default
```

`sensei build` does not load through it. The endpoint comes from the
`-store-url` flag, whose default is `http://localhost:7878/store?default`, so
editing the project config changes nothing and the build silently publishes
wherever the flag points. A build intended for an isolated store on another port
went into the shared services store instead, added seven `aw:repo` tags onto
subjects services also owns — `sensei init`'s scaffold generates the same
canonical guardrail IRIs for every repository — rotated the global marker, and
left services UNPROVEN in the new proof set, which the build itself reported:

```text
UNPROVEN github.com/globulario/services — the slice for
"github.com/globulario/services" changed during a publication of "example.com/a"
```

**This is `sensei-code#10`'s shape a third time.** Custody is decided by a flag
while the operator is reading a config file, exactly as write custody is decided
by `-awareness-dir` regardless of the domain a proposal requests. Three signals
agree — the project config, the working directory, the domain on the command
line — and only the flag decides. A configuration file that names a store the
build ignores is not an inert field; it is a statement the system does not
honour.

The repair for the specific pollution is to republish the affected domain from
its own corpus, which recomputes its slice and regenerates its proof:

```bash
cd <services checkout> && sensei build --repo github.com/globulario/services
```

Worth raising upstream: either the build should read the project's configured
store, or `-store-url` should refuse to differ from it silently. Publishing to
somewhere other than where the operator believes is the failure class this
repository has now met three times in three different surfaces.

## Capability envelope is only partly enforced (P0.4)

**Done.** `internal/broker` mechanically enforces three capabilities for a
worker process: `push`, `force_push` (detected as a non-fast-forward push,
including branch deletion, because git gives a pre-push hook no `--force`
flag), and candidate isolation via `GuardCanonicalCheckout`. These are enforced
by git itself, not by prompt text, and are covered by tests that drive real
pushes against a real remote.

**Not done.** `run_builds`, `run_tests`, `local_commit` and
`production_deploy` are still not mechanically preventable. Nothing stops a
worker process from invoking a compiler, opening a socket, or executing a
deploy script it wrote itself. `Envelope.Unenforceable()` reports exactly this
set so readiness can fail the role, which is the correct fail-honest interim
behaviour — but reporting a gap is not closing it, and the envelope is not
complete until these are enforced.

**What closing it requires.** A process-level sandbox per provider adapter:
restricted exec, filesystem and network policy applied to the worker process
itself. Each provider must declare what it can enforce, and a provider that
cannot honour a required boundary must fail closed for that role rather than
silently widening the envelope.

## Change risk is read structurally, and the parser is gone

`globulario/sensei#171` published blast radius and approval gate as structured
fields on the preflight result, which was the stated condition for deleting
`approvalGate` in `internal/workflow/authority.go`. It is deleted. The router
reads `change_risk.approval_gate` and `change_risk.blast_radius`, and no
governance transition in this repository now depends on recognising a sentence.

Two things surfaced while doing it, and both are worth keeping.

**#171 landed on the RPC and not on the surface this repository reads.** Sensei
Code reaches Sensei only over MCP, and `structPreflight` in
`cmd/awareness-mcp/main.go` projected the string lists while dropping
`change_risk` entirely. Deleting the parser on the strength of the closed issue
alone would have left the router with no verdict at all, escalating every plan
forever with nothing to say why. Fixed upstream on `fix/mcp-publish-change-risk`
with the same verdict published on both surfaces, and verified over the real
wire from this repository: a scoped request decodes as `blast=local,
gate=none`, and a fileless one as explicitly unclassified. **This repository now
requires an `awareness-mcp` built from a Sensei carrying that fix.**

**The old parser failed open, not closed.** With no `Change risk:` line present
it returned `""`, and the caller's guard read that as permission and granted
architectural authority — while the function's own comment claimed it failed
closed. Sensei's proto is explicit that an unspecified gate means unclassified
and never `none`, so the structured reader escalates on an absent verdict, an
explicitly unspecified one, and on a member this build has never seen.

`internal/acceptance/change_risk_test.go` asks the deployment whether it
publishes the fields, and skips rather than passes when the endpoint cannot
scope this repository, so it cannot report a verdict nobody gave.

## The configured Sensei endpoint is serving another repository's graph

Found while verifying the above, not yet fixed. `.sensei-code/config.json`
points this repository at `localhost:10121`, which the `sensei-code#10`
disposition established as the sensei-code authority server. The process
currently listening there is not that server:

```text
:10120  awareness-graph -oxigraph-url 127.0.0.1:7878 -home-domain globular
        domains [github.com/globulario/services globular], 120243 triples
:10121  awareness-graph -oxigraph-url 127.0.0.1:7880
        -home-domain github.com/globulario/sensei
        domain [github.com/globulario/sensei], 8304 triples, no -awareness-dir
```

So every preflight this repository issues is answered by the *Sensei*
repository's graph, on a different Oxigraph store than the one the topology
above describes. It does not error. It answers `coverage_insufficient`, which
reads as "this region is thinly covered" rather than "you are asking a graph
about a repository it has never seen" — the `sensei-code#10` failure class
again, in read form: the request, the content and the id all say sensei-code,
and only the deployment disagrees.

Nothing here is proof of a defect in Sensei; it is a local deployment that
drifted. It is recorded because it is invisible from the answers themselves,
because it would have been read as evidence by any governed run started today,
and because a proposal written through that endpoint has no `-awareness-dir` at
all and would land wherever that server's default write root is.

Restore the documented topology before the next governed run, and re-read
`internal/acceptance/authority_root_test.go`'s result as the check that it took.

## Remaining first-version control-plane slices

All P0 slices from `docs/first-working-version-review.md` are implemented, with
the P0.4 caveat recorded above: the capability envelope is enforced for push,
force-push and candidate isolation, and open for the rest.

Specified but not yet implemented: `docs/p1-repair-knowledge.md`, which lets the
project learn the repair that actually held rather than the one a reviewer
accepted. It was blocked on globulario/sensei#172, which has since landed
`applied_repair` as the positive counterpart to `forbidden_fix`, so the design
is implementable and only unimplemented.

Specified but not yet implemented: `docs/p1-level-1-routine.md`, which makes
ceremony proportional to measured risk without reducing verification. It is
sequenced after the P0 merge and after the governed acceptance run passes,
because a path that skips steps is much harder to debug while the full path has
never completed.

Not yet started, tracked in the same review: the P1 items, including `doctor` as
the single readiness computation promised by the architecture, splitting the
workflow monolith by semantic ownership, and making event/session persistence
failures visible. Beyond those, the canonical Sensei admission/apply/verify
lifecycle remains the last slice of the original sequence and is deliberately
untouched.
