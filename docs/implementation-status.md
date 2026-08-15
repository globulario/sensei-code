# Implementation Status

This file distinguishes implemented behavior from planned Sensei Code architecture. It exists to prevent future agents from turning roadmap prose into claims about code that does not yet exist.

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

### Required Sensei toolchain

`doctor` requires the `sensei_workspace_status`, `sensei_workspace_admit_change`, and
`sensei_workspace_verify_admission` MCP tools. These are registered in
`globulario/sensei` `cmd/awareness-mcp/main.go`, but they are **absent from Homebrew
`sensei` 1.3.0**, whose `awareness-mcp` exposes 17 tools rather than 24. Against that older
binary `doctor` fails on those three checks and `sensei-code context` fails closed with
`unknown tool "sensei_workspace_status"`. Build `awareness-mcp` from a current Sensei
checkout until a release carries these tools. This is a Sensei packaging lag, not a Sensei
Code defect: no Sensei Code change can or should paper over a missing governance surface.

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
- session resume after process restart
- worker OS sandbox hardening beyond worktree/provider mechanisms

Those are the next governed implementation slices. They must reuse Sensei's canonical owners rather than inventing Sensei Code substitutes.
