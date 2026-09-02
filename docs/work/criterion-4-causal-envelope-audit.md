# Criterion 4, audited: the acceptance record still preserves a verdict without its question

> 4. acceptance-critical experiments preserve the question, conditions,
>    execution, and result

Priority 4 asks for an audit of *every* acceptance-, authority-, review-,
admission- and self-improvement-critical record, on the grounds that #137
repaired one instance and the class was open. This is that audit, measured
2026-09-02 on `sensei-code` at `a97e8b7`.

## Method

Every `CallTool("awareness_*")` in non-test code, classified by whether the call
carries an `evidence.Envelope` and whether its result reaches a **durable
event**. A call that only fetches context for a prompt is not an evidence
record; a call whose result is emitted is.

## Result

18 governed call sites. **Six persist a durable event. Four carry the envelope.**

```
env  tool                  event                    site
---  --------------------  -----------------------  ---------------------------------
yes  awareness_preflight   SenseiResult             internal/workflow/assisted.go:406
yes  awareness_preflight   SenseiResult             internal/workflow/engine.go:648
yes  awareness_preflight   SenseiResult             internal/workflow/engine.go:2294
yes  awareness_preflight   SenseiResult             internal/workflow/engine.go:4273
NO   awareness_audit_diff  CandidateAudited (+5)    internal/workflow/engine.go:1439
no   awareness_preflight   ChangeReported           internal/workflow/engine.go:958
```

The preflight class is closed. `#139` routed all four emitting sites through
`internal/evidence`, and the remaining twelve preflight/briefing/resolve calls
feed prompts rather than records.

## The finding: `awareness_audit_diff`

`awareness_audit_diff` is **the acceptance-critical record in this system**. It
is the verdict that governs whether a candidate may be accepted — a reviewer may
not accept over a failing audit. Its request is built at `engine.go:1402`:

```go
auditArgs := map[string]any{"diff": diff, "task": task}
auditArgs["domain"]        = start.Domain()
auditArgs["expected_head"] = tc.Identity.BaseSHA
```

and its result is emitted six times, at lines 1451, 1459, 1469, 1475, 1479 and
1608, always as `audit.Structured` and **never with `auditArgs`**.

That is sensei-code#134 exactly, one level up. #134 preserved a run that stopped
at the human authority boundary carrying its verdict and not the request that
produced it, and the specimen could not be replayed when the upstream repair
landed. The same shape now sits on the record that decides admission.

It matters more here than it did there. `expected_head` is a *correctness* input
to this call, not a detail: the surrounding comment records that omitting it and
pinning the wrong one both produce `cannot_verify`, through different doors. A
preserved `cannot_verify` verdict with no preserved `expected_head` cannot
distinguish "the candidate is unverifiable" from "we asked wrongly".

## The guarantee that is stated but not enforced

`internal/evidence`'s package comment says it is

> the one way to record such a call, so a new site cannot quietly omit the half
> that makes a record replayable.

Nothing enforces that. Two emitting sites omit it today, and a third added
tomorrow would too. The claim is true of the package and false of the codebase —
the same shape as sensei#335, where a drift detector promised that "a surface
added later fails here" while hardcoding half the surfaces.

**A guarantee that lives in a doc comment is a convention, not a mechanism.**

## Not folded in: `engine.go:958`

The preflight at `engine.go:958` is deliberately classified separately rather
than counted as the same defect. It does not preserve a verdict: it copies
`risk_class` and the governing invariant titles into a `change` struct, and the
event carries the change report. That is a **derived summary**, not a preserved
experiment, and the envelope's contract is about replaying an experiment.

It is still true that nothing records why that summary says what it says. Whether
a derived summary owes the same envelope is a real question and it is left open
here rather than answered by making the numbers tidier. Classification stops
where structure stops.

## The repair, and the choice it turned on

Written after this audit and landed in #148. It is recorded here because the
audit is what named the call, and because the choice it turned on is the part a
reader should be able to disagree with:

**does the diff body belong in the record?** `auditArgs["diff"]` is the
question, and `Envelope.Request` is documented as "the exact input". But the
diff would then be duplicated into six event payloads, against the package's own
"the objective is not enormous logs".

The alternative — recording a digest of the diff plus the already-pinned
`BaseSHA` and `CandidateTree` that reconstruct it — is *not* obviously the
forbidden shape, because those pins give the digest a **referent**. The known-bad
version of this is a question that "survives only as a digest with no referent".
A digest whose referent is pinned in the same record is a different thing.

Both readings are defensible. **#148 took the first**: the diff is carried in
full, on the grounds that the candidate worktree is transient and may be gone
when anyone reads the record, so a digest's referent can expire while the record
outlives it. The cost is real and is stated in that PR rather than hidden — the
diff now appears in the audit events — and a reviewer who weighs event size
differently should say so there.

## Criterion 4 verdict

**FAIL at the time of this audit, narrowly and specifically.** Not "evidence
records are unreplayable" — the preflight class was already closed and four
sites carried a complete envelope. The failure was one call, and it was the one
that decides acceptance.

**Closed by #148.** `awareness_audit_diff` now carries its request on all seven
emits including the transport-failure path, and
`TestEveryGovernedCallSiteIsClassified` replaces the preflight-only walk with a
scan over every governed call in `internal/` and `cmd/`, classified `record` or
`context: <why>` in both directions. That is what makes `internal/evidence`'s
stated guarantee — "a new site cannot quietly omit the half that makes a record
replayable" — mechanical rather than a convention.
