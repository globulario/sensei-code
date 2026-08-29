# C3 — the confinement repair, governed by X, in an isolated subject (brief)

The third attempt at M27 item 7, and the first under the corrected isolation
protocol. C1 was refused at routing (two planned files unexamined); C2
produced a scope-exact candidate that never converged, because a confinement
check placed only before `e.validate` does not bind the candidate that
reaches judgement. C3's plan authorises both call sites, so the lesson C2
established is what C3 implements rather than what C3 rediscovers.

Governor `f01592b` (unchanged by #118 or #119 landing), base = X, subject
materialised from X alone into a fresh repository with no remotes, no shared
object database and no reachable controller ref. Expected artifacts are
closed, and a missing one makes the witness void rather than failed.

Everything else — identities, the plan hash, the pre-freeze per-file
measurement, the falsifier, the bootstrap rule and the predictions — is in
`experiments/c3-confinement/manifest.md`. If the candidate is scope-exact,
passes audit and review, and the governor stays pinned, it is the first
candidate eligible to become X+1, by the owner's admission.
