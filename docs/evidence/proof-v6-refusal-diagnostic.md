# Diagnostic: the five COLD refusals

**This is diagnostic, not benchmark scoring.** No refusal is rescored. The
frozen result stands exactly as measured: COLD delivered 4 of 11, and all seven
non-delivering arms remain `NOT_EVALUATED`.

Each classification cites the preserved event stream of the attempt it
describes.

## Two distinct causes, not one

The five refusals split cleanly, and the split matters more than the count.

| task | gate condition | RAW was | class |
|---|---|---|---|
| `internal-architect-2e095c4` | knowledge gap, unclosed | INCORRECT | **JUSTIFIED** |
| `internal-setup-16ecbc3` | knowledge gap, unclosed | INCORRECT | **JUSTIFIED** |
| `internal-setup-e645669` | knowledge gap, unclosed | CORRECT | **JUSTIFIED** |
| `internal-tui-ea046ba` | explicit approval gate | CORRECT | **UNNECESSARY** |
| `internal-tui-be512db` | explicit approval gate | INCORRECT | **UNNECESSARY** |

---

## Class A — knowledge gap, unclosed (3 arms): JUSTIFIED_REFUSAL

**Exact condition**, from `authority.required` payloads:

```
architect-2e095c4  "Sensei reported missing coverage in the planned region:
                    graph indexes this area but no anchored rules apply"
setup-16ecbc3      same
setup-e645669      "graph coverage is absent for the planned files: only 1 of 2
                    requested file(s) are examined in the graph"
```

**Genuinely unavailable, or merely not retrieved?** Genuinely unavailable. The
graph holds no anchored rules for `internal/architect`, and has examined only
one of the two files `setup-e645669` plans to touch. This is absence of
knowledge, not a retrieval failure.

**Would proceeding have violated a documented invariant?** Not one that exists.
No anchored rule applies — which is precisely why Sensei refuses: absence of a
rule is not proof of safety, and reading silence as permission is the failure
this product was built to prevent. Classified JUSTIFIED on Sensei's own
doctrine, not on the outcome.

**Did an autonomous path already exist?** A closure round **ran and failed** on
all three:

```
status :: the knowledge gap did not close; escalating with it open
```

**And here is the finding.** The mechanism that could close a coverage gap
autonomously — machine-derived coverage — exists in this codebase and is **not
connected**. `Engine.derivedCoverage` computes it; `internal/derived` derives
it; `relevance.go` decides whether it resolves the gap. But `routePlan` builds
its `Action` without `DerivedCoverage`, so the router never receives any. This
was found and pinned during PR #83, before any of these runs:

```go
// internal/workflow/relevance_test.go
func TestTheGovernedRunDoesNotYetSupplyDerivedCoverage(t *testing.T)
```

So the closure round consists of asking the architect to investigate, while no
channel exists by which that investigation can produce coverage the router will
accept. It cannot close, so it escalates — every time.

**Minimum existing mechanism that would allow delivery:** connect
`Engine.derivedCoverage` to `routePlan`'s `Action`. No new governance. The
relevance gate that guards it is already built, already tested, and already
refuses irrelevant derivations. **Rule Zero holds: no new mechanism is needed,
an existing one is unwired.**

That change widens autonomy and must arrive with its own evidence — which is
exactly what the pinning test says.

---

## Class B — explicit approval gate (2 arms): UNNECESSARY_REFUSAL

**Exact condition:**

```
"Sensei requires approval for this change class: human_approval_required
 (blast radius security)"
```

Confirmed live against the same graph:

```
$ sensei preflight --file internal/tui/model.go
Risk: SECURITY_RISK
Change risk: blast=security, approval=human_approval_required
Blind spots: anchored entity in security/auth/rbac/pki/jwt/cert namespace
```

**Genuinely unavailable, or merely not retrieved?** Neither. This is not a
knowledge gap at all — `PREFLIGHT_STATUS_OK`, coverage sufficient, 2 direct
anchors. The graph had complete information and classified the change as
security-gated.

**Why:** the invariant `sensei_code.provider.credentials_remain_provider_owned`
lists `internal/tui/model.go` among its protected files. Every change to that
file is therefore a security change class requiring human approval.

**Would proceeding have violated that invariant?** No. Both tasks change
transcript **scrolling**. The only credential-related content in
`internal/tui/model.go` is one help string:

```go
b.WriteString("\n\nCredentials stay with each native provider client.
               Sensei Code stores no OAuth tokens.\n")
```

A file that *renders a sentence about* credentials is anchored as though it
*handles* them. No credential path is touched by either task.

**Did an autonomous path exist?** No, and correctly not. An explicit approval
gate outranks everything and is deliberately not closable by evidence —
`TestAnApprovalGateIsNotClosableByEvidence`. **The router behaved exactly
right.** Weakening that is forbidden and is not proposed.

**Minimum existing mechanism that would allow delivery:** narrow that
invariant's `protects.files` to the files that actually own credentials.
That is awareness **data**, not mechanism, and it does not weaken the standard —
it stops a credentials rule from claiming a file that only mentions credentials.

The same over-broad anchor was found independently during the linkage
evaluation in PR #83, where it produced the one spurious `tui → tui` relation
that had to be rejected. **Two different analyses, months of work apart, landed
on the same anchor.**

---

## Ranked by observed impact

```
1.  REFUSAL          5/11 arms   — 3 justified (unwired closure), 2 unnecessary (over-broad anchor)
2.  TIMEOUT          1/11 arms   — decision-6cf23e8 exhausted 22 min
3.  INFRASTRUCTURE   1/11 arms   — session-4d32937, externally attributable
```

Refusal is the dominant delivery cost by a factor of five, and it has **two
different causes needing two different repairs** — one wiring, one data. Neither
requires a new governance mechanism.

## What this diagnostic does not establish

- It does not rescore any refusal. COLD delivered 4 of 11.
- It does not claim the three justified refusals were *wrong* — under Sensei's
  own doctrine they were right, and two of the three were on tasks RAW got
  wrong.
- It does not establish that connecting derived coverage *would* have delivered
  those three. It establishes that the router had no channel to receive a
  closure, which is why the closure round could not succeed.
- One timeout and one infrastructure failure are single observations. Nothing is
  ranked from them beyond their count.
