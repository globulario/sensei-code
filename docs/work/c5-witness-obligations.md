# C5 — the witness obligations, governed by X (brief)

C4 was VOID. Its instrument was incomplete against its own frozen contract,
and the defects that produced that verdict were the adjudicator's: a run
script that did not emit the fields its own freeze required, a setup step
outside the frozen materialisation, a plan that prescribed the whitespace
normalisation it was meant to forbid, and corrections that reached some
renderings of the record and not others.

So C5's first job is not to test the loop. It is to make the witness prove
itself:

> **An instrument must prove its own completeness before its contents are
> allowed to prove anything else** — mechanically, as a gate that refuses,
> because every one of C4's defects was something verified by reading.

Seven frozen obligations: instrument self-closure before adjudication; refs
classified by reachability and ownership rather than shape; candidate commit
mandatory before terminal completion; exact Git pathname identity; the
materialisation verified **and captured** before the governor runs; a missing
required field taking the hard refusal/VOID path rather than degraded
continuation; and the actual reviewer identity measured from the run — with
the rule that **reviewer identity is evidence about who reviewed, not
authority to admit**, and that no bounded verdict means UNREVIEWED, never
clean by exhaustion.

Two of those are repairs to the loop and form the supplied plan
(`990090fd50446fedcdf60f11e3256ed91a22fac1670cc4d9333e86f9e638d554`): path identity compared exactly as Git reports it, and terminal
completion refusing an uncommitted candidate. The other five are repairs to
this harness.

Identities, the ref-classification table, required artifacts and fields, the
falsifiers and the predictions are in
`experiments/c5-witness-obligations/manifest.md`. `capture.go` remains C6.
