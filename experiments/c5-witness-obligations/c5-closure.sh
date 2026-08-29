#!/bin/bash
# Instrument closure GATE. Exits non-zero -> the witness is VOID and no semantic
# adjudication may proceed. $1 = runs dir, $2 = "start"|"end"
R=$1; PHASE=$2; MISS=0
req_file() { [ -s "$R/$1" ] || { echo "MISSING ARTIFACT: $1"; MISS=1; }; }
req_field() { grep -q "$2" "$R/$1" 2>/dev/null || { echo "MISSING FIELD in $1: $2"; MISS=1; }; }
req_file C5.run
req_file C5.subject.start.txt
req_file C5.graph.metadata.pre.json
req_field C5.run "^governor commit (source) "
req_field C5.run "^governor binary path "
req_field C5.run "^governor binary sha256 "
req_field C5.run "^producer binary path "
req_field C5.run "^producer binary sha256 "
req_field C5.run "^subject HEAD "
req_field C5.run "^subject tree "
req_field C5.run "^plan sha256 "
if [ "$PHASE" = "end" ]; then
  req_file C5.subject.end.txt
  req_file C5.log
  [ -f "$R/C5.receipts.jsonl" ] || { echo "MISSING ARTIFACT: C5.receipts.jsonl (must exist even when empty)"; MISS=1; }
  for f in "governor commit at end " "governor binary path at end " "governor binary sha256 at end " "producer binary path at end " "producer binary sha256 at end " "subject HEAD at end " "subject tree at end " "reviewer provider " "reviewer verdict " "reviewer reviewed digest " "candidate committed " "^EXIT "; do req_field C5.run "$f"; done
fi
if [ $MISS -ne 0 ]; then echo "INSTRUMENT CLOSURE FAILED ($PHASE) -> WITNESS VOID"; exit 1; fi
echo "instrument closure OK ($PHASE)"
