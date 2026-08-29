#!/bin/bash
# C5 subject capture AND falsifier gate.
#
# Prints the classification table and EXITS NON-ZERO if any frozen falsifier
# fired. The frozen manifest says a falsifier at the start gate aborts before
# the governor runs; printing "FALSIFIER" and exiting 0 would leave that
# guarantee to whoever happened to read the output (#PR review of ddbf956).
#
#   $1 output file   $2 label   $3 subject dir   $4 X   $5 expected candidate ref ("" at start)
set -u
OUT=$1; LABEL=$2; S=$3; X=$4; EXPECT_CAND=${5:-}
FALSIFIED=0
fal() { FALSIFIED=$((FALSIFIED+1)); }

{ echo "C5 subject state — $LABEL"
  echo "captured $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  HEAD_SHA=$(git -C "$S" rev-parse HEAD)
  echo "HEAD                          $HEAD_SHA"
  echo "tree                          $(git -C "$S" rev-parse HEAD^{tree})"
  if [ "$HEAD_SHA" = "$X" ]; then echo "HEAD is X                     YES"; else echo "HEAD is X                     NO   [FALSIFIER]"; fal; fi
  echo "parents of X                  $(git -C "$S" log --format='%P' -1 "$X" 2>/dev/null | wc -w)"
  echo "working tree (non .sensei)    $(git -C "$S" status --short | grep -v '^?? .sensei-code/' | wc -l) path(s)"

  REMOTES=$(git -C "$S" remote | wc -l)
  if [ "$REMOTES" -eq 0 ]; then echo "remotes                       0"; else echo "remotes                       $REMOTES   [FALSIFIER]"; fal; fi

  if [ -f "$S/.git/shallow" ]; then echo "shallow                       PRESENT   [EXPECTED]"; else echo "shallow                       ABSENT    [FALSIFIER: the boundary mechanism is missing]"; fal; fi

  if [ -f "$S/.git/objects/info/alternates" ]; then
    echo "alternates                    PRESENT: $(cat "$S/.git/objects/info/alternates")   [FALSIFIER]"; fal
  else echo "alternates                    ABSENT"; fi

  echo "object store                  $(git -C "$S" count-objects -v | tr '\n' ' ')"
  echo
  echo "refs (classified against the frozen table):"
  while read -r ref obj; do
    case "$ref" in
      refs/heads/main|refs/tags/c5-boundary)
        if [ "$obj" = "$X" ]; then echo "  $ref -> $obj  PERMITTED (predeclared, names X)"
        else echo "  $ref -> $obj  FALSIFIER (predeclared ref does not name X)"; fal; fi ;;
      refs/heads/sensei-code/*)
        if [ -z "$EXPECT_CAND" ]; then
          echo "  $ref -> $obj  FALSIFIER (candidate-shaped ref present before the governor ran)"; fal
        elif [ "$ref" != "$EXPECT_CAND" ]; then
          echo "  $ref -> $obj  FALSIFIER (not the candidate ref the governor reported: expected $EXPECT_CAND)"; fal
        elif [ "$obj" = "$X" ]; then
          echo "  $ref -> $obj  PERMITTED (the governor's candidate ref; names X ITSELF -- no candidate commit exists)"
        elif [ "$(git -C "$S" rev-parse -q --verify "$obj^1" 2>/dev/null)" = "$X" ]; then
          echo "  $ref -> $obj  PERMITTED (the governor's candidate ref; first parent is exactly X)"
        else
          echo "  $ref -> $obj  FALSIFIER (candidate ref's first parent is not exactly X)"; fal
        fi ;;
      *) echo "  $ref -> $obj  FALSIFIER (ref outside the frozen table)"; fal ;;
    esac
  done < <(git -C "$S" for-each-ref --format='%(refname) %(objectname)')

  echo
  echo "object probes:"
  if git -C "$S" cat-file -t "$X" >/dev/null 2>&1; then printf "  %-14s %-30s %s\n" "${X:0:12}" "commit" "[EXPECTED]"
  else printf "  %-14s %-30s %s\n" "${X:0:12}" "unresolvable" "[FALSIFIER: X must be present]"; fal; fi
  for o in 85abf69fd414cb9a9e51bce908c5c64ee0e5d565 1a40d3301cac72f1d60449efca39cb1636f74c01 ba5e8eed63e144d7469bff001f708d7acfad010f; do
    if git -C "$S" cat-file -t "$o" >/dev/null 2>&1; then printf "  %-14s %-30s %s\n" "${o:0:12}" "RESOLVES" "[FALSIFIER: controller object]"; fal
    else printf "  %-14s %-30s %s\n" "${o:0:12}" "unresolvable" "[expected]"; fi
  done
  echo
  echo "falsifiers fired: $FALSIFIED"
  [ "$FALSIFIED" -eq 0 ] && echo "ISOLATION GATE: PASS ($LABEL)" || echo "ISOLATION GATE: FAILED ($LABEL) -- $FALSIFIED falsifier(s)"
} > "$OUT" 2>&1
tail -2 "$OUT"
[ "$FALSIFIED" -eq 0 ]
