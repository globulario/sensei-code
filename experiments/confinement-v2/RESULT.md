# confinement-v2 — result: links 6–7 for the second family; then a 9 MB binary defeated review

```
E3   16:57:37Z → 17:02:17Z   exit 1   workflow.failed
coverage   derived coverage: 1 anchor(s) over 1 planned file(s) — gosumcheck/main.go [invocation confinement]
route      architectural-authority-granted                    ← links 6 and 7, second family
plan       one file, as the task was written to allow
worker     Claude, 29s: +2 −2 in main.go — exactly the requested change
then       candidate also contains gosumcheck/gosumcheck, an ELF binary, 9,211,341 bytes, TRACKED
audit      refused: malformed_diff · diff payload exceeds 5242880 bytes
edit_check ResourceExhausted: 6860500 > 4194304 (gRPC max)
review     Codex: no bounded decision → Claude: no bounded decision
terminal   no bounded implementor produced an acceptable candidate; candidate kept, resumable
```

## Established

**Links 6 and 7 hold for `command_invocation_confined_to`.** With a plan that
stays inside `gosumcheck/main.go`, the confinement anchor closed the coverage
gap and the route changed from cold to granted; the run proceeded to an
implementor. With confinement-v1's link 5, the second family now has the full
`recipe → DERIVED → relevant coverage → routing changed` chain observed on a
foreign repository. The mechanism is not a lock analyzer.

The code change itself is correct (doc comment and the one verbose line), and
`"go" confined to gosumcheck` re-derives `DERIVED` on the candidate.

## The failure, which is a product finding and not about the family

The implementor verified its edit by building the command. The 9.2 MB binary
it produced sat untracked in the candidate worktree, the candidate commit swept
it in, and from there every governance surface failed **on size** rather than
on substance: the diff audit refused a payload over 5 MB, `edit_check`'s gRPC
message exceeded 4 MB, and both independent reviewers could not bound a review
of a diff they could not read. The engine then treated "no bounded review" as
an implementor problem and handed the *same oversized candidate* to the next
executor, which changed nothing about the binary — twice.

Three defects, in order of weight:

1. **The candidate boundary admits build artifacts.** A worker's compilation
   output is not part of the change; nothing strips or refuses it before the
   candidate is committed and judged.
2. **Size failures are attributed as review failures.** A candidate that
   cannot be audited for a structural reason should terminate with that reason
   (`candidate not auditable: binary artifact`), not be retried through every
   implementor and reviewer as if the judgement were the hard part.
3. **Audit and edit-check refuse whole diffs by byte count**, so one binary
   blinds them to a two-line text change they could have judged.

None of this is the fixture's fault, and none of it is about derivation.

## Not established

Nothing about compounding beyond one encounter for this family; nothing about
the reviewer's verdict on the change, which it never got to give.

## E3.log trimmed

The committed `E3.log` keeps every non-`output` event (routing, coverage, audit, review, git, terminal). Worker/reviewer stream-json `output` events -- 104,600 of them, 34353534 bytes, carrying the 9 MB binary through the review transcripts -- are dropped. Untrimmed original: sha256 a0da2009b9ad27128e6160d72c7d82d063fb39a1078daf1a2399b45b471149ae, preserved outside the repository.
