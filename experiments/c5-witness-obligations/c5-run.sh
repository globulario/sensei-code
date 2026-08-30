#!/bin/bash
# C5 frozen orchestration. The ONE runner: pin -> materialise -> capture ->
# gate -> governor -> extract -> capture -> gate -> closed. Nothing about C5 is
# left to an after-freeze shell sequence; C4 began in violation of its own
# predicate because a `git checkout -B main` was typed outside the procedure.
#
#   c5-run.sh <workdir>
#
# An abort before the governor is an instrument refusal (nothing was governed).
# An abort after it makes the witness VOID; no semantic adjudication follows.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
CTRL=$(cd "$HERE/../.." && pwd)
RUNS="$HERE/runs"
WORK=${1:?usage: c5-run.sh <workdir>}
SUBJ="$WORK/c5-subject"
X=f01592b0f0828605ed254047fc064f41dacc78f2
GOV=/tmp/claude-1000/-home-dave-Documents-github-com-globulario-sensei-code/0c05292b-ad66-420a-9985-54db3e81471f/scratchpad/sensei-code-shw1
GOV_SHA=7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea
# The frozen `producer`: a FILE identity. No run has been shown to execute it.
# The executable that answers awareness is measured separately, below.
PROD=/tmp/claude-1000/-home-dave-Documents-github-com-globulario-sensei-code/14e477ec-1052-4b6a-b741-ba143440786d/scratchpad/sensei-f3
PROD_SHA=13d4bfada3a458b8ea92b550cc307338b4f542c81446d6b365daf35c01a64ac9
AW_PORT=10122
DOMAIN=github.com/globulario/sensei-code
PLAN="$HERE/plan.json"
PLAN_SHA=990090fd50446fedcdf60f11e3256ed91a22fac1670cc4d9333e86f9e638d554
RUN="$RUNS/C5.run"
CLOSURE="$RUNS/C5.closure.txt"
# Harness pins. Recording a hash proves what ran; REFUSING a hash keeps an
# edited harness from running at all. A frozen witness needs the second.
PIN_CAPTURE=ecfd73e899c9261fddb5c05b1ef27795fb8e908d445376873029d30522dbee18
PIN_CLOSURE=7481974a89b540a589b7b0d170fe1008a24ada5640d0a0bd53c1a2a20f5ff9cf
PIN_EXTRACT=dfad93f2bf7a5ba57f9a2dadcbfb075148b2fe9b847f1f6b1446fe600391d6a3

die() { echo "C5 ABORT: $*" >&2; exit 1; }
gate() { # tee the gate's own output into the artifact the freeze requires
  local phase=$1 rc=0
  bash "$HERE/c5-closure.sh" "$RUNS" "$phase" >> "$CLOSURE" 2>&1 || rc=$?
  tail -20 "$CLOSURE"
  return $rc
}
# Measured identity of the process that ANSWERS awareness.
serving() {
  local pid exe sha
  pid=$(ss -lptnH "sport = :$AW_PORT" 2>/dev/null | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2)
  [ -n "$pid" ] || { echo "pid unknown | exe unknown | sha256 unknown"; return; }
  exe=$(readlink -f "/proc/$pid/exe" 2>/dev/null)
  sha=$(sha256sum "/proc/$pid/exe" 2>/dev/null | cut -d' ' -f1)
  echo "pid $pid | exe ${exe:-unknown} | sha256 ${sha:-unknown}"
}
serving_sha() { printf '%s' "$1" | sed 's/.*sha256 //'; }

# ---- 0. one invocation, no overwrite --------------------------------------
mkdir -p "$RUNS"
[ -e "$RUN" ] && die "$RUN exists; C5 is ONE invocation and this runner never overwrites a record"
[ -e "$SUBJ" ] && die "$SUBJ exists; refusing to reuse a subject directory"

