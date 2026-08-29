#!/bin/bash
# C5 frozen orchestration. The ONE runner: materialise -> capture -> gate ->
# governor -> capture -> gate -> closed. Nothing about C5 is left to an
# after-freeze shell sequence; C4 began in violation of its own predicate
# because a `git checkout -B main` was typed outside the frozen procedure.
#
#   c5-run.sh <workdir>
#
# Every step that can falsify aborts the run. An abort before the governor is
# an instrument refusal (nothing was governed); an abort after it makes the
# witness VOID and no semantic adjudication may proceed.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CTRL=$(cd "$HERE/../.." && pwd)
RUNS="$HERE/runs"
WORK=${1:?usage: c5-run.sh <workdir>}
SUBJ="$WORK/c5-subject"
X=f01592b0f0828605ed254047fc064f41dacc78f2
GOV=/tmp/claude-1000/-home-dave-Documents-github-com-globulario-sensei-code/0c05292b-ad66-420a-9985-54db3e81471f/scratchpad/sensei-code-shw1
GOV_SHA=7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea
PROD=/tmp/claude-1000/-home-dave-Documents-github-com-globulario-sensei-code/14e477ec-1052-4b6a-b741-ba143440786d/scratchpad/sensei-f3
PROD_SHA=13d4bfada3a458b8ea92b550cc307338b4f542c81446d6b365daf35c01a64ac9
AW_PORT=10122
DOMAIN=github.com/globulario/sensei-code
PLAN="$HERE/plan.json"
PLAN_SHA=990090fd50446fedcdf60f11e3256ed91a22fac1670cc4d9333e86f9e638d554
RUN="$RUNS/C5.run"
CLOSURE="$RUNS/C5.closure.txt"

die() { echo "C5 ABORT: $*" >&2; exit 1; }
gate() { # phase -> tee the gate's own output into the artifact the freeze requires
  local phase=$1 rc=0
  bash "$HERE/c5-closure.sh" "$RUNS" "$phase" >> "$CLOSURE" 2>&1 || rc=$?
  tail -20 "$CLOSURE"
  return $rc
}
# measured identity of the process actually answering awareness, not a file on disk
serving() {
  local pid exe
  pid=$(ss -lptnH "sport = :$AW_PORT" 2>/dev/null | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2)
  [ -n "$pid" ] || { echo "pid unknown|exe unknown|sha256 unknown"; return; }
  exe=$(readlink -f "/proc/$pid/exe" 2>/dev/null)
  echo "pid $pid|exe ${exe:-unreadable}|sha256 $(sha256sum "/proc/$pid/exe" 2>/dev/null | cut -d' ' -f1)"
}

# ---- 0. one invocation, no overwrite --------------------------------------
mkdir -p "$RUNS"
[ -e "$RUN" ] && die "$RUN exists; C5 is ONE invocation and this runner never overwrites a record"
[ -e "$SUBJ" ] && die "$SUBJ exists; refusing to reuse a subject directory"

# ---- 1. pinned identities, verified before anything is materialised -------
[ -x "$GOV" ] || die "governor binary missing"
[ "$(sha256sum "$GOV" | cut -d' ' -f1)" = "$GOV_SHA" ] || die "governor binary sha256 differs from the pinned governor"
[ -f "$PROD" ] || die "producer binary missing"
[ "$(sha256sum "$PROD" | cut -d' ' -f1)" = "$PROD_SHA" ] || die "producer binary sha256 differs from the frozen value"
[ "$(sha256sum "$PLAN" | cut -d' ' -f1)" = "$PLAN_SHA" ] || die "plan.json differs from the frozen plan"
git -C "$CTRL" cat-file -e "$X^{commit}" 2>/dev/null || die "X not present in the controller"

