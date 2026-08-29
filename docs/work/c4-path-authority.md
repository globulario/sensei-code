# C4 — path identity as authority, governed by X (brief)

C2 established *when* authority must bind: after the last mutation. C3
implemented that timing correctly at both binding points, and its candidate
was refused because *what* it bound was incomplete — `report.FromDiff`
parses only unquoted headers, so a path Git quotes produced no FileChange
and confinement failed OPEN. C4 changes the identity set, not the timing.

The law C4 must satisfy:

> **Confinement operates over Git path identity, not over a lossy textual
> rendering of a diff.** The question "which paths changed?" has one
> authoritative producer that cannot silently lose a legal Git pathname.

Every authority decision downstream of the candidate diff — confinement,
M2.2 grant inspection, prospective inspection, Level-1 classification, the
audit and the change report — lives inside `runCandidate`, so reconciling
the authoritative `gitx.ChangeSet` against `report.FromDiff` at C3's two
binding points protects all of them without editing their files.

C4 also repairs the two protocol defects C3's adjudication exposed:
**isolation is measured at start and end** rather than asserted, with its
interpretation frozen in advance, and the **candidate must be committed**, so
a successful candidate has literal parent X rather than an identity carried
by bytes alone. The subject is materialised as a shallow boundary containing
commit X itself, which is why ancestry and isolation now coexist.

Plan `97e520945f0b3fb84104e042ff1423fd1cfaf7b4b37d3c2a1d497e58aa6a4694`; identities, materialisation, required artifacts,
falsifiers and predictions in
`experiments/c4-path-authority/manifest.md`. `internal/gitx/capture.go`
remains an explicit non-claim owed to C5.

## C4 RESULT — witness FAILED under its own frozen protocol, candidate unadmitted

Governor, producer and subject identity all held, and the subject was
**literally X** with no controller object reachable at either end. The
ancestry-capable mechanism was demonstrated by the **pre-freeze
validation**, not by this run: C4's own subject carried `refs/heads/main`
from the start, a step the frozen procedure does not contain and that I
added at materialisation, so the isolation predicate was already violated
before the governor ran.
The repair was independently accepted over exactly the frozen seven paths.

Two frozen requirements failed, applied as written: the isolation predicate
was violated **at startup by the controller's own deviation** — an extra
`refs/heads/main` created outside the frozen procedure, before the governor
ran, and not (as first recorded) by the candidate ref at end — and the
required candidate commit was absent, so literal parent-X lineage is **not
established**. No commit was minted afterwards and
the branch is not called harmless. The candidate is retained and unadmitted;
there is no X+1.

Owed next: refs classified by authority rather than appearance, and
candidate commitment as a workflow obligation before terminal completion.
`capture.go` (C5) does not jump ahead of those.


A third refusal ground, found by the review of the record: `confineToPlan`
collapses whitespace-distinct paths via `TrimSpace`, so a legal Git pathname
differing only by a leading space passes confinement. **C4's own frozen plan
specified that canonicalisation**, so the worker followed instructions and
the fail-open is the freeze's. C3 lost quoted paths at the renderer; C4
acquired Git-owned paths and normalised them away at the comparison —
authoritative acquisition is not authoritative use. C5 owes: a Git path is
compared as Git reports it.
