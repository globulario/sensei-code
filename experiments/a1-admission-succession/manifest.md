# A1 — admission and succession: can C become X+1?

**DRAFT FOR REVIEW. Nothing acts on `6016791c` until this is approved.**

Every prior experiment asked whether a candidate could be *known*. This one asks
who may make it *the next world*.

## The subject

```text
C                    6016791c6ec722301c038b96f0268cdbf2190bc8
C^1                  2d5b31c0082e22fa0b2de43dcf61367eca8d6992   (= B = governor)
C^{tree}             c1a208d7c62eb4e25158801440e1eda6e3c4e5b6   (= accepted T2)
reconstructs from    (B, T) by git hash-object alone             VERIFIED
custody              refs/evidence/j1r/preserved-candidate       evidence only
                     no branch contains it; not pushed; not in an admitted namespace
provenance           J1-R: codex REVISE on T1, worker revision, codex ACCEPT on T2
```

**What C actually changes** (2 files, +561/-3): it rebinds the candidate capture
to the post-validation state — `capture, diff = reviewed, reviewed.Diff` — and
adds `certifiedAgainstCapture`, which fails closed when the validation bundle
and the returned diff disagree with the recapture, "rather than let the
disagreement surface as a movement refusal at the mint, which names the wrong
cause."

**C repairs P1: the defect in the governor that judged it.**

## Policy field, recorded rather than inferred

```text
ARCHITECTURE = B      admission binds C; integration produces M;
                      succession is proven on M
```

Three identities, so no single Git object is asked to mean three things:

```text
C      the exact object that passed candidate admission
M      the ordinary integration result incorporating C into current history
X+1    M, and only after the successor proofs succeed
```

**Invariant: admission of C does NOT imply admission of M.** M's tree was never
reviewed as a whole. M must satisfy the result-transition, graph-binding and
next-run-origin proofs independently.

## The nine criteria

```text
ADMISSION — does authority attach to the exact object?
1  admission acts on exactly C, not a reconstructed equivalent
2  the acceptance receipt does not itself grant admission authority
3  the admission decision names exact C
4  no rebase, squash or rewrite substitutes C

INTEGRATION — is the relation proved rather than assumed?
5  the resulting authoritative repository state is READ BACK, not inferred
6  if X+1 == C, prove it directly
7  if X+1 != C, prove the explicit integration relation X+1 -> C
   (for ARCHITECTURE = B: C is byte-identical and reachable from M)

SUCCESSION — is it a successor, or an artifact on a shelf?
8  regenerated authoritative knowledge describes the actual resulting X+1,
   not C, not B, not a convenient intermediate
9  the next governed execution demonstrably STARTS FROM that exact X+1
```

Criteria 1–7 establish that a candidate was legitimately admitted. **8 and 9
establish causal succession.** Without 8 the graph is a historical report;
without 9 the result is an artifact on a shelf. With both, one governed
iteration's result becomes the next iteration's independently grounded premise.

## Named blockers, before anyone discovers them mid-run

**Criterion 8 is currently blocked by an operational boundary.** `sensei
rebuild` refuses to replace a live store of 298,957 triples with a self-only
build of 2,041, calling it a likely topology mismatch. That refusal is correct.
`--force` is **forbidden** here: overwriting a combined deployment to publish a
self-change's own knowledge is precisely the act the stop boundaries exist to
prevent. Criterion 8 therefore requires the deployment owner, not this session.

**Criterion 9 requires building a governor from M and running it.** That is
feasible and costs one governed run.

**Main has moved.** `C^1` is B, and the working branch carries later commits, so
`X+1 == C` (architecture A) would require rewinding real work. This is the
concrete reason B was chosen, not a preference.

## Forbidden

```text
- rebase, squash, amend or any rewrite of C
- forcing the graph rebuild past its topology refusal
- treating the J1-R acceptance receipt as an admission decision
- treating admission of C as admission of M
- letting the candidate certify itself, or supply its own admission criteria
- declaring succession from a merged diff without criteria 8 and 9
```

## Stopping rule

Each criterion is answered by a measurement recorded before interpretation. A
criterion that cannot be measured is **UNKNOWN**, never assumed. A blocked
criterion is recorded as blocked, with the boundary named. Partial success is
reported as partial: "admitted but not succeeded" is a real and useful outcome.

