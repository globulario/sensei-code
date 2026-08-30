# J1-R result — the first complete governed revision cycle

J1-R, frozen at `288adf6`, completed under its one-invocation stopping rule.
Recorded before admission, merge, cleanup, or any further interpretation.

**The strongest evidence is not `COMPLETE / ACCEPTED`. It is that a naturally
occurring REVISE → T2 → ACCEPT cycle preserved identity across both states.**

## Result

```text
governed run receipt        COMPLETE / ACCEPTED        missing: none
schema                      sensei-code.governed-run-receipt/v6

plan_state                  PRESENT
candidate_state             PRESENT
candidate_digest_relation   MATCH
formatter_mutation          UNCHANGED

governor_commit             2d5b31c0082e22fa0b2de43dcf61367eca8d6992
base_commit                 2d5b31c0082e22fa0b2de43dcf61367eca8d6992

reviewed_digest             bf149d0c1755c7fe…
reviewed_tree               c1a208d7c62eb4e2…

candidate_commit            6016791c6ec72230…
candidate_tree              c1a208d7c62eb4e2…
candidate_first_parent      2d5b31c0082e22fa…
candidate_commit_diff       bf149d0c1755c7fe…
```

The candidate is **retained and unadmitted**. Nothing here makes it
authoritative or integrated.

## The identity reconstructs, by an independent path

Verified with `git hash-object` alone, outside the program that produced it:

```text
tree c1a208d7c62eb4e25158801440e1eda6e3c4e5b6
parent 2d5b31c0082e22fa0b2de43dcf61367eca8d6992
author/committer Sensei Code Candidate Identity <candidate-identity@sensei-code.invalid>
  <base commit time + 1> +0000
message derived only from (base, tree)

        -> 6016791c6ec722301c038b96f0268cdbf2190bc8      RECONSTRUCTS
```

No signature, no trust in the machine that minted it.

## The natural revision cycle

```text
attempt 0   codex   DELIVERED   revise   digest ea9e02ab0ed628d7…   tree 371dbeb95746ea83…
attempt 1   codex   DELIVERED   accept   digest bf149d0c1755c7fe…   tree c1a208d7c62eb4e2…
```

The first review did not request a cosmetic change. It refused the **proof**:

> "Neither new test invokes `runCandidate` … the required engine-level proof is
> missing." The tests hand-assembled the intended sequence rather than
> exercising the production path, so they established an adjacent property
> instead of the required end-to-end behaviour.

The worker revised implementation and proof. The second review accepted on the
merits: post-validation canonical recapture implemented, bundle/diff
disagreement refused, and mint-time independent remeasurement and base-parent
checks left unchanged.

## The twelve frozen criteria

```text
1   reviewer evaluated a real behavioural implementation            PASS
2   validation/audit correspond to the state submitted for review   PASS
3   verdict authorises only its exact tree/digest                   PASS
4   final C wraps the final accepting verdict's tree                PASS
5   C^1 remains the governed base B                                 PASS
6   COMPLETE remains meaningful with a bounded outcome              PASS
7   outcome requires no event-stream reconstruction                 PASS

8   T1 and T2 mechanically distinguishable                          PASS
9   the verdict over T1 did not authorise T2                        PASS
10  T2 received its own validation/audit/review                     PASS
11  C wraps the accepted Tn, never the earlier T1                   PASS
12  revision history preserved rather than overwritten              PASS
```

Criteria 8–12 had never been testable in this chain. They are answerable here
because a revision arose naturally, not because one was manufactured.

```text
T1 = tree 371dbeb95746ea83…  digest ea9e02ab0ed628d7…  verdict REVISE
T2 = tree c1a208d7c62eb4e2…  digest bf149d0c1755c7fe…  verdict ACCEPT

C.tree = T2      C^1 = B      Render(B,C) digest = T2's reviewed digest
```

The T1 verdict did not migrate onto T2. Both trees are preserved on disk:
`J1R.refused-T1.diff` (14,755 bytes) and `J1R.candidate-C.diff` (25,775 bytes).

## P1 activation

By the procedure frozen before execution, **measurement 2 applies**: a canonical
candidate was minted with `MATCH`, establishing that the stale-binding defect
**did not activate**. The independent instrumentation corroborates it —
`formatter_mutation: UNCHANGED`, measured across the format step rather than
inferred.

The old governor could certify the repaired candidate because validation did not
mutate candidate bytes on this run. **The pre-registered bootstrap boundary did
not occur, and remains untested.**

## What J1-R established

The governed loop, in one invocation: produced a real implementation →
independently reviewed exact T1 → received a substantive REVISE → revised →
separately validated and audited T2 → independently reviewed exact T2 →
received ACCEPT → minted a canonical C for exactly T2 → preserved T1 and its
refusal in the durable record.

It is also the first direct evidence in this chain that an autonomous worker can
receive an architectural criticism, modify its implementation **and its proof**
in response, and return a new exact candidate an independent reviewer accepts.

## What J1-R did NOT establish

* admission of `6016791c`
* integration into authoritative repository state
* X+1, or self-hosting
* regeneration of Sensei knowledge from X+1
* the bootstrap case where the old governor's defect prevents certification of
  its repaired successor
* behaviour when formatter mutation actually activates P1
* general autonomous engineering capability across repositories or task classes

## Timing

```text
start 07:25   finish 08:06   elapsed ~41m   budget 50m
```

The second cycle used the additional budget justified before execution. The
frozen `2 × 25m` denominator was materially relevant rather than decorative.

## The claim, frozen at this scope

> J1-R demonstrated one complete, naturally arising governed revision cycle over
> real behavioural code: an independent reviewer rejected T1 for an
> architectural/proof deficiency, the worker revised it, T2 received fresh
> validation and independent review, and the system minted a reconstructible
> canonical candidate whose tree and digest are exactly those of the accepting
> verdict while preserving the rejected T1 as history.

Candidate `6016791c…` is **evidence, not yet authority**.

The next question is not "merge because J1-R passed". It is admission: whether
this exact accepted candidate can cross from retained `C` into governed `X+1`
without losing the identity chain just proved.
