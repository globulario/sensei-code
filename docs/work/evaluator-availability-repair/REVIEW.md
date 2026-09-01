# Evaluator availability repair — for review only

Closes nothing yet. `failure.sensei_code.configured_evaluator_endpoint_has_no_supervised_process`
closes only after the supervised unit survives restart AND reboot with the
services authority referent and full certification. The hand-started twin is
EVIDENCE OF THE CORRECT CONFIGURATION, NOT THE FIX.

NOTHING IN THIS DIRECTORY HAS BEEN INSTALLED. These are drafts for review.

## How fragile the current state actually is

The twin is not merely unsupervised. As of 2026-08-31 it is running a copy of
the binary INSIDE A SESSION SCRATCHPAD:

    /tmp/claude-1000/.../scratchpad/awareness-graph-1b39 -addr :10122

So :10122 is served today by a process whose executable lives in a temporary
directory, started by hand, supervised by nothing, and holding the port that
`.sensei/config.yaml` names. It survives only until that process is killed;
the scratchpad path does not survive the session at all. Every governed run
that reaches the configured evaluator currently depends on it.

Its PID is deliberately NOT recorded here. An earlier draft named PID 308968;
the process has since been restarted and is now a different PID, so any
instruction written against a number is stale the moment it is written. Find
it by port instead (section 3a).

## Files

| draft | install path |
|---|---|
| `awg-awareness-graph-10122.service` | `~/.config/systemd/user/awg-awareness-graph-10122.service` |
| `drop-ins/awg-oxigraph.service.d__10-break-ordering-cycle.conf` | `~/.config/systemd/user/awg-oxigraph.service.d/10-break-ordering-cycle.conf` |

INSTALL EXACTLY THOSE TWO FILES. A third finding (the :10120 unit's
StartLimit* keys sitting in [Service], where systemd ignores them) is written up
in `startlimit-separate/NOTE.md` and MUST NOT be installed with this
repair: if both land at once, a successful restart test cannot say which change
produced the behaviour.

## 1. Review BEFORE installing (reads only)

    # syntax + semantics of the draft, from the scratchpad copy
    systemd-analyze --user verify <DRAFT>/awg-awareness-graph-10122.service

    # the cycle as it stands today
    systemctl --user list-dependencies default.target | grep -i awg
    systemd-analyze --user verify awg-oxigraph.service awg-awareness-graph.service
    journalctl --user -u awg-awareness-graph.service | grep -i "ordering cycle"

## 2. Install, then inspect the EFFECTIVE units before starting anything

    mkdir -p ~/.config/systemd/user/awg-oxigraph.service.d
    cp <DRAFT>/awg-awareness-graph-10122.service ~/.config/systemd/user/
    cp <DRAFT>/drop-ins/awg-oxigraph.service.d__10-break-ordering-cycle.conf \
       ~/.config/systemd/user/awg-oxigraph.service.d/10-break-ordering-cycle.conf
    systemctl --user daemon-reload

    # what systemd actually resolved, drop-ins included
    systemctl --user cat awg-oxigraph.service awg-awareness-graph-10122.service
    systemctl --user show awg-oxigraph.service -p After -p Wants -p WantedBy
    systemctl --user show awg-awareness-graph-10122.service \
      -p Requires -p After -p Restart -p RestartUSec -p StartLimitBurst -p StartLimitIntervalUSec
    systemd-analyze --user verify awg-awareness-graph-10122.service

Expect: `awg-oxigraph.service` After= no longer contains `default.target`;
the :10122 unit shows `Requires/After=awg-oxigraph.service`, `Restart=on-failure`,
`RestartUSec=5s`, `StartLimitBurst=5`, `StartLimitIntervalUSec=2min`.

## 3a. STOP THE HAND-STARTED TWIN FIRST

The twin holds :10122. The supervised unit CANNOT start while it does -- it
would fail to bind. Stop it before starting the unit, not after.