# ---- 2. materialisation, exactly the frozen procedure ---------------------
git -C "$CTRL" tag -f c5-boundary "$X" >/dev/null 2>&1 || die "cannot tag the boundary"
git clone --depth 1 --no-tags --single-branch --branch c5-boundary "file://$CTRL" "$SUBJ" >"$RUNS/C5.materialise.txt" 2>&1
CLONE_RC=$?
git -C "$CTRL" tag -d c5-boundary >/dev/null 2>&1
[ $CLONE_RC -eq 0 ] || die "clone failed (see C5.materialise.txt)"
git -C "$SUBJ" remote remove origin >/dev/null 2>&1
# Measured pre-run: `clone --branch <tag>` leaves a DETACHED HEAD and creates no
# refs/heads/main. C4's frozen table permitted a `main` naming X that its
# procedure never created, so C4 typed `checkout -B main` outside the procedure
# and began in violation of its own predicate. The step is inside the procedure
# here, before the start capture, so the capture measures its result.
git -C "$SUBJ" checkout -B main "$X" >>"$RUNS/C5.materialise.txt" 2>&1 || die "cannot place the subject on main at X"
mkdir -p "$SUBJ/.sensei-code"
cp "$CTRL/.sensei-code/config.json" "$SUBJ/.sensei-code/config.json" || die "cannot supply the subject config"

# ---- 3. start record ------------------------------------------------------
S=$(serving)
{ echo "START $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "runner $HERE/c5-run.sh sha256 $(sha256sum "$HERE/c5-run.sh" | cut -d' ' -f1)"
  echo "governor commit (source) $X"
  echo "governor binary path $GOV"
  echo "governor binary sha256 $GOV_SHA"
  echo "producer binary path $PROD"
  echo "producer binary sha256 $PROD_SHA"
  echo "producer serving at start ${S//|/ | }"
  echo "subject path $SUBJ"
  echo "subject HEAD $(git -C "$SUBJ" rev-parse HEAD)"
  echo "subject tree $(git -C "$SUBJ" rev-parse HEAD^{tree})"
  echo "subject config sha256 $(sha256sum "$SUBJ/.sensei-code/config.json" 2>/dev/null | cut -d' ' -f1)"
  echo "plan sha256 $PLAN_SHA"
} > "$RUN"

sensei metadata --domain "$DOMAIN" -addr ":$AW_PORT" --json > "$RUNS/C5.graph.metadata.pre.json" 2>&1 \
  || die "graph metadata capture failed; the pre-run world is unrecorded"

# ---- 4. start capture + start gate: both may abort BEFORE the governor ----
bash "$HERE/c5-capture.sh" "$RUNS/C5.subject.start.txt" "start" "$SUBJ" "$X" "" \
  || die "start capture recorded a falsifier; the governor was not invoked"
gate start || die "instrument closure failed at start; the governor was not invoked"

# ---- 5. the ONE governed invocation --------------------------------------
TASK=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["summary"])' "$PLAN") \
  || die "cannot read the task text from the frozen plan"
( cd "$SUBJ" && SENSEI_CODE_BENCHMARK=1 "$GOV" run -task "$TASK" -plan "$PLAN" -json -timeout 30m ) \
  > "$RUNS/C5.log" 2>&1
EXIT_RC=$?
END_TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# ---- 6. measured end identities ------------------------------------------
python3 - "$RUNS/C5.log" "$RUNS/C5.receipts.jsonl" > "$RUNS/C5.extract.txt" <<'PY'
import json,sys
log,rec=sys.argv[1],sys.argv[2]
task_id=prov=verd=dig=""; receipts=[]; resolved=""
for line in open(log,errors="replace"):
    line=line.strip()
    if not line.startswith("{"): continue
    try: d=json.loads(line)
    except Exception: continue
    k=d.get("kind",""); p=d.get("payload") or {}
    if k=="task.created": task_id=d.get("task_id","")
    if k in ("agent.role.assigned",) and p.get("role")=="reviewer": prov=p.get("provider","") or prov
    if k=="review.completed":
        verd=p.get("decision","") or verd
        pv=p.get("provenance") or {}
        prov=pv.get("provider","") or prov
        dig=pv.get("candidate_digest","") or dig
    if k=="candidate.resolved": resolved=json.dumps(p)
    if "receipt" in k or k.startswith("derive"): receipts.append(line)
