# Multi-Agent Adversarial Collaboration Model

**Status:** governing implementation note for Sensei Code  
**Scope:** provider scheduling, architect/reviewer/worker roles, cross-agent handoff, governed mode, future simulation/counterexample agents

## 1. Decision

Sensei Code should not treat multiple AI providers as interchangeable copies of one coding agent.

The intended model is a small governed engineering organization in which agents receive **different jobs, different context, and different authority**. Their outputs are reconciled against Sensei, repository evidence, candidate identity, and proof. Agreement between models is not proof, and disagreement is not noise to be averaged away.

The central rule is:

> **Use agents asymmetrically. One agent proposes or implements; another attacks the result; another may search for counterexamples; Sensei remains architectural authority; Sensei Code coordinates the loop.**

This extends the role separation already defined in `docs/architecture.md`:

- Sensei owns architectural truth, admission, evidence semantics, and closure.
- Sensei Code owns orchestration and authority routing.
- architect/reviewer models make bounded recommendations inside delegated authority.
- workers mutate isolated candidates.
- the human owns authority that has not been delegated.

The new requirement is that Sensei Code should actively exploit **epistemic independence** between agents rather than merely fail over from one provider to another.

## 2. The organization model

The desired governed-mode topology is:

```text
HUMAN
  owns intent, policy, trust boundaries, undelegated authority
    |
    v
ARCHITECT MODEL
  interprets the task inside Sensei-certifiable architecture
  chooses bounded plan, proof obligations, and worker roles
    |
    v
SENSEI CODE
  orchestrates roles, candidates, evidence, budgets, and handoffs
    |
    +--------------------+-----------------------+
    |                    |                       |
    v                    v                       v
IMPLEMENTER          REVIEWER              COUNTEREXAMPLE HUNTER
builds candidate     attacks candidate      attacks behavioral claim
inside worktree      and proof              via alternate hypotheses,
                                             scenario/state exploration
    |                    |                       |
    +--------------------+-----------------------+
                         |
                         v
                 ARCHITECT RECONCILIATION
                         |
                         v
                     SENSEI
          audit / proof / admission / closure
                         |
                         v
                HUMAN only on a real
                 authority crossing
```

The roles are semantic. Provider names are configuration and may change.

## 3. Do not ask every agent the same question

A multi-agent system is weak if it does this:

```text
ask Claude: is this correct?
ask Codex:  is this correct?
ask agent C: is this correct?
majority says yes
```

That is three correlated opinions, not independent engineering evidence.

Sensei Code should instead assign asymmetric work.

### Implementer

Typical instruction shape:

```text
Find the causal mechanism.
Implement the smallest contract-preserving change.
Produce the exact candidate revision and required evidence.
Do not widen architectural authority.
```

### Reviewer

Typical instruction shape:

```text
Assume the candidate may be wrong even if tests are green.
Attack the diff and its proof.
Search for stale evidence, authority bypasses, races,
unbound identity, hidden scope expansion, and false-positive PASS states.
Return concrete findings tied to code/evidence.
```

### Counterexample hunter

Typical instruction shape:

```text
Assume the stated behavioral claim is incomplete.
Search adjacent state transitions and operation orderings.
Try interruption, restart, stale actors, retries, partitions,
concurrency, and other relevant scenario families.
Return reproducible counterexamples or bounded evidence that none
were found in the explored space.
```

### Architect

The architect does not vote between agents. It reconciles their claims against:

```text
Sensei contracts and invariants
repository facts
candidate revision and diff
proof obligations and receipts
simulation/runtime evidence
human-owned intent
```

If the disagreement is certifiable, the architect resolves it. If it crosses the authority boundary defined in `docs/architecture.md` section 7, it escalates to the human.

## 4. Epistemic independence is a product requirement

The reviewer should not automatically inherit the implementer's complete reasoning transcript.

Sharing all reasoning creates anchoring: the reviewer begins inside the implementer's assumptions and may reproduce the same blind spot.

The default should be **shared facts and laws, independent conclusions**.

A reviewer packet should normally contain:

```text
task identity
current graph generation and authority state
governing contracts and invariants
forbidden fixes and known failure modes
bounded plan or externally visible claim
exact candidate base and head revision
candidate diff
required proof obligations
proof/evidence receipts already produced
known scope and affected components
```

It should not require the implementer's private narrative or hidden chain of reasoning.

A counterexample agent should receive even less implementation framing when possible. It needs the property being claimed, the relevant architecture, the exact candidate, and the simulator/probe surface. Its job is to discover a path the implementation team did not anticipate.

