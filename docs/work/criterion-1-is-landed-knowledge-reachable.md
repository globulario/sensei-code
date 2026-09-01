# Criterion 1, measured: landed knowledge is not reachable

The program's definition of success is explicit that an empty board is not
completion:

> Do not declare this program complete because the issue/PR board is empty.
> Call the loop closed only when evidence demonstrates all of these …
> 1. newly admitted knowledge becomes demonstrably reachable by future
>    governed decisions;

That is measurable today, without landing anything. It was measured on
2026-09-01, against the live Sensei the agent tooling actually queries.

## The measurement

PR #321 landed on `main` at 15:29Z, adding
`failure.sensei.a_decision_surface_reported_current_while_the_admitted_knowl…`.
Asked for it by exact id, scoped to this domain:

```
mode=by_id  id=failure_mode:failure.sensei.a_decision_surface_reported_current…
            domain=github.com/globulario/sensei

→ rows: []
```

**The knowledge is not there.** And the same response says:

```
state:                 current
verdict:               authoritative
authoritative:         true
graph_freshness_state: GRAPH_FRESHNESS_STATE_CURRENT
graph_build_commit:    ""            ← empty
source_repo_commit:    ""            ← empty
certified_awareness_graph_commit: b98c91eb…
graph_freshness_detail: "live graph marker 401d4bb737d1 and triple count
                         299036 match the expected artifact;
                         store content not compared"
```

`b98c91eb` **is not in this repository's history at all** — it is the services
repo's commit, which the same block also reports as
`certified_services_repo_commit`. So the graph serving this domain's questions
is certified against a revision from a different repository, names no build
commit of its own, and reports itself current.

## What this means for criterion 1

It fails, and not marginally. A governed decision made right now cannot see
knowledge that was admitted to `main` four hours earlier. The gap is not
latency: nothing in the path moves that knowledge, because publication is a
deliberate step that has not been taken.

**The honest reading is that #321 made the question askable and did not answer
it.** It built the reachability assessment — `current | stale | unknown`, with
`unknown` as a member rather than a fallback — but the live surface still
answers freshness from its own marker-and-count comparison, which the detail
string itself admits does not compare content.

By #321's own logic this graph is `Unknown`: `PublishedCommit == ""` is the
first case in `Assess`, and its detail is *"the serving graph does not state
which revision produced it."* The surface reports `current` instead.

## Why this is the blocker for criterion 5

Criterion 5 — *Sensei surfaces at least one previously learned relevant law
before a human or Codex reviewer points it out* — is the program's primary
objective, and it cannot be satisfied while criterion 1 fails. A law that is
not in the graph the tooling queries cannot be surfaced by it, however good the
retrieval is.

Every law learned today is in this position: the skip law, Non-Execution False
Green, the promotion-base distinction. They exist as source YAML in unlanded
branches, or as landed YAML that nothing has published. **Authored is not
reachable**, which was already recorded as a lesson and is now measured again
one level further along: *landed* is not reachable either.

## What would close it

Publication is an owner action and is deliberately not automated — "never
auto-publish from a branch" is a standing constraint, and it is the right one.
So this document does not propose automating it.

What it does propose is that the loop cannot be called closed on the strength
of merges. The measurement above takes about thirty seconds and is the only
thing that distinguishes *admitted* from *reachable*.
