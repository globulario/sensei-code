# V2 Investigator Method v1 (V2-M1)

**Status:** method specification, revision 3. §2.1 added: a second candidate law, earned by
the Family A implementation rather than by an investigator run. UNCOMMITTED, for review and challenge.
**Date:** 2026-09-11
**Derived from:** the A0 false-negative autopsy, with B0 as control. Every rule cites the
evidence that earned it; no rule is included because it sounds prudent.
**Scope of change:** the investigative METHOD only. The intelligence layer is deliberately
NOT changed (§4).

---

## 0. What the experiment established

A0 and B0 received a byte-identical brief (one path token apart), had comparable budgets
(50 calls/10m13s vs 53/11m35s), and read the defective file to identical depth — once, in
full, via a single `cat`, neither reading its tests, neither executing it.

**The failure was not capability.** A0 demonstrably held the required repertoire:

- it reasoned fluently about guards that exist but are inert (its own Finding 4, complete
  with a mutation control: "delete the block … observe that NO test fails");
- it constructed adversarial, unrepresentable states in three other findings;
- it reached the truncation hypothesis that is now **globulario/sensei#352**;
- it named the exact missing `ConsumedAt` predicate that is finding F3.

It deployed none of these on the region it declared sound. **What failed was the protocol
deciding when and where to deploy abilities it already had.**

That is a much smaller problem than inventing investigator intelligence, and it is the only
thing V2-M1 changes.

---

## 1. The rules

Each rule states what it forbids or requires, the autopsy evidence that earned it, and — where
one exists — a MECHANICAL CHECK. A rule with no check is an aspiration; A0 violated an
explicit instruction and nothing detected it.

### M1-1. No negative certification by default

Output candidates, evidence, falsifiers and unresolved suspicions. Never emit "sound", "safe",
"correct", "closed", "no issue", "tight", "clean" or equivalents about a region, unless the
task EXPLICITLY requests a proof of absence — in which case a falsification procedure or
deterministic proof is required, not inspection.

> **Absence of a finding is absence of a finding. It is not evidence of correctness.**

*Earned by:* the single damaging output of A0 was one soundness sentence. The brief already
said "Do not pad. If you find nothing in a category, say nothing about it." A0 opened a
section titled "Two things I looked for and did not find" — and having opened a section that
must be filled, the only material available was the source comments. B0's report contains
**zero** soundness claims anywhere.

*Check:* grep the report for the soundness vocabulary. Any hit that is not inside an
explicitly requested absence-proof is a method violation, independent of whether the claim
happens to be true.

### M1-2. Comments and repair narratives are hypotheses, never evidence

A comment asserting a property RAISES the obligation to verify it, never discharges it. A scar
narrating a past repair is evidence that the class occurred here, not that it is closed.

*Earned by:* all three clauses of A0's all-clear are near-verbatim restatements of comments in
the files it read. Worse, the comment at `lifecycleaction.go:120-126` narrates this exact bug
as already fixed — the defect had been reintroduced one step earlier than the step the scar
describes. **A0 read a repair narrative and took it as evidence of the repair's adequacy.**

*Check:* no finding — and no decision not to report a finding — may cite a comment as its sole
support. Every claim quotes executable code.

### M1-3. Finite state spaces are reduced to concrete tuples before any verdict

Before claiming anything about a selector, validator or state transition, enumerate the
dimensions that control it and look for uncovered combinations.

*Earned by:* F1 and F2 fall out of one cell — `contradiction × TypedProtocol=false`. The
corpus's own test file sets `TypedProtocol: true` in every contradiction case, including a
test named `TestContradictionIsRejectedBeforeEarlierBranches`. **Execution was not required**;
the cost of catching both was one unopened file.

*Check [R2 — corrected].* "Producible on request" recreates a proof after the reasoning has
already happened. The enumeration is part of the RUN RECORD whenever the rule triggers,
written before the verdict.

Exhaustive Cartesian enumeration is not required when the space is large: a dimension
inventory plus equivalence partitions and boundary cells suffices. What the rule forbids is
claiming "I considered the combinations" with no artifact showing which.

