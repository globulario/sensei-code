# Implementation brief: observation instrumentation must not mutate the governed repository

## Why this exists

The repaired observation lane moved the architect into a detached disposable worktree, which correctly stops the governed checkout from being the provider's working directory. A later audit found a narrower defect: the read-only measurement itself uses `git status --porcelain`, and Git may refresh index stat data while answering that query.

A checker for a read-only lane must not become the write it is trying to detect.

The claim for this slice must stay narrow:

> Observation cannot modify the governed repository's tracked content or working tree through its own orchestration and measurement operations.

Do **not** claim filesystem confinement or "no side effects anywhere". A detached worktree is isolation of working directories, not a sandbox.

## Required investigation

Before editing, enumerate every Git command executed against the governed checkout during `observe`, including cleanliness checks, worktree creation/removal, HEAD/revision discovery, and cleanup. Identify which commands can acquire optional locks, refresh the index, write refs, or otherwise mutate repository metadata.

Do not assume `status` is the only one.

## Required behavior

- Read-only inspection/measurement against the governed checkout must use non-refreshing/non-optional-lock Git invocation where Git supports it, e.g. `--no-optional-locks` / `GIT_OPTIONAL_LOCKS=0`, through the repository's normal direct-argv execution path.
- The provider continues to run only in the disposable detached workspace.
- Provider writes in that disposable workspace remain observable/reportable but are not confused with governed-checkout mutation.
- The disposable worktree must be removed before the terminal `workflow.observed` event; deferred cleanup remains only a backstop.
- Cleanup failure is an error, not a successful observation with residue.
- Preserve the final governed-checkout cleanliness check as defense in depth, but do not describe it as the safety boundary.

## Tests that must exist

1. A real Git integration test records the governed repository index/worktree state, performs the observation-lane read-only checks, and proves the measurement itself did not modify the index or tracked working tree.
2. A provider that writes inside the disposable workspace is detected/reported, while the governed checkout remains unchanged.
3. A simulated/forced worktree-removal failure prevents the observed terminal event.
4. After a successful observation, there are no registered leftover worktrees and no child processes.
5. Comments and user-facing strings must state the narrow guarantee. Add a regression assertion for any canonical phrase so a future edit cannot silently return to "no file is written" or "separate worktree prevents access to the governed checkout".

Where platform/filesystem timestamp noise makes byte-for-byte metadata checks unreliable, document exactly what observable is used and what it proves. Do not turn an unobservable property into PASS.

## Non-goals

- Do not build a general OS sandbox in this slice.
- Do not claim the provider cannot reach arbitrary filesystem paths.
- Do not treat a clean final tree as proof that no intermediate write occurred.
- Do not allow a provider flag to assert read-only behavior as evidence.

## Success criterion

The observation lane's own Git and orchestration operations do not mutate the governed repository state they are measuring, the disposable workspace is cleaned before success is emitted, and all comments/status text claim only what the implementation actually enforces.
