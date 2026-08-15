# Sensei Code Agent Instructions

Sensei Code is an autonomous orchestrator around Sensei. It is not itself a coding agent and it must not duplicate Sensei governance semantics.

## Architectural laws

1. Sensei is the authority for architectural truth, task/convergence state, evidence, admission, verification, and closure.
2. Sensei Code owns orchestration, provider lifecycle, Git/worktrees, local UX state, events, and authority routing.
3. Workers mutate isolated candidates. They do not own architecture or admission.
4. The architect may resolve ordinary architectural decisions autonomously.
5. Ask the human only at a genuine human-owned authority boundary. Do not add routine permission prompts.
6. Reviewer acceptance is not Sensei admission.
7. A local event/session record must never masquerade as a Sensei receipt.
8. Fail closed when required Sensei evidence is stale, unavailable, refused, malformed, or uncertifiable.

## Implementation rules

- Go is the implementation language.
- Use direct `exec.CommandContext` argv. Do not route provider or Git commands through a shell.
- Keep provider adapters replaceable.
- Keep the TUI a projection of structured events; governance semantics belong in the workflow/Sensei layers.
- Preserve candidate worktree isolation.
- Do not import internal Sensei Go packages to copy governance logic. Use public Sensei structured surfaces, primarily `awareness-mcp`.
- Keep `.sensei-code/` local and non-authoritative.
- Add tests for failure/authority behavior whenever the workflow state machine changes.

## Verification

Before claiming a slice complete:

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
```

If the environment cannot perform a check, record the exact missing tool/dependency in `docs/implementation-status.md`. Never turn an unrun check into PASS.