### M1-4. A falsifier must be pursued when reasonably cheap

Read-only means PRESERVE THE BENCHMARK, not prohibit experimentation. Copy to scratch,
construct the state, run it, then prove the original untouched.

*Earned by:* A0 identified a discriminating experiment, declined it, and named its reason —
"the corpus is read-only" — misreading a brief that explicitly permitted `go test` and `/tmp`
scratch. B0 resolved the identical sentence by relocation (`cp -a`, then a tool call proving
the original unmodified).

**FACTUAL CORRECTION [R2].** An earlier draft said "the declined experiment is #352". It is
not, and the error is instructive. A0's declined experiment deletes the consumption ARTIFACT,
so `LoadRecordedConsumption` errors and a CLI guard falls through. #352 deletes the tail
ENTRY, so the chain still verifies and `foldGovernance` sees absence. Same outcome class —
a spent capability becomes grantable — but different mechanisms in different components.
Merging them was a reconstruction error introduced by the autopsy and repeated before being
caught against the raw report. **A0's path remains unreproduced and is not covered by #352.**

*Check [R2 — strengthened].* The trigger is the EXISTENCE of the experiment, not how
confidence was narrated; the earlier check could be evaded by simply not lowering confidence.

> If the investigator identifies a reasonably cheap experiment whose result would materially
> distinguish a hypothesis, it MUST either execute it, or move the item to `UNRESOLVED` with a
> recorded reason for non-execution.

**"Reasonably cheap" is operational, not aspirational:** it fits the remaining experiment
budget, and it requires no authority outside the sandbox. Without that bound the rule demands
infinite experimentation. "Read-only" is never a valid reason for non-execution.

### M1-5. Confidence cannot discharge evidence debt

Confidence ranks what to investigate next. It never converts an unresolved item into a
reported one.

*Earned by:* A0 wrote "medium … which I established by reading rather than by executing …
The falsifier settles it" and shipped the finding. The uncertainty was *recorded*, which feels
like discharge, and the item still shipped. The brief already said confidence "RANKS ATTENTION
ONLY and is never evidence."

*Check:* any finding containing "would settle", "the falsifier settles it" or equivalent, for
an experiment not performed, belongs in the unresolved list — not the findings list.

### M1-6. The reference implementation is not exempt from suspicion

If one component is used as the standard against which another is judged, its own predicates
remain independently challengeable.

