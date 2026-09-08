# Control surface unit

`units/sensei-code-control.service` is the supervised unit for the Sensei Code
control surface and the GitHub webhook ingress. It is a **verbatim copy** of the
installed file at `~/.config/systemd/user/sensei-code-control.service`, following
the convention of `docs/work/evaluator-availability-repair/units/`.

Verbatim matters. A tracked copy that has drifted from the installed one is worse
than no copy, because it invites a reader to trust it. Check before believing it:

    diff ~/.config/systemd/user/sensei-code-control.service \
         docs/work/control-surface/units/sensei-code-control.service

Recorded at `be4edbabbd1805fe13fe983dff862b86c27632b6eddc6eeaf39290c7d56604ab`.

## This is a RECORDED INSTANCE, not a template

It carries values true of one machine and one installation:

    WorkingDirectory   /home/dave/Documents/github.com/globulario/sensei-code
    ExecStart          /home/dave/.local/share/sensei-code/bin/sensei-code
    secret / key paths under /home/dave/.config/sensei-code/
    -github-app-id 4850747  -github-installation-id 159521273
    -github-webhook-repository-id 1335129805
    -github-reviewer-id 1697116 (davecourtois)

None of these are secrets — the webhook secret and the App private key are
referenced by PATH and their contents never appear here or in any log line. But
they are not portable, and installing this file elsewhere without changing them
would bind another machine to this installation's identity.

`WorkingDirectory` is load-bearing rather than tidiness: `discoverProposalRepoRoot`
walks up from the process's cwd to the nearest `.git` boundary and uses that
worktree for the inert proposal ledger. Started anywhere else, an authenticated
objective proposal is a receive failure rather than a durable record.

## Why it is recorded at all

The unit's comments carry reasoning that exists nowhere else in the repository:
why two listeners never share a socket, why `StartLimit*` must sit under
`[Unit]`, why the process is deliberately not sandboxed, and why
`-github-bridge-roles` is an instrument chosen per experiment rather than a
default. That reasoning was rewritten twice on 2026-09-08 — once to carry the
architect and reviewer roles for the #158 experiment, once to return them to
`none` after it succeeded — and none of it was recoverable from the repository.

The unit predates this record by a day, during which it existed only as a file
on one machine. Before that it was a `nohup` of a binary in a scratchpad
directory, which is the failure its own header describes.

## Related

- #156 / #157 — the mailbox this unit serves (PR #157's conversation)
- #158 — the wake path the role flags were carried for; commissioned 2026-09-08
- #162 — exchange lifetime, so a request whose waiter dies is retracted
- globulario/sensei#346 — why a briefing against this graph reports DEGRADED
