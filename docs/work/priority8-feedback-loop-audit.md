# Priority 8 audit: does the feedback loop genuinely exist?

Priority 8 says to **audit** whether this loop exists, and to implement it only
"after the deterministic semantic foundation above is stable." The foundation
(A–E) is built and green but not landed, so this is the audit, not the
implementation.

The loop as specified:

```text
candidate/question produced
 → reviewed
 → accepted/rejected/refined/contested
 → outcome retained with provenance and scope
 → future ranking/generation changes appropriately
```

**Verdict: stages 1–3 exist. Stage 4 exists but retains almost nothing. Stage
5 does not exist.**

## Stages 1–3: present

A candidate is produced and reviewed, and the verdict is a closed vocabulary —
`accept`, `revise`, `escalate` (`internal/roles/verdict.go:79-87`), validated
so that a `revise` carries instructions or findings and an `accept` cannot be
recorded alongside blocking findings (`verdict.go:156-163`).

"Contested" is also represented, and better than the document asks: when two
reviewers disagree on an unchanged candidate digest,
`internal/workflow/reviewconsistency.go:112` names it as a contradiction
*"nothing changed but the reviewer"* and requires the architect to adjudicate
with a closed answer (`revise` or `accepting stands`), rejecting anything else.

`internal/taskstate/state.go:420` carries `ReviewVerdict` including
`ReviewUnobserved`, so "not reviewed" is a member of the vocabulary rather than
an absence — the law this project keeps rediscovering.

## Stage 4: the outcome is retained, but nearly empty

The only sink is `internal/behavioral`, called from
`Engine.reportOutcome` (`internal/workflow/engine.go:993`):

```go
client.Record(ctx, behavioral.Outcome{
    Status: status,
    Theme:  "sensei_code.candidate_workflow",
    Note:   strings.TrimSpace(task + " -- " + note),
})
```

Four problems, in order of severity:

1. **`Theme` is a constant.** Every outcome this repository ever files carries
   the same theme string. The field exists so "the service can detect repeated
   patterns" (`behavioral.go:163`) and it is the one field that could group
   them — so grouping is defeated at the only call site.

2. **Provenance is prose.** `Note` is `task + " -- " + note`. The candidate
   digest, the revision, the review verdict, and the finding identifiers all
   exist elsewhere in the engine and none of them reach the record in a
   structured field. Downstream this is a sentence, not a citation:
   **reference ≠ referent.**

3. **Scope is one fixed pair, not four levels.** Scope comes from
   `Config.Project` and `Config.Domain`, set once at config load with
   `Domain: "sensei_code"` (`internal/config/config.go:139`). The four levels
   Priority 8 requires — repository-local, organization, language/framework,
   candidate-universal — **have no representation anywhere in the type.**
   The document's warning is that "a repository-specific answer must not
   silently become universal architecture"; today nothing records which of the
   four an outcome even is, so the distinction cannot be preserved because it
   is never made.

4. **It is off, and unconfigured here.** `Enabled: false` by default, and this
   repository's `.sensei/config.yaml` has no behavioral block, so in practice
   no outcome is filed at all. Reporting is also deliberately fire-and-forget,
   which is correct for not failing a task on a reporting error — but it means
   the absence is silent.

## Stage 5: absent, and half of that is correct

Nothing reads outcomes back to change ranking or generation. The only read path
is `CheckAction` → `behavioral.Decision`, which is a **gate**, not a ranking —
and it default-allows when no principle covers the action, with `Governed:
false` recorded so an ungoverned allow is not mistaken for an endorsement.

Part of this absence is deliberate and should stay. The package says so in its
own header: recording "cannot create or promote a principle, and Sensei Code
does not try to: an outcome is evidence, and promoting evidence into a rule
stays a governed step elsewhere." That is Priority 10 holding — *do not let
learned ranking become authority* — and it is the right boundary.

## The actual finding

The constitutional boundary is intact. The **evidence a future governed
promotion would need is not being retained.**

That is the sharp version, and it is not the same complaint as "there is no
learning." Priority 8 cannot simply be implemented later on top of what exists
now, because the record being accumulated today is one constant theme and a
prose note with no scope level and no structured provenance. A promotion step
built on it would have nothing to promote *from*.

## What this implies for sequencing

The document's instruction — implement after the foundation is stable — stands.
But the cheap, non-authority-granting half should come first and is
independent of ranking:

* give `Outcome` a **scope level** from the four named ones, with no default
  that silently means universal;
* carry structured **provenance** (candidate digest, revision, verdict,
  finding ids) rather than concatenated prose;
* make `Theme` derived from what happened rather than a constant.

None of that lets learned ranking become authority. All of it is required
before any ranking could honestly exist.

**Not implemented here.** This is the audit Priority 8 asks for as its first
instruction; the change belongs after A–E land.
