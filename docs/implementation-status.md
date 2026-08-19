# Implementation Status

This file distinguishes implemented behavior from planned Sensei Code architecture. It exists to prevent future agents from turning roadmap prose into claims about code that does not yet exist.

## Whole-project review baseline

The first working Go version was reviewed against the governing architecture at `main` commit `d1342aa3eeaf94bfa9d80fa6a321abf62ed66922`.

See [`first-working-version-review.md`](first-working-version-review.md) for the required control-plane corrections and redesign sequence.

The important distinction is that several architectural laws already exist in documentation and prompts but are **not yet mechanical enforcement boundaries**. In particular, certifiability-driven authority routing, typed Sensei semantic gates, assisted-as-default interactive mode, mechanical capability enforcement, durable Level-3 governance writes, and runtime-integrated cross-agent continuity remain work to do. The implemented items below should therefore be read as foundation capabilities, not as proof that the full product contract is already enforced.

## Implemented in the Go foundation

- Go module and CLI entrypoint
- Bubble Tea v2 TUI source
- Bubbles v2 textarea source
- Lip Gloss v2 actor/status styling
- structured event model and in-process bus
- append-only local JSONL event sessions
- explicit execution / architectural / human authority types
- local capability configuration under `.sensei-code/config.json`
- direct argv process runner with streamed stdout/stderr events
- generic CLI agent adapter
- Claude Code output normalization
- Codex/Claude default role configuration
- Git repository discovery
- isolated sibling Git worktree creation
- direct Sensei MCP Content-Length JSON-RPC client
- Sensei MCP initialize / tools/list / tools/call support
- `sensei-code doctor`
- workspace status before work begins
- Sensei preflight before architecture
- bounded architect JSON protocol
- Level-3 1/2/3 human authority rendezvous
- Claude primary worker and Codex fallback
- bounded autonomous review/repair cycles
- Sensei `awareness_audit_diff` before reviewer decision
- reviewer accept/revise/escalate protocol
- automatic fallback when a bounded worker fails to converge
- fail-closed malformed model decisions

## Verified in the current development environment

Verified locally on Go 1.25.0 (2026-08-15), Phase 2 local smoke test:

- `go mod tidy`, `go test ./...`, `go vet ./...` all pass.
- `go build -o bin/sensei-code ./cmd/sensei-code` compiles the **full module including the TUI**. The earlier statement that the Charm v2 dependency graph could not be compiled reflected a Go 1.23.2 environment and is no longer true; GitHub Phase 1 also compiles the module on Go 1.25.
- `sensei-code init` writes `.sensei-code/config.json`.
- `sensei-code doctor` passes with every required Sensei MCP tool available.
- `sensei-code context` produces a real context packet from live Sensei evidence, carrying a `sensei.workspace.identity.v1` receipt with `composition_state: complete`.
- `sensei-code handoff` binds a handoff to that packet's exact digest.
- The TUI launches, renders, accepts composer input, and toggles agent activity with Ctrl+O.

`codex` and `claude` executables are present and resolve on PATH, so `doctor` reports them
green. Provider **end-to-end orchestration is still not claimed**: the smoke test
deliberately stopped short of submitting a task, so no provider was actually driven through
a workflow.

### Onboarding

`sensei-code setup` checks everything a session needs and, with `--apply`,
repairs what it can and re-checks rather than assuming the repair worked. It
loops because repairs unlock each other: `sensei init` creates the domain that
then has to be registered. Verified against a brand new repository, which it
takes to ready in two passes.

Launching in a repository that cannot work now fails immediately with the
report and the fix, instead of starting and failing the first task with a
symptom that names none of the cause. The launch check is the quick one, which
omits the checks needing a Sensei round trip rather than reporting them as
passing.

Installation is `brew install globulario/tap/sensei-code`, `winget install
Globulario.SenseiCode`, or `packaging/install.sh`. Sensei is a dependency of the
formula rather than a suggestion. `.github/workflows/release.yml` builds the
artefacts all three resolve; the formula and winget manifests in `packaging/`
carry placeholder checksums until a release is cut.

### Capability enforcement

`.sensei-code/config.json` declares nine capabilities. They are not all enforced, and the
difference matters because the flags read like guarantees:

- `read_repository`, `write_candidates`, `create_worktrees` are checked by the workflow
  before it acts.
- `push` and `force_push` are enforced for workers: when `push` is false a pre-push hook is
  installed and the worker's git is pointed at it through `GIT_CONFIG_*`, so git refuses the
  push rather than the worker being merely asked not to. This stops accidents, not a
  determined process, since anything that can edit its own environment can route around it.
  The candidate worktree branch remains the real blast-radius boundary.
- `run_builds`, `run_tests`, `local_commit` and `production_deploy` are **not enforced**.
  They are conveyed to workers as instructions in the prompt, and a prompt is not an
  enforcement boundary. Do not read them as sandboxing.

### Codex MCP tool allowlist

Codex treats `[mcp_servers.sensei.tools.<name>]` tables in `~/.codex/config.toml` as an
**allowlist**: a tool with no table is cancelled at call time, reported to the agent as
"user cancelled MCP tool call". A Sensei server can therefore be registered and still be
unusable. `sensei-code mcp` reports that case as `partial` rather than `configured`, and
can add the missing entries. Only read-only evidence tools are allowlisted; `awareness_propose`,
the admission and verification tools, and the task/projection tools are deliberately left
out so an agent cannot mutate governance state unattended.

### Required Sensei toolchain

`doctor` requires the `sensei_workspace_status`, `sensei_workspace_admit_change`, and
`sensei_workspace_verify_admission` MCP tools. These are registered in
`globulario/sensei` `cmd/awareness-mcp/main.go`, but they are **absent from Homebrew
`sensei` 1.3.0**, whose `awareness-mcp` exposes 17 tools rather than 24. Against that older
binary `doctor` fails on those three checks and `sensei-code context` fails closed with
`unknown tool "sensei_workspace_status"`. Build `awareness-mcp` from a current Sensei
checkout until a release carries these tools. This is a Sensei packaging lag, not a Sensei
Code defect: no Sensei Code change can or should paper over a missing governance surface.

The same build must also publish `change_risk` on the preflight tool's structured payload.
globulario/sensei#171 added those fields to the Preflight RPC, but `structPreflight` in
`cmd/awareness-mcp/main.go` dropped them, so an `awareness-mcp` predating the fix on
`fix/mcp-publish-change-risk` reports no approval gate at all. The router reads an absent
gate as unclassified and escalates, which is the correct direction to fail in and is
nonetheless an unusable governed path. `internal/acceptance/change_risk_test.go` asks the
deployment for it directly.

## Intentionally not yet claimed

The current workflow stops at:

```text
candidate ready for governed admission
```

It does **not** yet claim:

- canonical Sensei convergence bundle creation/binding for the candidate
- sealed Sensei candidate artifact lifecycle
- `sensei_workspace_admit_change` invocation with the exact canonical request artifacts
- application of the exact admitted artifact into the governed target
- `sensei_workspace_verify_admission` on the observed result
- proof/runtime evidence completion
- `complete_task` terminal completion
- Git commit/push/PR from the interactive workflow
- worker OS sandbox hardening beyond worktree/provider mechanisms

Those are the next governed implementation slices. They must reuse Sensei's canonical owners rather than inventing Sensei Code substitutes.
