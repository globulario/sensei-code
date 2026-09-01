# Evaluator availability repair — drafts for review, nothing installed

`.sensei/config.yaml` names `localhost:10122` as the evaluator endpoint. No
supervised process has ever served it. What serves it today is a hand-started
process running a binary out of a session scratchpad — see `REVIEW.md`.

| path | what it is |
|---|---|
| `REVIEW.md` | the review + install + verification procedure, and the reboot checklist that alone closes the failure mode |
| `units/awg-awareness-graph-10122.service` | the supervised unit, a twin of the `:10120` unit differing only in port and identity |
| `units/awg-oxigraph.service.d__10-break-ordering-cycle.conf` | drop-in removing the `default.target` ordering cycle that deletes the graph unit's start job at boot |
| `startlimit-separate/` | an INDEPENDENT finding. Do not install it with this repair |

## Why the third finding is quarantined

`awg-awareness-graph.service` declares `StartLimitBurst`/`StartLimitIntervalSec`
under `[Service]`, where systemd ignores them — the journal says so on every
load. The retry bound its own comment describes has never been enforced.

That is a real defect and its fix is a two-line drop-in. It is kept out of this
repair on purpose: if both land together, a successful restart test cannot say
which change produced the behaviour. One repair, one verifiable consequence.

## Status

Prepared for review under an explicit instruction not to install, enable,
restart, or reboot. Nothing here has been applied to the machine.
