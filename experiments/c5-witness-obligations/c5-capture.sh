#!/bin/bash
# $1 = output file, $2 = label, $3 = subject dir, $4 = X, $5 = expected candidate ref ("" at start)
S=$3; X=$4; EXPECT_CAND=$5
{ echo "C5 subject state — $2"
  echo "captured $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  echo "HEAD                          $(git -C $S rev-parse HEAD)"
  echo "tree                          $(git -C $S rev-parse HEAD^{tree})"
  echo "HEAD is X                     $([ "$(git -C $S rev-parse HEAD)" = "$X" ] && echo YES || echo NO)"
  echo "parents of X                  $(git -C $S log --format='%P' -1 $X 2>/dev/null | wc -w)"
  echo "working tree (non .sensei)    $(git -C $S status --short | grep -v '^?? .sensei-code/' | wc -l) path(s)"
  echo "remotes                       $(git -C $S remote | wc -l)"
  echo ".git/shallow                  $([ -f $S/.git/shallow ] && echo PRESENT || echo ABSENT)   [EXPECTED PRESENT]"
  echo ".git/objects/info/alternates  $([ -f $S/.git/objects/info/alternates ] && echo "PRESENT: $(cat $S/.git/objects/info/alternates)" || echo ABSENT)   [FALSIFIER IF PRESENT]"
  echo "object store                  $(git -C $S count-objects -v | tr '\n' ' ')"
  echo
  echo "refs (each classified against the frozen table):"
  git -C $S for-each-ref --format='%(refname) %(objectname)' | while read -r ref obj; do
    case "$ref" in
      refs/heads/main|refs/tags/c5-boundary)
        [ "$obj" = "$X" ] && echo "  $ref -> $obj  PERMITTED (start ref naming X)" || echo "  $ref -> $obj  FALSIFIER (permitted ref not naming X)" ;;
      refs/heads/sensei-code/*)
        if [ -n "$EXPECT_CAND" ]; then
          anc=$(git -C $S merge-base --is-ancestor $X $obj 2>/dev/null && echo yes || echo no)
          echo "  $ref -> $obj  PERMITTED-IF-CANDIDATE (governor-created; descends from X: $anc)"
        else
          echo "  $ref -> $obj  FALSIFIER (candidate ref present before the governor ran)"
        fi ;;
      *) echo "  $ref -> $obj  FALSIFIER (ref outside the frozen table)" ;;
    esac
  done
  echo
  echo "object probes:"
  printf "  %-14s %-46s %s\n" "${X:0:12}" "$(git -C $S cat-file -t $X 2>&1 | head -1)" "[EXPECTED: commit]"
  for o in 85abf69fd414cb9a9e51bce908c5c64ee0e5d565 1a40d3301cac72f1d60449efca39cb1636f74c01 ba5e8eed63e144d7469bff001f708d7acfad010f; do
    printf "  %-14s %-46s %s\n" "${o:0:12}" "$(git -C $S cat-file -t $o 2>&1 | head -1)" "[FALSIFIER IF RESOLVES]"
  done
} > $1
