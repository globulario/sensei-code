# Where we actually are against V2_ARCHITECTURE_DIRECTION

Assessed against `globulario/sensei/docs/architecture/V2_ARCHITECTURE_DIRECTION.md`.

## The short answer

We built a **§6.2-shaped output** produced by a **§5-shaped investigator**, and
we validated the **§4.1 role separation** — with an LLM standing in the
investigator's seat rather than a learned model. That is the right order:
the socket exists before anything is trained.

But we are **not at Section 6**. Section 6 is the learned layer. We are at
**Phase B**, building the deterministic and heuristic machinery that Phase C
would later rank and Phase D would later generate into.

And two of the doc's own requirements say things about this campaign that we
have not been saying.

## What lines up, precisely

**§6.2 permitted outputs** lists *candidate evidence request*. A recipe is
exactly that: a request that evidence be gathered, not a claim that anything is
so. The recipe writer emits a permitted output class and nothing else.

**§4 pipeline position.**

```
deterministic extraction → unknowns → v2 inference
→ candidate questions / evidence requests → deterministic evidence checks
→ governed review
```

The closure round sits exactly there: it runs *after* the graph reports a gap
and *before* anything is promoted, and its output feeds `sensei derive`, which
is the deterministic evidence check.

**§4.1 three roles, uncollapsed.**

| role | who |
|---|---|
| deterministic substrate | `sensei derive` — decides what is so |
| learned investigator | the closure round — decides where to look |
| governed authority | a human — decides what ought to be |

**Law 5 — "a candidate is not a canonical claim."** A recipe is weaker than a
candidate: it is a *question*, and it becomes coverage only through a derivation
the author does not control.

**§11 stop boundary — "optimizing benchmark scores by weakening Unknown or
fail-closed behavior."** This one is worth dwelling on. The tempting fix for our
refusals was to stop escalating on "no anchored rules apply". **The design
forbids exactly that move**, and the recipe path avoids it: it adds investigative
capability rather than removing a stop.

## A correction I owe to my own earlier analysis

I described "no anchored rules apply → escalate" as *the absence of governance
reported as a finding*, implying it was simply wrong.

**Law 1 is "Absence is not safety."** By that law, refusing to treat an unruled
region as safe is correct, and Sensei was obeying it.

The real defect is narrower and sharper: absence should route to the
**investigator**, not to the **human**. §3's objective is to *"generate
evidence-grounded architectural questions from the residual uncertainty left
after deterministic analysis"* — residual uncertainty is the investigator's
input, not a human's inbox. Escalating it is a **routing error**, not a law
violation, and it is the error the recipe writer addresses.

## Two things the design says that we have not been saying

### 1. §8.3 — this campaign cannot validate v2, however it comes out

> **V2 cannot be considered validated while Sensei remains its primary proving
> ground.** The decisive test requires repositories not designed by the Sensei
> architect; maintainers unfamiliar with the ontology; agents without prior
> project-specific instructions.

The 11-task corpus is drawn from **sensei-code's own git history**, oracles
written by the same author, run against the graph that describes it. Every task
fails all three conditions.

This does not make the benchmark worthless — it measures RAW vs COLD on real
tasks with behavioural contracts, which is a genuine result. But it is a
**Phase A instrument**, not a Phase F validation, and it should stop being
described as though a good result would establish the thesis.

### 2. §6.3 — the recipe writer owes an inference receipt it does not produce

> Every inference run must produce an immutable receipt containing: model
> identity and version; model artifact digest; feature-extractor version; input
> graph digest; inference configuration; output candidate digests; timestamp;
> resource limits; deterministic post-processing version; any nondeterminism
> declaration.

A closure round proposing a recipe **is an inference run**. Its provenance
records `origin_task`, `origin_gap`, `region`, `written_at`, `written_by`. It
does **not** record:

- model identity and version — which provider proposed the question
- input graph digest — what the graph held when it was asked
- output candidate digest — the recipe's own content hash
- nondeterminism declaration — an LLM is nondeterministic and says so nowhere

Concretely fixable, small, and it should be fixed before the cold-start
experiment, so the experiment's own outputs are receipt-bound.

## A scoping question worth raising

The V2 document lives in **`globulario/sensei`** and describes the graph
substrate. The closure round, the recipe writer and the benchmark all live in
**`globulario/sensei-code`**, which is a *consumer* of that substrate.

So the investigator we just built is a **per-consumer instance of something the
design places in the substrate**. `sensei derive` is a sensei capability; the
question generator is currently a sensei-code capability. Any other consumer of
the graph gets none of it.

That may be the right staging — prove the socket in one consumer first — but it
is a decision, not an accident, and it should be recorded as one.

## Phase A status, honestly

| Phase A requirement | state |
|---|---|
| measure deterministic extraction coverage | **partial** — 73/97 files (75%) measured ad hoc, not instrumented |
| record unresolved candidate classes | **partial** — `contract_unknown` candidates exist |
| capture architect questions and review outcomes | **yes** — authority receipts; 10 name one identical condition |
| identify recurring unknown patterns | **yes** — "no anchored rules apply" is the recurring one |
| establish external repository evaluation fixtures | **NO** |
| measure governance and review cost | **yes, best-instrumented** — proofbench: wall time, refusal rate, RAW vs COLD |

Roughly two-thirds, and done as one-off investigation rather than standing
instrumentation. The missing item is the same one §8.3 names.

## Does finalizing the design fix what we hit?

**Diagnosed by the design and fixed by building it:**

- *coverage gaps* → §5/§6.2 candidate evidence requests. The recipe writer is a
  hand-built instance of exactly this.
- *escalating on absence* → §4's pipeline sends residual uncertainty to the
  investigator. A routing change, not a new mechanism.

**Named by the design but NOT fixed by finishing it:**

- *`promote` validates form, not evidence.* §5.1 asks *"Which claim is supported
  only by evidence from the same source that asserted it?"* — that is precisely
  the C-case that promote accepted. The design **names the question**; nothing
  answers it. This is Phase B work on the v1 substrate and it is a live defect
  today, not a v2 feature.
- *external validation* → §8.3. No amount of design work substitutes for other
  people's repositories.
- *task truth vs system truth* → §8.2 lists *prevented architectural mistakes*
  as a metric. That is the architectural oracle we discussed. The design has it;
  we do not.

**So: the design is not the bottleneck.** It correctly anticipated every problem
we hit, including the one we were tempted to solve the wrong way. What is
missing is Phase A instrumentation, one Phase B substrate check, and an external
proving ground.

## Recommended order

1. **Inference receipts on the recipe writer** (§6.3). Small, and it makes the
   next experiment's outputs receipt-bound rather than retrofitted.
2. **The cold-start experiment**, outside the 11-task corpus — this is Phase A
   *and* Phase B evidence, and it is the first measurement of whether the
   investigator socket produces anything.
3. **The self-evidence check** (§5.1) against `promote` — a deterministic
   adversarial generator that refuses a claim whose evidence cites only what the
   claim introduced. This closes a demonstrated hole in v1.
4. **An external repository fixture** (§8.3), before any claim that Sensei works
   is made outside this room.

Training a model is not on this list, and by §10 it should not be until Phase B
has produced the candidates a Phase C model would rank.