A worker handoff is different from a review packet. When one implementer replaces another on the same task, continuity is valuable and the second worker should receive the prior scope, decisions, evidence, and open questions as already required by the architecture.

Therefore Sensei Code needs at least two distinct transfer concepts:

```text
WorkerHandoffPacket
  continuity-oriented
  preserves prior engineering decisions and progress

IndependentReviewPacket
  independence-oriented
  preserves facts, laws, candidate identity, and evidence
  does not require prior reasoning conclusions
```

Conflating these packets would weaken either continuity or review independence.

## 5. Agents are reasoning providers, not truth holders

Sensei Code must preserve the existing ownership map:

```text
Git / worktree receipts    -> code and candidate identity
Sensei                     -> architecture and governance authority
proof runners / simulator  -> behavioral and test evidence
Globular / runtime owners  -> runtime truth when integrated
AI providers               -> reasoning, implementation, review, hypotheses
human                      -> undelegated intent and architectural authority
```

An agent must never become authoritative merely because it is designated `architect`, `reviewer`, or `primary`.

The architect role is delegated authority only inside the region Sensei can certify, exactly as `docs/architecture.md` already defines.

Reviewer acceptance remains only a review verdict. It is not Sensei admission.

## 6. Cross-agent repair loop

The normal multi-agent governed loop should be able to execute this shape without human intervention:

```text
Sensei preflight
    |
    v
architect plan
    |
    v
implementer candidate
    |
    v
local validation + Sensei diff audit
    |
    v
independent reviewer
    |
    +--> ACCEPT ------------------------------+
    |                                         |
    +--> REVISE -> bounded implementer repair |
    |                                         |
    +--> ESCALATE -> architect ---------------+
                                              |
                                              v
                                  counterexample search
                                              |
                              +---------------+---------------+
                              |                               |
                        counterexample                    no finding
                              |                               |
                              v                               v
                     architect re-plan                proof closure
                              |                               |
                              +---------------+---------------+
                                              |
                                              v
                                         Sensei admission
```

Not every task needs every role. Rigor, blast radius, and proof obligations determine the required organization for that task.

For example:

```text
low-risk local refactor:
  architect -> worker -> reviewer -> Sensei checks

high-risk distributed authority change:
  architect -> worker -> reviewer -> counterexample hunter
  -> simulation proof -> architect reconciliation -> Sensei admission
```

The important rule is proportionality without collapsing independence.

## 7. Disagreement semantics

Sensei Code must never resolve disagreement by provider majority.

Use this ladder:

```text
agent disagreement
  -> identify the disputed claim
  -> classify its source: graph | repository | evidence | inference | human intent
  -> resolve factual claims from canonical sources where possible
  -> run additional proof when the dispute is behavioral
  -> ask another bounded agent only when it can add independent evidence
  -> architect resolves inside certifiable authority
  -> human only if authority/certifiability genuinely requires it
```

Examples:

- If reviewer says a function has two writers, inspect repository/Sensei evidence. Do not vote.
- If implementer says a scenario is impossible and counterexample agent produces a reproducible trace, the trace wins as evidence.
- If two agents propose incompatible architectures but both satisfy current contracts, the architect may choose within Level-2 authority.
- If the choice changes an invariant or human-owned intent, it is Level 3 and must reach the human.

## 8. Provider specialization and rotation

Provider-to-role assignment must be configurable and evidence-driven, not hard-coded as permanent truth.

The current architecture begins with a useful default:

```text
architect / reviewer: Codex or OpenAI
worker primary:        Claude Code
worker fallback:       Codex
```

That should remain a default, not a law.

Sensei Code should eventually be able to learn practical role performance such as:

```text
causal diagnosis success
candidate convergence rate
review findings later confirmed
false-positive review rate
counterexamples found
proof failures after reviewer ACCEPT
human interventions required
cost / latency / token use
```

This evidence may inform scheduling. It must not silently become architectural authority. Until a governed learning path exists, provider-performance data is operational scheduling input only.

The same model may occupy more than one role when necessary, but independent roles should use separate sessions and role-specific packets. Where a genuinely different provider is available, high-risk changes should prefer cross-provider review because it reduces shared-model correlation.

## 9. Implementation requirements for Sensei Code

The following should be treated as implementation requirements, not future UX polish.

### 9.1 First-class semantic roles

Represent roles explicitly rather than inferring them from provider name:

```text
architect
implementer
reviewer
counterexample_hunter
proof_runner
```

A provider adapter declares which roles/capabilities it can satisfy.

### 9.2 Role-specific context construction

The workflow engine must construct packets by role. Do not reuse one giant prompt for every agent.

