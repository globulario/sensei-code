# Mediation ledger

The record of the **mediator's** interventions in the V2 campaign: the
direction-setting, interpretation refusals and stop decisions made by the
human who holds objective and merge authority, as distinct from the findings
of the independent model reviewer (recorded per encounter in the corpus as
`review_findings` with `review_provenance`) and from anything an agent did.

Why it exists. Every agent in the loop — architect, implementor, reviewer,
re-reviewer, executor — is a model. The one thing that was not a model was
the mediator: each time the loop was about to read a rule the convenient
way, promote a control to the missing specimen, call a gate met on part of
its chain, or widen a role because three workers reached for the same
import, the refusal came from here. Those refusals are value judgements with
a concrete trigger and a concrete outcome. They are the highest-value data
this campaign produced, and until this file they lived only in conversation.
V2's learned stages must learn from *these*, not only from which candidate
was accepted; and the way the mediator steps back is by making each of
these legible enough to be checked mechanically — which is what the
`became` column tracks.

Rules. One entry per intervention. `trigger` states what the loop proposed
or was about to do, in the loop's own terms. `decision` is the mediator's,
verbatim where possible. `law` names the constitutional law applied, or
"none stated" — a refusal with no nameable law is itself a finding. `became`
names the document, test, invariant or PR the decision turned into, or
"conversation only", which means it can still be lost. `confirmed` is false
until the mediator has read the entry; entries are authored by an agent from
the transcript and may be wrong about emphasis — never edit an entry
silently; append a correction naming it.

`ledger.jsonl` is the record; this file is its contract.
