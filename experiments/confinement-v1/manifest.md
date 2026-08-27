# confinement-v1 — the second family, exercised for the first time

`command_invocation_confined_to` has been registered, mapped and prompted since
before the cold-start work, and has never produced a governed-run result. This
is its first. Same discipline as the lock family:

```
cold graph → investigator question (untold) → receipt → recipe persists
→ later derive → DERIVED / REFUTED / UNRESOLVED → coverage only on DERIVED
→ routing consequence
```

## Frozen

```
fixture     github.com/golang/mod @ d0a27b2d4a48460806692bf5c87fc157c3c65292 (2026-08-23)
graph       isolated :10193 · deterministic import (basic) · 4384 triples · closure PROVEN 0/0 · authoritative
substrate   sensei @ e81d7bed (main; #310 and #311 landed) — ONE binary for build and derive
prompt      gap-closure-prompt/v4 · recipes at start: 0
family      command_invocation_confined_to · scope: repository-wide (-search .)
```

## Selection, mechanical, sealed

Stable path order over non-test `.go`; the first literal `exec.Command` site is
the subject. Two sites exist; the rule picks the first:

```
SELECTED   gosumcheck/main.go   (the other: zip/zip.go)
```

The relation and the hand-derived verdict are **sealed in `selection.json`** as
pre-registration of the mechanism and are **not** in the investigator's
context. Encounter 1 receives only the cold gap and the source. It must decide
on its own that executable ownership is worth asking about — or ask something
else, which is preserved and reported, not steered.

## Encounters, written from the code, naming no executable

- **E1** — *gosumcheck logs with the prefix `notecheck:` and its usage line
  names `gosumcheck`. Make the command's log prefix consistent with its name,
  with no other change in behaviour.*
- **E2** — *gosumcheck stops at the first go.sum argument it cannot read.
  Report every unreadable argument and exit non-zero once at the end, with no
  change in behaviour for readable arguments.*

Both plan over `gosumcheck/main.go`, the file that holds the selected site.

## Predictions

- E1: cold → closure round → `RECORDED` or `NO_PROPOSAL` → stop. Whether the
  question is about executable ownership is reported, not required.
- E2 with the investigator's own recipe if it is in this family and about this
  region; otherwise the sealed specimen, labelled `written_by: experiment`:
  `DERIVED` → 1 anchor → route granted → task proceeds.

Falsifiers: `REFUTED` on a confinement that holds → reader defect;
`UNRESOLVED` → the family's envelope is narrower than its tests suggest;
1 anchor from a family the relevance gate does not map → gate defect.

## Divergence from the read-only scan, recorded

The user's scan found `zip/zip.go`; mine also found `gosumcheck/main.go`, which
sorts first. The rule is path order, so `gosumcheck` is the subject. Not
re-selected to match the earlier scan.

## E1 result and the E2 arms, decided before E2 runs

E1: `RECORDED` — `command_invocation_confined_to("gosumcheck.exe" confined to
gosumcheck) searched under gosumcheck`. The family was chosen untold; the
executable is the one `gosumcheck/test.bash` builds and runs, which is a real
invocation on the uncovered test path — but a shell-script invocation is outside
this reader's envelope (it reads Go source for literal `exec.Command`).
Pre-derived and sealed: **`UNKNOWN`** — *no literal invocation found; nothing
to establish*.

- **E2a — faithful**: the investigator's recipe alone. Prediction: `UNKNOWN` →
  0 anchors → cold. A grounded question the family cannot answer earns nothing.
- **E2b — mechanism**: the sealed specimen alone (`go` confined to
  `gosumcheck`, searched repository-wide), labelled `written_by: experiment`.
  Prediction: `DERIVED` → 1 anchor → route granted → task proceeds.

Same base, same E2 task, sequential, artefacts preserved per arm.