Identify it by PORT, never by a recorded PID (see the note above):

    ss -ltnp | grep ':10122'                     # the pid is in the last column
    ps -o pid,cmd -p <that pid> | cut -c1-200    # CONFIRM before killing:
    #   it must be an awareness-graph binary with -addr :10122.
    #   A scratchpad path in cmd is expected and is the point of this repair.
    kill <that pid>
    ss -ltn | grep 10122     # expect NO listener before continuing

## 3. Enable + start, then post-start checks

    systemctl --user enable --now awg-awareness-graph-10122.service
    systemctl --user status awg-awareness-graph-10122.service --no-pager

    # listening address
    ss -ltn | grep -E ':(10120|10122)'

    # process arguments -- the authority referent must be the SERVICES marker
    ps -o pid,cmd -C awareness-graph --no-headers | cut -c1-300
    #   expect BOTH instances to carry:
    #     -graph-marker-file .../globulario/services/.sensei/graph-authority.json
    #     -home-domain github.com/globulario/services
    #     -no-seed
    #   and to differ ONLY in -addr, and BOTH to run the repo binary at
    #   /home/dave/Documents/github.com/globulario/sensei/bin/awareness-graph
    #   -- no scratchpad path may remain

    # (see section 3a -- the twin must already be stopped before this point)

## 4. Certification checks (the ones that actually matter)

    # composition, coverage, limitations, authority
    #   expect: composition_state complete, COVERAGE_STATE_SUFFICIENT,
    #           graph_authority.authoritative true,
    #           live_store_graph_digest_sha256 401d4bb7...,
    #           live_store_graph_triple_count 299036,
    #           limitations: []   <-- must be EMPTY
    sensei task-status -active -verify        # or the workspace status MCP tool

    # the evaluator path that failed during witness B
    cd /home/dave/Documents/github.com/globulario/.sensei-code-worktrees/task-1788136200536005547 \
      && sensei gate --diff b789200..b26f46c --domain github.com/globulario/sensei-code --enforce
    #   expect exit 0 and "PASS: 0 blocking findings"
    #   exit 2 means Sensei could not verify -- fail-closed, NOT a pass

## 5. Restart behaviour

    systemctl --user restart awg-awareness-graph-10122.service
    # repeat section 4; then prove the retry bound is real:
    systemctl --user stop awg-oxigraph.service
    systemctl --user start awg-awareness-graph-10122.service   # ExecStartPre must fail loudly
    journalctl --user -u awg-awareness-graph-10122.service -n 20 --no-pager
    #   expect the named reason "oxigraph did not answer ..." and a FAILED unit,
    #   not an eternally activating one
    systemctl --user start awg-oxigraph.service
    systemctl --user start awg-awareness-graph-10122.service

## 6. Reboot checklist — the only thing that closes the failure mode

    [ ] reboot
    [ ] systemctl --user is-active awg-oxigraph.service            -> active
    [ ] systemctl --user is-active awg-awareness-graph.service      -> active   (the :10120 unit, whose start job the cycle used to delete)
    [ ] systemctl --user is-active awg-awareness-graph-10122.service -> active
    [ ] journalctl --user -b | grep -i "ordering cycle"            -> no match
    [ ] ss -ltn | grep -E ':(7878|10120|10122)'                    -> all three
    [ ] ps -o cmd -C awareness-graph                               -> services marker + home domain on BOTH
    [ ] workspace status                                           -> composition complete, coverage sufficient, limitations []
    [ ] sensei gate --enforce (section 4)                          -> exit 0, PASS

Only when every box is ticked does the failure mode close, and it closes as a
DEPLOYMENT repair -- it says nothing about the two engine-semantics failure
modes, which remain open regardless of how well this evaluator runs.

## Rollback

    systemctl --user disable --now awg-awareness-graph-10122.service
    rm ~/.config/systemd/user/awg-awareness-graph-10122.service
    rm ~/.config/systemd/user/awg-oxigraph.service.d/10-break-ordering-cycle.conf
    systemctl --user daemon-reload