open(rec,"w").write("".join(r+"\n" for r in receipts))
print("task id "+task_id)
print("reviewer provider "+(prov or "NONE -- no bounded reviewer identity recorded"))
print("reviewer verdict "+(verd or "NONE -- UNREVIEWED, never clean by exhaustion"))
print("reviewer reviewed digest "+(dig or "NONE"))
print("candidate resolution "+(resolved or "NONE"))
PY
TASK_ID=$(sed -n 's/^task id //p' "$RUNS/C5.extract.txt")
EXPECT_CAND=""
[ -n "$TASK_ID" ] && EXPECT_CAND="refs/heads/sensei-code/$TASK_ID"
CAND_REFS=$(git -C "$SUBJ" for-each-ref --format='%(refname)' refs/heads/sensei-code/ | wc -l)
CAND_HEAD=""; CAND_PAR=""; ANC=no; COMMITTED=no
if [ -n "$EXPECT_CAND" ] && git -C "$SUBJ" rev-parse --verify -q "$EXPECT_CAND" >/dev/null; then
  CAND_HEAD=$(git -C "$SUBJ" rev-parse "$EXPECT_CAND")
  CAND_PAR=$(git -C "$SUBJ" rev-parse -q --verify "$EXPECT_CAND^1" 2>/dev/null)
  if [ "$CAND_HEAD" != "$X" ] && [ "$CAND_PAR" = "$X" ]; then ANC=yes; COMMITTED=yes; fi
fi
SE=$(serving)
{ echo "EXIT $EXIT_RC $END_TS"
  echo "governor commit at end $X (pinned; the governor checkout is not written by this run)"
  echo "governor binary path at end $GOV"
  echo "governor binary sha256 at end $(sha256sum "$GOV" | cut -d' ' -f1)"
  echo "producer binary path at end $PROD"
  echo "producer binary sha256 at end $(sha256sum "$PROD" | cut -d' ' -f1)"
  echo "producer serving at end ${SE//|/ | }"
  echo "subject HEAD at end $(git -C "$SUBJ" rev-parse HEAD)"
  echo "subject tree at end $(git -C "$SUBJ" rev-parse HEAD^{tree})"
  cat "$RUNS/C5.extract.txt"
  echo "candidate-shaped refs in subject $CAND_REFS"
  echo "candidate ref ${EXPECT_CAND:-NONE -- the log named no task id}"
  echo "candidate head ${CAND_HEAD:-NONE}"
  echo "candidate parent ${CAND_PAR:-NONE}"
  echo "candidate ancestry X->candidate $ANC"
  echo "candidate committed $COMMITTED"
  echo "log sha256 $(sha256sum "$RUNS/C5.log" | cut -d' ' -f1) $(stat -c%s "$RUNS/C5.log") bytes"
} >> "$RUN"

if [ "$COMMITTED" = yes ]; then
  git -C "$SUBJ" diff --no-ext-diff "$X" "$CAND_HEAD" > "$RUNS/C5.candidate.diff"
else
  WT=$(git -C "$SUBJ" worktree list --porcelain | sed -n 's/^worktree //p' | grep -v "^$SUBJ$" | head -1)
  if [ -n "$WT" ] && [ -d "$WT" ]; then
    git -C "$WT" diff --no-ext-diff "$X" > "$RUNS/C5.candidate.diff"
    echo "candidate worktree $WT (UNCOMMITTED: diff is over a working state, not a commit)" >> "$RUN"
  fi
fi
[ -f "$RUNS/C5.receipts.jsonl" ] || : > "$RUNS/C5.receipts.jsonl"
[ -s "$RUNS/C5.receipts.jsonl" ] || echo "(C5.receipts.jsonl preserved EMPTY: the run emitted no derive receipts)" >> "$RUN"

# ---- 7. end capture + end gate + closed gate -----------------------------
CAP_RC=0
bash "$HERE/c5-capture.sh" "$RUNS/C5.subject.end.txt" "end" "$SUBJ" "$X" "$EXPECT_CAND" || CAP_RC=$?
gate end || { echo "WITNESS VOID (end closure)"; exit 1; }
gate closed || { echo "WITNESS VOID (closure receipt loop)"; exit 1; }
[ $CAP_RC -eq 0 ] || { echo "END CAPTURE RECORDED A FALSIFIER -> isolation FAILS"; exit 1; }
echo "C5 complete: exit $EXIT_RC, candidate committed $COMMITTED, artifacts in $RUNS"
