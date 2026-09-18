# The workspace-integrity boundary for governed local state

**Issue:** globulario/sensei-code#184
**Decided:** 2026-09-18, on landed main after Review Spine v1

Sensei-Code keeps governance-bearing records in the workspace. This states the
threat model they are kept under, inventories them, classifies what each one
re-derives versus trusts, and records the decision about whether to raise the
boundary.

---

## 1. The threat model, named before any cryptography

**Inside the boundary.** The local user account running the process. Every
governed record is written by one process, under one uid, with owner-only
permissions. That account is trusted completely: it holds the GitHub App private
key, the session store, the task state and the candidate worktrees.

**Outside the boundary.** Other local users on the same machine; any process not
running as that account; anything reaching the workspace over the network.

**Explicitly NOT defended against.** An attacker who already controls the
account, or the process, or a shell that can write to `.sensei-code/`. Such an
attacker can rewrite any record, and a signature or MAC would not change that,
because the key would have to be readable by the same account.

> A signature whose key is writable by the same compromised workspace does not
> raise the boundary. It relocates the question and makes the record look
> stronger than the authority inputs it depends on.

That is why this document names the model before choosing a mechanism, and why
the decision below is not "add cryptography".

---

## 2. Inventory of governance-bearing local records

| record | what it decides | re-derived on load | trusted as OBSERVED |
|---|---|---|---|
| `.sensei-code/reviews` (`reviewstore`) | which exact review answered a request | artifact reparsed, digest recomputed over stored bytes, request identity, standing, schema version, every evidence row validated | which authenticated GitHub principal posted, which comment id, which terminal principal relayed, that an App publication completed |
| `.sensei-code/exchanges` (`ReviewObligationStore`) | which review is still owed, for which candidate, from which provider, answerable by which account | task/request identity, candidate binding shape, reattachability, conversation reachability | that a request was published, its comment locator, the pinned reviewer principal, the pinned publisher |
| `.sensei-code/attestations` (`AttestationStore`) | an owner override of an unmet independent-review obligation | review digest, reviewer, candidate binding, decision vocabulary, schema, lifecycle state | which local terminal principal attested, that the App published the override |
| `.sensei-code/tasks` (`taskstate`) | task phase, evidence snapshot, open findings, owed obligations — what resume can discover | schema and field validity | the recorded phase transitions |
| session store (`internal/session`) | the event transcript resume reads | event schema; `FindInterrupted` is a projection recomputed every read | that the events occurred, in the recorded order |
| `workflow.owner_attestation` in local config | whether an owner override is permitted at all | nothing — it is a policy input | the owner set it |

### Classification rule

The repository's existing doctrine, from `internal/runreceipt`:

- **RE_DERIVABLE** — recompute at consumption. Digests, bindings, git object
  identity, request relationships, schema invariants, body parsing, provider
  equality against the durable obligation.
- **OBSERVED** — cannot be reconstructed after the event, so it lives inside the
  trusted writer boundary. Which authenticated principal supplied bytes; which
  local terminal performed an authority action; that a publication actually
  occurred; ordering where semantically required.

The rule is *keep the observed surface small*, not *make it authentic*. Review
Spine R2 applied it by removing live `publicationStands` re-validation from
consumption: re-contacting GitHub to prove a transport event a second time would
have made the original transport a perpetual oracle, so that an outage or a
deleted comment could retract a review that was already authenticated, bound,
validated and recorded.

---

## 3. What the inventory found

Every record was owner-write-only, so the **integrity** boundary was already
uniform. What was not uniform was **how** the records reached disk:

```text
reviews        0700/0600   temp file, then rename
attestations   0700/0600   temp file, then rename   (create: O_EXCL)
exchanges      0700/0600   WriteFile straight onto the target
task state     0755/0644   WriteFile straight onto the target
```

Two ad-hoc copies of one mechanism, and the two stores without it included the
**obligation owner** — the component R4 made the single authority on what review
is owed. A crash between truncate and write left a truncated obligation. R4
reads that as unreadable and fails closed, so it degraded safely; it was also
avoidable.

Task state was additionally the only governed record that was world-readable,
which made the modes misdescribe the boundary: a reader comparing `0600` on a
review with `0644` on the task state that points at it would conclude the two
were protected to different standards. They were not; only the writers differed.

---

## 4. Decision

**Retain the existing workspace-integrity boundary.** No signing, no MAC, no
hash-chained ledger, no TPM, no remote anchor.

The threat model does not support them. Every candidate mechanism would need a
key or an append point writable by the same account that already owns every
record, so none of them raises the boundary; each would raise the *appearance*
of the boundary, which is worse than leaving it stated plainly.

**Make the boundary coherent instead.** `internal/governedfile` now owns one
mechanism — the posture and the durability of a governed write — and all four
stores use it. This is a consolidation, not an addition: two duplicated
implementations and two absent ones became one.

It owns no semantic fact. Nothing gains authority by being written through it,
and it decides nothing about what the bytes mean.

### What would change this decision

- governed records shared between accounts or machines;
- a workspace readable by a service account that must not forge authority;
- an audit requirement for tamper-*evidence* rather than tamper-*resistance*
  — a hash-chained ledger is the cheapest answer there, and it would have to
  cover the whole authority chain, not reviews alone.

Any of those makes this a real threat-model change and warrants reopening the
question. Adding cryptography without one of them does not.

---

## 5. Acceptance criteria, answered

| criterion | where |
|---|---|
| one documented threat model | §1 |
| one inventory of authority/evidence stores | §2 |
| consistent RE_DERIVABLE vs OBSERVED classification | §2 |
| no store claims a stronger guarantee than its authority inputs | §3, §4 — the asymmetry is removed, and `TestNoGovernedStoreInventsItsOwnWritePosture` keeps it removed |
| hardening applies to the authority chain, not reviews in isolation | §4 — all four stores, enforced structurally |
