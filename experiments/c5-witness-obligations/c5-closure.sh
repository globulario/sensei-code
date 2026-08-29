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
no_falsifier() {
  [ -s "$R/$1" ] || return 0
  if grep -q "FALSIFIER" "$R/$1"; then
    bad "FALSIFIER RECORDED in $1:"; grep -n "FALSIFIER" "$R/$1" | sed 's/^/    /'
  fi
  grep -q "^ISOLATION GATE: PASS" "$R/$1" || bad "NO ISOLATION-GATE PASS LINE in $1"
}

req_file C5.run
req_file C5.subject.start.txt
req_file C5.graph.metadata.pre.json
no_falsifier C5.subject.start.txt
for f in "governor commit (source) " "governor binary path " "governor binary sha256 " \
         "producer binary path " "producer binary sha256 " \
         "subject HEAD " "subject tree " "plan sha256 " \
         "harness pinned "; do req_field C5.run "$f"; done
req_measured C5.run "producer serving at start "

if [ "$PHASE" = "end" ] || [ "$PHASE" = "closed" ]; then
  req_file C5.subject.end.txt
  req_file C5.log
  no_falsifier C5.subject.end.txt
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
  # Conditional artifact: a candidate exists -> its diff and its measured identity are required.
  if grep -qE "^candidate committed[[:space:]]+yes" "$R/C5.run" 2>/dev/null; then
    req_file C5.candidate.diff
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
