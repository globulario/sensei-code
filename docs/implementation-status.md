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

The non-UI core packages are formatted, vetted, and tested with the locally available Go toolchain.

The current environment contains Go 1.23.2. Current Charm v2 packages require Go 1.25, and this environment cannot reach the Go module proxy, so the full TUI dependency graph cannot be compiled here. The TUI source was written against the current Charm v2 APIs and must be compiled in a networked Go 1.25+ environment before the PR is promoted from draft.

The current environment also does not contain the `codex` or `claude` executables, so provider end-to-end integration is not claimed here.

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
