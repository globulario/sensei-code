#!/usr/bin/env python3
"""Subject selection on the pinned external fixture. FROZEN before any encounter.

  Fixture   : golang/sync @ 3ffd83cb522e5ef49bd2fa50f0c0d63dc152ad1f
  Ordering  : stable lexicographic path order over non-test .go files
  Predicate : the first file expressible in the ALREADY FROZEN vocabulary --
              field_access_under_lock or command_invocation_confined_to
  Rule      : no vocabulary may be added after seeing the result

The subject is NOT preselected. It is known in advance that at least one
qualifying relationship exists in this repository, which avoids accidentally
testing vocabulary emptiness -- but which file is chosen is decided here, by the
predicate, not by anyone's judgement about which would read well.
"""
import json, os, re, subprocess, sys

ROOT = sys.argv[1]
MUTEX  = re.compile(r'\bsync\.(RW)?Mutex\b')
STRUCT = re.compile(r'type\s+(\w+)\s+struct\s*\{(.*?)\n\}', re.S)
FIELD  = re.compile(r'^\s*(\w+)\s+([^\s/][^\n]*)$', re.M)
EXEC   = re.compile(r'exec\.Command(?:Context)?\(\s*(?:ctx\s*,\s*)?"([^"]+)"')

def questions(path):
    src = open(os.path.join(ROOT, path), errors="ignore").read()
    out = []
    for m in STRUCT.finditer(src):
        typ, body = m.group(1), m.group(2)
        lock, others = None, []
        for fm in FIELD.finditer(body):
            name, ftyp = fm.group(1), fm.group(2).strip()
            if name in ("struct", "interface") or ftyp.startswith("//"):
                continue
            if MUTEX.search(ftyp):
                lock = lock or name
            else:
                others.append(name)
        if lock and others:
            out.append({"kind": "field_access_under_lock", "dir": os.path.dirname(path),
                        "type": typ, "lock": lock, "candidate_fields": others})
    for c in sorted({m.group(1) for m in EXEC.finditer(src)}):
        out.append({"kind": "command_invocation_confined_to", "command": c,
                    "owner": os.path.dirname(path)})
    return out

files = sorted(f for f in subprocess.run(["git", "-C", ROOT, "ls-files", "*.go"],
                                         capture_output=True, text=True).stdout.split()
               if not f.endswith("_test.go"))
rows, selected = [], None
for f in files:
    q = questions(f)
    row = {"file": f, "expressible_relations": len(q), "relations": q}
    if not q:
        row["verdict"] = "NOT_EXPRESSIBLE"
    elif selected is None:
        row["verdict"], selected = "SELECTED", f
    else:
        row["verdict"] = "ELIGIBLE_NOT_FIRST"
    rows.append(row)

sha = subprocess.run(["git", "-C", ROOT, "rev-parse", "HEAD"],
                     capture_output=True, text=True).stdout.strip()
json.dump({"fixture": "github.com/golang/sync", "pinned_sha": sha,
           "predicate": __doc__, "selected": selected, "files": rows},
          open("experiments/coldstart-v1/fixture.json", "w"), indent=2)
for r in rows:
    print(f"  {r['verdict']:19s} {r['file']:34s} relations={r['expressible_relations']}")
    for q in r["relations"]:
        if q["kind"] == "field_access_under_lock":
            print(f"        {q['kind']}({q['type']}.<field> under {q['type']}.{q['lock']}) "
                  f"fields={q['candidate_fields']}")
        else:
            print(f"        {q['kind']}({q['command']!r} confined to {q['owner']})")
print(f"\nSELECTED: {selected}")