# ---- 1. the harness pins itself, before anything else ---------------------
for f in c5-capture.sh:$PIN_CAPTURE c5-closure.sh:$PIN_CLOSURE c5-extract.py:$PIN_EXTRACT; do
  n=${f%%:*}; want=${f##*:}; got=$(sha256sum "$HERE/$n" | cut -d' ' -f1)
  [ "$got" = "$want" ] || die "harness drift: $n is $got, pinned $want"
done
# c5-run.sh cannot pin its own digest inside itself. It refuses working-tree
# drift instead and records the commit and blob a reader can verify.
[ -z "$(git -C "$CTRL" status --porcelain -- \
        experiments/c5-witness-obligations/c5-run.sh \
        experiments/c5-witness-obligations/c5-capture.sh \
        experiments/c5-witness-obligations/c5-closure.sh \
        experiments/c5-witness-obligations/c5-extract.py)" ] \
  || die "harness files differ from the committed freeze; commit or restore them before running"
HARNESS_COMMIT=$(git -C "$CTRL" rev-parse HEAD)
HARNESS_BLOB=$(git -C "$CTRL" hash-object "$HERE/c5-run.sh")

# ---- 2. pinned identities -------------------------------------------------
[ -x "$GOV" ] || die "governor binary missing"
[ "$(sha256sum "$GOV" | cut -d' ' -f1)" = "$GOV_SHA" ] || die "governor binary sha256 differs from the pinned governor"
[ -f "$PROD" ] || die "producer reference file missing"
[ "$(sha256sum "$PROD" | cut -d' ' -f1)" = "$PROD_SHA" ] || die "producer reference file sha256 differs from the frozen value"
[ "$(sha256sum "$PLAN" | cut -d' ' -f1)" = "$PLAN_SHA" ] || die "plan.json differs from the frozen plan"
git -C "$CTRL" cat-file -e "$X^{commit}" 2>/dev/null || die "X not present in the controller"
S=$(serving)
[ "$(serving_sha "$S")" != "unknown" ] || die "the awareness producer is unmeasurable (:$AW_PORT); a run cannot record who produced its evidence"

# ---- 3. materialisation, exactly the frozen procedure ---------------------
git -C "$CTRL" tag -f c5-boundary "$X" >/dev/null 2>&1 || die "cannot tag the boundary"
git clone --depth 1 --no-tags --single-branch --branch c5-boundary "file://$CTRL" "$SUBJ" >"$RUNS/C5.materialise.txt" 2>&1
CLONE_RC=$?
git -C "$CTRL" tag -d c5-boundary >/dev/null 2>&1
[ $CLONE_RC -eq 0 ] || die "clone failed (see C5.materialise.txt)"
git -C "$SUBJ" remote remove origin >/dev/null 2>&1
# Measured pre-run: `clone --branch <tag>` leaves a DETACHED HEAD and creates no
# refs/heads/main. C4's table permitted a `main` its procedure never created, so
# C4 typed the branch by hand and began outside its own predicate. It is inside
# the procedure here, before the start capture, so the capture measures it.
git -C "$SUBJ" checkout -B main "$X" >>"$RUNS/C5.materialise.txt" 2>&1 || die "cannot place the subject on main at X"
mkdir -p "$SUBJ/.sensei-code"
cp "$CTRL/.sensei-code/config.json" "$SUBJ/.sensei-code/config.json" || die "cannot supply the subject config"

# ---- 4. start record ------------------------------------------------------
{ echo "START $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "runner $HERE/c5-run.sh sha256 $(sha256sum "$HERE/c5-run.sh" | cut -d' ' -f1)"
  echo "harness pinned commit $HARNESS_COMMIT, c5-run.sh blob $HARNESS_BLOB; capture/closure/extract verified against the digests pinned in this runner"
  echo "governor commit (source) $X"
  echo "governor binary path $GOV"
  echo "governor binary sha256 $GOV_SHA"
  echo "producer binary path $PROD (frozen reference FILE identity, NOT a demonstrated serving process)"
  echo "producer binary sha256 $PROD_SHA (the file's digest; no run has been shown to execute it)"
  echo "producer serving at start $S"
  echo "subject path $SUBJ"
  echo "subject HEAD $(git -C "$SUBJ" rev-parse HEAD)"
  echo "subject tree $(git -C "$SUBJ" rev-parse HEAD^{tree})"
  echo "subject config sha256 $(sha256sum "$SUBJ/.sensei-code/config.json" 2>/dev/null | cut -d' ' -f1)"
  echo "plan sha256 $PLAN_SHA"
} > "$RUN"

sensei metadata --domain "$DOMAIN" -addr ":$AW_PORT" --json > "$RUNS/C5.graph.metadata.pre.json" 2>&1 \
  || die "graph metadata capture failed; the pre-run world is unrecorded"

# ---- 5. start capture + start gate: both abort BEFORE the governor -------
bash "$HERE/c5-capture.sh" "$RUNS/C5.subject.start.txt" "start" "$SUBJ" "$X" "" \
  || die "start capture recorded a falsifier; the governor was not invoked"
gate start || die "instrument closure failed at start; the governor was not invoked"

# ---- 6. the ONE governed invocation --------------------------------------
TASK=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["summary"])' "$PLAN") \
  || die "cannot read the task text from the frozen plan"
