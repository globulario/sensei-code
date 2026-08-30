#!/bin/bash
# Instrument closure GATE. Exits non-zero -> the witness is VOID and no semantic
# adjudication may proceed.
#
#   $1 = runs dir   $2 = start | end | closed
#
# Amendment (pre-run, on top of the ddbf956 freeze):
#   - a field counts only when its VALUE is non-empty, not when its label exists;
#   - the gate reads the subject captures and refuses any FALSIFIER row;
#   - C5.candidate.diff is required iff the run reports a candidate;
#   - the gate checks its own artifact (C5.closure.txt) in the "closed" phase,
#     so the receipt required by the frozen table is itself gated.
set -u
R=$1; PHASE=$2; MISS=0
bad() { echo "$1"; MISS=1; }
req_file() { [ -s "$R/$1" ] || bad "MISSING ARTIFACT: $1"; }
# req_field FILE LABEL -- label must be at line start AND followed by a non-blank value.
req_field() {
  local pat; pat=$(printf '%s' "$2" | sed 's/[][\.*^$(){}?+|/]/\\&/g')
  if ! grep -qE "^${pat}[[:space:]]*[^[:space:]]" "$R/$1" 2>/dev/null; then
    if grep -qE "^${pat}" "$R/$1" 2>/dev/null; then bad "EMPTY FIELD in $1: $2"
    else bad "MISSING FIELD in $1: $2"; fi
  fi
}
# The evidence producer is the process that ANSWERED, not a file on disk.
# "unknown" is an instrumentation defect, not a measurement.
req_measured() {
  req_field "$1" "$2"
  if grep -qE "^$(printf '%s' "$2" | sed 's/[][\.*^$(){}?+|/]/\\&/g').*unknown" "$R/$1" 2>/dev/null; then
    bad "UNMEASURED FIELD in $1: $2 (records 'unknown'; the producer must be measured, not guessed)"
  fi
}
# START ONLY. A falsifier before the governor runs is a PRECONDITION refusal:
# the subject was not what the freeze says, so nothing may be governed.
no_falsifier() {
  [ -s "$R/$1" ] || return 0
  if grep -q "FALSIFIER" "$R/$1"; then
    bad "FALSIFIER RECORDED in $1:"; grep -n "FALSIFIER" "$R/$1" | sed 's/^/    /'
  fi
  grep -q "^ISOLATION GATE: PASS" "$R/$1" || bad "NO ISOLATION-GATE PASS LINE in $1"
}
# END. Completeness ONLY -- never the verdict. A complete end capture that
# records a falsifier is a VALID WITNESS reporting an isolation FAIL; treating
# it as VOID would collapse FAIL into VOID and lose exactly the measurement the
# run exists to make. C4's lesson is an ORDER: complete first, judge second.
complete_capture() {
  [ -s "$R/$1" ] || { bad "MISSING ARTIFACT: $1"; return; }
  grep -qE "^falsifiers fired: [0-9]+" "$R/$1" || bad "TRUNCATED CAPTURE $1: no falsifier count -- the capture did not run to completion"
  grep -qE "^ISOLATION GATE: (PASS|FAILED)" "$R/$1" || bad "TRUNCATED CAPTURE $1: no terminal classification line"
  grep -q "^refs (classified against the frozen table):" "$R/$1" || bad "INCOMPLETE CAPTURE $1: no ref classification section"
}

req_file C5.run
req_file C5.subject.start.txt
req_file C5.graph.metadata.pre.json
req_file C5.materialise.txt
no_falsifier C5.subject.start.txt
for f in "governor commit (source) " "governor binary path " "governor binary sha256 " \
         "producer binary path " "producer binary sha256 " \
         "subject HEAD " "subject tree " "plan sha256 " \
         "harness pinned "; do req_field C5.run "$f"; done
req_measured C5.run "producer serving at start "

if [ "$PHASE" = "end" ] || [ "$PHASE" = "closed" ]; then
  complete_capture C5.subject.end.txt
  req_file C5.log
  req_file C5.extract.txt
  [ -f "$R/C5.receipts.jsonl" ] || bad "MISSING ARTIFACT: C5.receipts.jsonl (must exist even when empty)"
  for f in "governor commit at end " "governor binary path at end " "governor binary sha256 at end " \
           "producer binary path at end " "producer binary sha256 at end " \
           "subject HEAD at end " "subject tree at end " \
           "reviewer provider " "reviewer verdict " "reviewer reviewed digest " \
           "candidate committed " "EXIT " \
           "reviewer attempts " "reviewer providers attempted "; do req_field C5.run "$f"; done
  req_measured C5.run "producer serving at end "
  req_field C5.run "producer serving stable "
  grep -qE "^producer serving stable[[:space:]]+yes" "$R/C5.run" \
    || bad "PRODUCER SERVING IDENTITY CHANGED (or unmeasured) ACROSS THE RUN"
  # Conditional artifact. The frozen table says REQUIRED IFF A CANDIDATE
  # EXISTS -- which is not the same as "was committed". An uncommitted
  # worktree state is still a candidate state, and its exact diff is the
  # evidence C4 had to reconstruct after the fact.
  req_field C5.run "candidate state exists "
  STATE=$(sed -n 's/^candidate state exists[[:space:]]*//p' "$R/C5.run" | head -1)
  NREFS=$(sed -n 's/^candidate-shaped refs in subject[[:space:]]*//p' "$R/C5.run" | head -1)
  if [ "${NREFS:-0}" -gt 0 ] 2>/dev/null && [ "$STATE" != "yes" ]; then
    bad "CANDIDATE STATE MISREPORTED: $NREFS candidate-shaped ref(s) in the subject but 'candidate state exists' says $STATE"
  fi
  if grep -qE "^candidate committed[[:space:]]+yes" "$R/C5.run" 2>/dev/null && [ "$STATE" != "yes" ]; then
    bad "CANDIDATE STATE MISREPORTED: committed yes but 'candidate state exists' says $STATE"
  fi
  if [ "$STATE" = "yes" ]; then
    # It must EXIST. A zero-byte diff is evidence only when the record says why.
    if [ ! -f "$R/C5.candidate.diff" ]; then
      bad "MISSING ARTIFACT: C5.candidate.diff (a candidate state existed)"
    elif [ ! -s "$R/C5.candidate.diff" ]; then
      req_field C5.run "candidate diff empty because "
    fi
  fi
  if grep -qE "^candidate committed[[:space:]]+yes" "$R/C5.run" 2>/dev/null; then
    req_field C5.run "candidate ref "
    req_field C5.run "candidate parent "
    req_field C5.run "candidate ancestry X->candidate "
    grep -qE "^candidate ancestry X->candidate[[:space:]]+yes" "$R/C5.run" \
      || bad "CANDIDATE DOES NOT DESCEND FROM X (or ancestry unmeasured)"
  fi
fi

if [ "$PHASE" = "closed" ]; then
  # The gate's own receipt is an artifact the frozen table requires; gate it.
  req_file C5.closure.txt
  grep -q "instrument closure OK (start)" "$R/C5.closure.txt" 2>/dev/null \
    || bad "MISSING RECEIPT in C5.closure.txt: start-phase closure"
  grep -q "instrument closure OK (end)" "$R/C5.closure.txt" 2>/dev/null \
    || bad "MISSING RECEIPT in C5.closure.txt: end-phase closure"
fi

if [ $MISS -ne 0 ]; then echo "INSTRUMENT CLOSURE FAILED ($PHASE) -> WITNESS VOID"; exit 1; fi
echo "instrument closure OK ($PHASE)"
