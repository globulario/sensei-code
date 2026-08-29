# C3 — the confinement repair, governed by X, in an isolated subject (brief)

The third attempt at M27 item 7, and the first under the corrected isolation
protocol. C1 was refused at routing (two planned files unexamined); C2
produced a scope-exact candidate that never converged, because a confinement
check placed only before `e.validate` does not bind the candidate that
reaches judgement. C3's plan authorises both call sites, so the lesson C2
established is what C3 implements rather than what C3 rediscovers.

Governor `f01592b` (unchanged by #118 or #119 landing), base = X, subject
materialised from X alone into a fresh repository **designed** to have no
remotes, no shared object database and no reachable controller ref — a
design the run did not measure and post-run capture corroborates, never a
run-time established fact (see the final adjudication). Expected artifacts are
closed, and a missing one makes the witness void rather than failed.

Everything else — identities, the plan hash, the pre-freeze per-file
measurement, the falsifier, the bootstrap rule and the predictions — is in
`experiments/c3-confinement/manifest.md`. If the candidate is scope-exact,
passes audit and review, and the governor stays pinned, it is the first
candidate eligible to become X+1, by the owner's admission.

## C3 RESULT — scope-exact, accepted by review, retained, NOT admitted

Governor pinned (commit and binary sha256 equal at both ends), subject
untouched, instrument complete, scope exactly the frozen four, Codex ACCEPT
on the reviewed diff `80c2abdb…`. Two identity caveats stand in the record:
the isolated subject is a byte-equivalent re-materialisation of X rather
than X itself, so **literal X→X+1 ancestry is not established**; and the
candidate was never committed, so its identity is carried by its bytes
alone. Admission — which would create a commit parented by X carrying the
reviewed tree — is the owner's, and has not been taken.


## ADJUDICATED — witness passes, candidate refused

The exact-head review of the C3 record constructed a quoted-path
counterexample against the candidate's repair, reproduced against the
preserved candidate: `confineToPlan` fails OPEN when a changed path is one
Git quotes, because `report.FromDiff` does not parse quoted headers. The
candidate is therefore **refused and permanently unadmitted**; there is no
X+1 from C3. The witness itself stands on what it measured: governor pinned,
scope exact, instrument complete. Run-time isolation is **not** among those:
it was designed for, retrospectively corroborated, and never measured — see
the final adjudication below. The repair belongs to a fresh freeze (C4)
under the same governor, over authoritative Git path identity rather than a
textual rendering of a diff. C3 is not repaired in place.


## FINAL ADJUDICATION

```text
instrument            COMPLETE under the frozen protocol (not void)
workflow evidence     VALID
candidate             REFUSED -- quoted-path fail-open; no X+1
run-time isolation    NOT ESTABLISHED, retrospectively corroborated
```

C3 was run in a subject designed for isolation, and post-run evidence
strongly corroborates that state, but run-time isolation is not proven
because the protocol did not measure it at start and end. C4 makes isolation
a required start/end instrument artifact with its interpretation frozen in
advance.