( cd "$SUBJ" && SENSEI_CODE_BENCHMARK=1 "$GOV" run -task "$TASK" -plan "$PLAN" -json -timeout 30m ) \
  > "$RUNS/C5.log" 2>&1
EXIT_RC=$?
END_TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# ---- 7. measured end identities ------------------------------------------
python3 "$HERE/c5-extract.py" "$RUNS/C5.log" "$RUNS/C5.receipts.jsonl" > "$RUNS/C5.extract.txt" \
  || die "extraction failed; the reviewer identity is unrecorded"
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
if [ "$(serving_sha "$S")" = "$(serving_sha "$SE")" ] && [ "$(serving_sha "$SE")" != "unknown" ]; then
  SERVING_STABLE=yes
else
  SERVING_STABLE="no -- start $(serving_sha "$S") end $(serving_sha "$SE")"
fi
{ echo "EXIT $EXIT_RC $END_TS"
  echo "governor commit at end $X (pinned; the governor checkout is not written by this run)"
  echo "governor binary path at end $GOV"
  echo "governor binary sha256 at end $(sha256sum "$GOV" | cut -d' ' -f1)"
  echo "producer binary path at end $PROD (frozen reference FILE identity, NOT a demonstrated serving process)"
  echo "producer binary sha256 at end $(sha256sum "$PROD" | cut -d' ' -f1)"
  echo "producer serving at end $SE"
  echo "producer serving stable $SERVING_STABLE"
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

# A candidate STATE is a commit, a candidate-shaped ref, or a live worktree.
# Whichever of those exists, its exact diff is preserved: C4 had to
# reconstruct an uncommitted candidate's diff after the fact, and that is the
# evidence a witness should never have to rebuild.
WT=$(git -C "$SUBJ" worktree list --porcelain | sed -n 's/^worktree //p' | grep -v "^$SUBJ$" | head -1)
STATE=no
[ "$COMMITTED" = yes ] && STATE=yes
[ "${CAND_REFS:-0}" -gt 0 ] && STATE=yes
[ -n "$WT" ] && [ -d "$WT" ] && STATE=yes
echo "candidate state exists $STATE" >> "$RUN"
if [ "$STATE" = yes ]; then
  if [ "$COMMITTED" = yes ]; then
    git -C "$SUBJ" diff --no-ext-diff "$X" "$CAND_HEAD" > "$RUNS/C5.candidate.diff"
  elif [ -n "$WT" ] && [ -d "$WT" ]; then
    git -C "$WT" diff --no-ext-diff "$X" > "$RUNS/C5.candidate.diff"
    echo "candidate worktree $WT (UNCOMMITTED: the diff is over a working state, not a commit)" >> "$RUN"
  else
    git -C "$SUBJ" diff --no-ext-diff "$X" "${CAND_HEAD:-$X}" > "$RUNS/C5.candidate.diff"
    echo "candidate worktree NONE (a candidate-shaped ref exists with no live worktree)" >> "$RUN"
  fi
  [ -s "$RUNS/C5.candidate.diff" ] \
    || echo "candidate diff empty because the candidate state carried no change against X" >> "$RUN"
fi
[ -f "$RUNS/C5.receipts.jsonl" ] || : > "$RUNS/C5.receipts.jsonl"
[ -s "$RUNS/C5.receipts.jsonl" ] || echo "(C5.receipts.jsonl preserved EMPTY: the run emitted no derive receipts)" >> "$RUN"

# ---- 8. end capture + end gate + closed gate -----------------------------
CAP_RC=0
bash "$HERE/c5-capture.sh" "$RUNS/C5.subject.end.txt" "end" "$SUBJ" "$X" "$EXPECT_CAND" || CAP_RC=$?
gate end || { echo "WITNESS VOID (end closure)"; exit 1; }
gate closed || { echo "WITNESS VOID (closure receipt loop)"; exit 1; }
[ $CAP_RC -eq 0 ] || { echo "END CAPTURE RECORDED A FALSIFIER -> isolation FAILS"; exit 1; }
echo "C5 complete: exit $EXIT_RC, candidate committed $COMMITTED, artifacts in $RUNS"