At minimum implement distinct:

```text
ArchitectPacket
WorkerHandoffPacket
IndependentReviewPacket
CounterexamplePacket
```

All packets carry task/candidate/provenance identity appropriate to the role.

### 9.3 Independent provider sessions

A reviewer or counterexample hunter starts a new provider session by default. It must not accidentally continue the implementer's conversational state.

The session/event record should make this visible.

### 9.4 Structured verdicts

Review and counterexample output must be machine-readable and attributable.

Reviewer findings should identify:

```text
finding id
severity
claim being challenged
file/component/evidence reference
reason
required correction or proof gap
```

Counterexamples should identify, when available:

```text
property challenged
scenario / operation sequence
seed or reproduction identity
evidence
candidate revision
result
```

### 9.5 Architect reconciliation receipt

When agents disagree, record the architect's reconciliation as a durable task decision with:

```text
disputed claim
inputs considered
canonical evidence used
decision
authority level
remaining uncertainty / proof obligation
```

This is not a replacement for Sensei governance. It is the orchestration receipt explaining why the workflow took the next branch.

### 9.6 Bounded budgets and alternate agents

Each role receives a bounded attempt/revision budget. Exhausting one provider should lead to an alternate provider or architect resolution, not an automatic human interruption.

### 9.7 Event vocabulary

Add normalized events sufficient to reconstruct the multi-agent loop, for example:

```text
agent.role.assigned
agent.session.started
agent.session.finished
handoff.created
review.started
review.finding
review.completed
counterexample.search.started
counterexample.found
counterexample.search.completed
architect.reconciliation
```

Raw provider text remains evidence/debug output, not workflow truth.

### 9.8 Provenance on every cross-agent artifact

Every packet/verdict binds to the relevant identities:

```text
task id
graph generation / authority state
base revision
candidate revision or diff digest
proof-plan identity where applicable
provider / model / role
session id
timestamp
```

A review of candidate A must never be attached to candidate B after a worker revision.

## 10. Required tests

The implementation should eventually prove at least these properties:

1. A worker cannot mark its own candidate reviewer-accepted.
2. Reviewer acceptance does not manufacture Sensei admission.
3. A new candidate revision invalidates reviewer/probe verdicts that were bound to the previous candidate.
4. A reviewer session starts without inheriting the worker's conversational session state.
5. `IndependentReviewPacket` contains governing facts and candidate evidence but does not require worker reasoning transcript.
6. Worker fallback uses `WorkerHandoffPacket` and preserves task continuity.
7. A reviewer `REVISE` result produces bounded worker instructions and another independent review cycle.
8. A reviewer `ESCALATE` reaches the architect first, not the human directly.
9. Counterexample evidence can reopen a reviewer-accepted candidate.
10. Majority agreement between agents has no workflow authority by itself.
11. A high-risk task can require cross-provider review/counterexample roles by policy or rigor.
12. Provider failure triggers bounded fallback before human escalation.
13. Human escalation still occurs when Sensei cannot certify the architectural decision, regardless of agent agreement.
14. Every cross-agent artifact is rejected when task/candidate/provenance identity mismatches.

## 11. Motivating pattern

The desired behavior is the pattern already proving useful during current Globular/Sensei engineering:

```text
one agent implements or investigates
      |
      v
another agent reviews adversarially
      |
      v
concrete proof/authority holes are found
      |
      v
implementer repairs under Sensei constraints
      |
      v
review attacks the new candidate again
```

The lesson is not that one named model writes and another named model reviews. The lesson is that **independent roles produce stronger engineering than a single agent recursively approving its own work**.

Sensei Code should make that dynamic mechanical, repeatable, observable, and bounded.

## 12. Non-negotiable laws

1. **Agents specialize; they do not vote.**
2. **Shared facts and laws are required; shared conclusions are optional and often undesirable for review.**
3. **Implementer and reviewer are independent roles even when the same provider must fill both.**
4. **A provider session never becomes architectural authority by designation.**
5. **The implementer cannot certify its own candidate.**
6. **Reviewer acceptance is not Sensei admission.**
7. **Counterexample evidence outranks prior model confidence.**
8. **Sensei Code coordinates truth holders; it does not become one.**
9. **Human involvement is reserved for genuine authority/certifiability crossings, not ordinary agent disagreement.**
10. **Every cross-agent decision is bound to exact task, candidate, and evidence identity.**

The product goal is therefore not "many AIs working on code." It is a governed engineering organization in which independent AI roles can propose, challenge, falsify, repair, and prove work while Sensei preserves one architecture and the human remains the root of intent.