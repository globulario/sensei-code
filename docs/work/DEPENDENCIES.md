# Dependencies

One section per brief. A single unsectioned note collided on this filename
every time two briefs were open at once, which is what happened here.

A dependency stays listed after it lands, with what it resolved to, because the
reason a brief was written against a moving shape is part of reading it later.

## observation-instruments-do-not-mutate

Depended on the final observation-lane structure from #68
(`feat(workflow): observation is not authorization`).

Resolved. Landed on top of #68 and #79, whose before/after comparison replaced
the post-hoc cleanliness check the brief was written against.

## value-objective-authority

Depends on the task/objective provenance model from #68 —
`SubmittedUnattended` versus interactive human provenance. Implement only on
top of the merged #68 behavior, so the objective-authority tests exercise the
actual entrypoints rather than the pre-#68 `RequestedByHuman` defect.

## observation-to-governed-repair

Resolved. Implemented as #79; the brief's PR (#73) was closed against it.

## fail-closed-claim-provenance

Resolved. Implemented as #80 on top of #68 and #69; the brief's PR (#70) was
closed against it.
