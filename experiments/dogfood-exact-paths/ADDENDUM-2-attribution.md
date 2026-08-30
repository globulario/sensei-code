# Addendum 2: how P1 activation will be MEASURED

Written before the run's result is known, because a classification procedure
invented after seeing an outcome is not a procedure.

Editing Go files is not evidence that P1 fired. Activation must be measured.

## The discriminator

```text
P1 activates only if validation actually MUTATED candidate bytes after the
capture froze T. Then D (post-format) describes content that the bound
T (pre-format) does not.
```

Three measurements, in order of strength:

```text
1  MINT REFUSAL naming two trees
   "the candidate moved after it was reviewed: the verdict's envelope named
    tree X and the worktree now holds Y"
   -> P1 ACTIVATED. Direct observation of the inconsistency.

2  RECEIPT relation
   candidate_digest_relation == MATCH, with a minted C
   -> P1 DID NOT ACTIVATE. D and T described the same content, which is
      only possible if nothing mutated between capture and binding.

3  VALIDATION evidence
   the validation record showing whether a format step rewrote files, read
   from the run's own validation evidence rather than inferred from the
   file types involved.
```

Measurement 3 is the one to consult when 1 and 2 are both absent — for example
if the run defers or fails before minting.

## The attribution tree, fixed in advance

```text
did validation mutate candidate bytes?
├─ NO   P1 did not activate  ->  score Task A against its nine criteria normally
└─ YES  P1 activated
        did a review complete before the mint?
        ├─ NO   little or no judgement evidence
        └─ YES  the reviewer's SEMANTIC judgement remains interesting,
                but every exact-identity claim is contaminated: the verdict
                was returned over a diff whose accompanying T is stale
```

A P1-triggered Task A is therefore classified:

> **useful behavioural observation, INVALID exact-identity specimen.**

Not "the experiment failed", and not creditable toward any J1-style criterion
about T1 -> T2 identity.

## The decision table for what follows

```text
Task A does not activate P1
    score Task A normally; run J1 as frozen

Task A activates P1 and exposes the known mint failure
    record it as an INCIDENTAL reproduction of J1's subject
    do NOT count it as J1
    still run frozen J1 deliberately

Task A exposes a NEW defect
    preserve it; determine whether it structurally prevents J1
    do NOT silently alter J1's falsifiers
```

## A trap already visible in J1, recorded here so it is not a surprise

J1 asks the loop to repair P1 while running under a governor that CONTAINS P1.
A plausible outcome:

```text
worker repairs P1 in the candidate
-> validation proves the repair
-> reviewer accepts the repaired candidate
-> the OLD governor binds a stale T
-> the OLD governor's mint refuses its own replacement
```

That would not be a judgement failure. It is a **bootstrap boundary**: the
candidate repaired the mechanism, and the defective mechanism could not certify
its replacement. It is the same shape self-hosting must eventually cross —

```text
X contains defect F; candidate X' repairs F; certification of X' is executed by X
```

— without letting a candidate self-certify. If J1 lands there, that is
architecture, not a failed run.
