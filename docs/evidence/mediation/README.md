# Mediation ledger

The record of the **mediator's** interventions in the V2 campaign: the
direction-setting, interpretation refusals and stop/go decisions, as
distinct from the independent automated reviewer's findings (in the corpus
as `review_findings`) and from anything an implementing agent did.

Actors, never collapsed. The mediation reasoning in this campaign was
authored by **GPT-5.6 Sol** (architect, re-reviewer, experimental mediator)
in conversation; **Codex** reviewed PRs as a separate automated actor;
**Claude** implemented and reconstructed this ledger from the transcript;
the GitHub connector executed reviews and merges under the account
**davecourtois**, and davecourtois — the human project owner and operator —
set and refined the objective and holds ultimate authority. The first
draft of this file said "the one thing that was not a model was the
mediator". That was false: the ledger's own first entry failed the ledger's
provenance standard, and was caught before admission (review 5047630344).

Why it exists. Each time the loop was about to read a rule the convenient
way, promote a control to the missing specimen, call a gate met on part of
its chain, or widen a role because three workers reached for the same
import, the refusal came from the mediator. Those refusals are value judgements with
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
"conversation only", which means it can still be lost. `reasoning_author`
names who authored the decision; `confirmed_by` names who has read and
confirmed the entry, else the entry is unconfirmed. Entries are authored by
an agent from the transcript and may be wrong about emphasis — never edit an
entry silently; append a correction naming it.

`ledger.jsonl` is the record; this file is its contract.
