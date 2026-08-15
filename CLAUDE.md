# Claude Code Instructions

Read and follow `AGENTS.md` first.

When invoked by Sensei Code as an implementation worker, you are operating in a bounded candidate worktree. Work autonomously inside that candidate, including inspection, edits, builds, and tests needed by the supplied plan. Do not ask the user for routine permissions.

Do not push, merge, deploy, change human-owned intent, weaken Sensei governance, claim admission, or treat passing tests as proof of completion. If the bounded plan cannot be implemented honestly, return the concrete blocker to Sensei Code.