*Earned by:* **N1, reference-standard capture** — the largest unanticipated mechanism. A0
promoted the governance-read side to yardstick ("the acting read path does not verify digests;
*the read-only projection does*"), and a component used as a measuring instrument is
structurally exempt from being a defect site. This produced the F3 near-miss: A0 named the
exact missing `ConsumedAt` relation and **assigned it to the wrong owner**, because the right
owner had become the standard of correctness.

*Check [R2 — sharpened to the predicate].* A component-level rule is mechanically satisfiable
while the relied-upon predicate stays immune — which is exactly what happened: `consumptionBinds`
appeared throughout A0's report, and its TEMPORAL predicate was never challenged.

> Any PREDICATE OR RELATION used as a positive control must itself appear in the challenge
> coverage, under its owning component.

That targets `consumptionBinds`' expiry relation directly, rather than settling for its package
appearing somewhere in coverage.

### M1-7. Evidence scope is tracked mechanically

Every evidence hop is tagged `IN_SCOPE`, `DEPENDENCY_EVIDENCE` or `OUT_OF_SCOPE`. Following a
dependency is allowed. Silently enlarging the search universe is not.

*Earned by:* both runs left the pre-registered scope — A0 for 3 of 7 findings, B0 similarly.
Neither disclosed it. The consequence is that the benchmark denominator cannot be used
honestly: neither run searched the universe that was fixed in advance.

*Check:* the tags are present, and the set of files touched is reconcilable against the
declared scope.

### M1-8. Claim validity and supporting-evidence validity are separate

A correct conclusion supported by a false supporting assertion has an evidence defect, and the
defect is reportable even though the conclusion stands.

*Earned by:* A0's Finding 5 conclusion is correct and independently confirmed, while its
supporting claim — that its site is the only risk-class use of the token — is false (73 other
occurrences). **Conclusion supported ≠ every supporting assertion valid.**

*Check:* each factual support is independently falsifiable and separately marked.

---

## 2. The consequence for Sensei itself

M1-6 generalises beyond the investigator, and this is the part with the longest reach:

> **Ownership answers "who defines this contract?" It does not answer "whose implementation
> may be assumed correct?"**

An owner is the authority over a PREDICATE'S DEFINITION. It is not an epistemic authority over
its own correctness. If the awareness graph lets ownership confer assumed-correctness, then
architectural ownership silently becomes immunity from investigation — and the component most
load-bearing in the design becomes the least examined. A0 demonstrated the failure in miniature
in ten minutes.

This is a candidate law for the graph itself, not only for V2. It is recorded here and NOT
proposed into the graph, because promoting it is a separate authorized act.

---

## 2.1 A second candidate law, earned by Family A `[R3]`

M1-6 was promoted to a candidate law for Sensei itself (§2) from investigator behaviour.
**M1-4 has now earned the same status from a different direction: production implementation,
under concurrency, repairing protocol semantics.**

### What happened

The Family A repair introduced a concurrency defect. Three approaches failed to find it, and
none of them was foolish — each interrogated a STORY ABOUT THE MECHANISM:

```text
isolation runs      asked: does contention reproduce it?        (isolation REMOVES contention)
the baseline        asked: did the candidate introduce it?      (measured a build failure)
the lock hypothesis asked: does serialization scope explain it? (did not fit the facts)
```

Every one could return reassuring evidence while leaving the actual property unmeasured. The
instrument that worked asked something different: **under the conditions in which this failed,
does the system preserve the entry/witness ordering invariant?** It did not care which
mechanism was believed. It made reality answer, and named the codes in 1.2 seconds.

### The candidate law

> **Falsify the observable contract, not only the hypothesised mechanism.**
>
> When a defect is defined by an externally observable invariant, closure evidence must include
> an experiment capable of falsifying THAT INVARIANT under the relevant conditions.
> Mechanism-specific tests may explain the defect and narrow the search, but they cannot
> substitute for testing the property whose violation defines the defect.

And its second half, which Family A demonstrated directly:

> **A repair is subject to the same law as the system it repairs.**

The witness repair was written around the law that directional disagreement carries evidence
and normalisation destroys it. Its own verifier then read stale entries against fresh witness
state and constructed a false disagreement. **The contract became a diagnostic instrument for
its own implementation** — a stronger result than "the tests helped".

### The epistemic brake

One episode does not prove M1-4 is universally applicable, and this section does not claim it.
What the episode DOES falsify is the weaker reading — that M1-4 is investigator or benchmark
hygiene. It produced value during production implementation, under concurrency, on protocol
semantics: a materially different environment from the one it was derived in.

**NOT PROMOTED.** Recorded here as a candidate, exactly as M1-6 is. Promotion into the
awareness graph is a separate authorized act, and performing it because the law looks good
would be the same shortcut this method exists to block.

---

## 3. What V2-M1 does NOT change

The intelligence layer is untouched. No prompt engineering of the reasoning, no added
heuristics about where defects live, no seeded knowledge of F1-F3 or G1-G3.

*Rationale:* if both the method and the reasoning changed at once, a later improvement could
not be attributed. And the autopsy establishes the repertoire was already present — so
changing it would be treating a symptom nobody has evidence for.

---

## 4. Evaluating V2-M1 — and a corpus problem

**Corpora A and B are burned for this purpose.** V2-M1 was derived from A0's failure on corpus
A. Measuring V2-M1 on corpus A is training on the test set; B is contaminated by the same
autopsy having inspected it as control.

New blind material is required. The strongest available source is the PR #351 review history,
which produced additional frozen heads each carrying an INDEPENDENT answer key — findings from
a reviewer that is not the evaluator:

```text
e809e2e8   reviewed 2026-09-10T15:49Z
0a43c29c   reviewed 2026-09-10T16:00Z
0f153fbf   reviewed 2026-09-10T16:45Z   4 inline findings
bd119701   reviewed 2026-09-10T17:37Z   4 inline findings
```

Each is a frozen revision with defects that were real, undescribed at that revision, and
labelled by an independent party rather than by this evaluator. That is better evidence than
a corpus whose key I wrote.

**Two caveats that must be recorded before use:**

1. **Priming is monotonic along the staircase.** Each later head contains the comments and
   scars of every earlier repair, so a run against a later head is primed toward the classes
   already fixed. The earliest head is the cleanest benchmark.
2. **The keys are of mixed quality.** The G-round review included a reachability argument that
   was wrong (the cited `foldGovernance` path was not attainable). A key item may be a real
   boundary defect with an unsound exploit story, and scoring must distinguish "found the
   mechanism" from "reproduced the reviewer's narrative".

3. **Historical-head isolation `[R2 — the load-bearing caveat]`.** A frozen git head is NOT a
   frozen benchmark. Repository bytes are only one channel; the KNOWLEDGE ENVIRONMENT is the
   other, and it is the one that silently carries the answer.

   > The investigator may consume only knowledge available AT the benchmark subject, or an
   > explicitly enumerated subset proven not to contain the hidden findings or their later
   > repair scars. Current graph state, later review comments, later failure-mode entries, and
   > post-subject generated artifacts are excluded unless deliberately classified as benchmark
   > inputs.

   Without this, `e809e2e8` looks pristine while the investigator quietly carries September 11
   institutional memory into September 10 code. Note that corpora A and B satisfied this only
   by accident — `git archive` stripped history, and the awareness graph inside the tree was
   the one contemporaneous with the subject. That was not a designed control, and it must
   become one.

---

## 4.1 Scoring protocol `[R2 — new]`

M1-7 fixes evidence accounting; it does not fix scoring. Once scope expansion is permitted and
disclosed, a broad investigator could improve its apparent benchmark score simply by searching
a larger universe. Recall and discovery are therefore scored on SEPARATE AXES:

```text
AXIS 1  answer-key recall      computed over IN_SCOPE investigation ONLY
AXIS 2  novel discovery        findings reached via DEPENDENCY_EVIDENCE or OUT_OF_SCOPE,
                               scored separately and never folded into recall
```

And each key item is scored on three independent questions, because agreement with a reviewer
is not ground truth:

```text
mechanism discovered          ← the investigator quality signal
exploit / reachability reproduced   ← the investigator quality signal
reviewer's narrative reproduced     ← NOT a quality signal; recorded only
```

Only the first two count. The G-round review shipped a mechanism that was real with a
reachability story that was false; an investigator that reproduced the narrative would have
been reproducing an error.

---

## 5. Standing status of the experiment

```text
V2-A0     frozen, unmodified. 0/3 on corpus A. One false all-clear.
V2-B0     frozen, unmodified. 1/3 on corpus B (discounted: the corpus primes that class).
          Executed its findings. Zero soundness claims. Produced #352.
V2-M1     this document. Method only. NOT YET RUN.
#352      tracked, OPEN, reproduced at 63e25c66. No repair authorized.
          Covers the ENTRY-deletion path only.
A0-F6     a SECOND capability-resurrection path (consumption-ARTIFACT deletion → CLI guard
          fall-through), identified by A0, never executed by anyone, not covered by #352.
          Unreproduced. Recorded here so it is not lost.
```

**The result that matters is not the answer-key score.** Rediscovery measures recall against
what is already known. #352 was in neither key, was missed by four rounds of independent
review, and is a live violation of a core safety property of the shipping system. That is
evidence of novel architectural discovery, which is the capability an investigator exists for
— and it survived the same run that produced a false all-clear ten minutes earlier.

> The investigator is not yet trustworthy as a JUDGE. It has demonstrated value as a DEFECT
> HUNTER. The remaining engineering problem is turning an existing reasoning repertoire into a
> stable scientific method — which is considerably smaller than inventing the repertoire.