## Open decisions for the owner

```text
1  the admitted-ref namespace: refs/sensei-code/admitted/<n>? something else?
2  who performs the integration — and does M go through a PR with its own gates?
3  does criterion 8 wait for a deployment-owner rebuild, or is A1 scoped to 1-7
   plus 9 with 8 recorded as blocked?
4  if the next governed run under X+1 behaves differently because P1 is repaired,
   is that evidence FOR succession, or a confound to control?
```

Nothing here acts. This is the design, brought back for inspection before any
admission or integration action touches `6016791c`.

---

# The four decisions, resolved by the owner and FROZEN

## 1. Admitted ref namespace

```text
refs/sensei-code/admitted/a1/6016791c6ec722301c038b96f0268cdbf2190bc8
```

One-shot and immutable. Not `accepted`, not `candidate`, not a movable ordinal:
**the full object ID makes the ref itself incapable of quietly meaning a
different C later.** `refs/evidence/j1r/preserved-candidate` remains custody
only.

## 2. Integration goes through an ordinary PR with its own gates

The candidate-producing worker gets **no special merge authority** because C was
admitted. No squash, rebase or cherry-pick substitution of C.

> **M is the authoritative main tip READ BACK AFTER the PR is merged, not the PR
> head before merge.**

That closes a sneaky identity split where one integration commit is reviewed and
another is minted at merge time. If main moves during the PR, the gates must
cover the final integration state before M is accepted.

**Structural consequence, recorded now:** `C^1` is `2d5b31c`, which lives on the
working branch and not on main. C therefore **cannot be integrated in
isolation** — merging it necessarily integrates the branch up to its parent. M
will contain more than C. That does not weaken criterion 7 (byte-identity and
reachability still hold), but it must not be described later as "C was merged".

## 3. Criterion 8 is NOT scoped out

Redefining A1 as "1–7 plus 9" because 8 is operationally inconvenient would
weaken the experiment at exactly the point it becomes interesting. The status
model is:

```text
1-7  pass
8    blocked
9    not yet canonical
-----------------------------
ADMITTED / INTEGRATED
SUCCESSION BLOCKED
```

**Criterion 9 occurs AFTER criterion 8, not before.** The intended sequence is
admission → regenerated self-knowledge → next iteration. A governor built from M
may be run earlier as a **pre-succession probe**; that is a diagnostic and is
explicitly **not** criterion 9. Only the governed run that follows the deployment
owner's safe publication of authoritative knowledge for M is canonical.

The topology refusal is doing its job. **Do not `--force`.**

## 4. P1 differential behaviour: a predeclared witness, never an admission proof

> **P1 differential behaviour is a predeclared positive witness of causal
> succession under criterion 9, not an admission proof. Exact M and binary
> identity remain the primary origin proof.**

Two different things are being evidenced:

```text
object identity   proves WHICH governor ran
P1 behaviour      proves the semantic change carried by C is ALIVE
```

Predicted outcomes, frozen before the probe and not reinterpreted afterwards:

```text
under B      the validation-bundle / recaptured-diff disagreement falls through
             toward a movement refusal at the mint -> WRONG causal attribution

under M      certifiedAgainstCapture catches the disagreement and fails closed at
             capture certification -> CORRECT causal attribution
```

If M produces the repaired behaviour, that is positive causal evidence the
successor embodies C's changed governance behaviour rather than merely pointing
at a new SHA. If it produces B's behaviour, that is a serious contradiction. If
circumstances make the comparison ambiguous, it is **UNKNOWN**.

**P1 must never certify C's admission retrospectively.** C repaired the governor
that judged it; using the repaired behaviour to justify its own admission would
close the forbidden self-certification loop. Its evidentiary role begins only
after independent admission and integration.

## Authorisation

Admission of C is the owner's decision, given explicitly and relayed into this
session. It is **not** derived from the J1-R acceptance receipt, which grants no
admission authority (criterion 2).

Approved scope: proceed through admission and integration. **Stop at the
criterion-8 deployment boundary.** Do not force it, and do not declare
succession until 8 and then canonical 9 both pass.
