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
