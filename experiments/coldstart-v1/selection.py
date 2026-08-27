#!/usr/bin/env python3
"""Deterministic, pre-declared subject selection for the cold-start experiment.

Frozen BEFORE any encounter is run and before any outcome is inspected.

PREDICATE
  Select the FIRST package, in lexicographic order of package directory, that is
  (a) outside the 11-task benchmark corpus,
  (b) not internal/event (holds the only pre-existing recipe),
  (c) genuinely uncovered by the awareness graph, and
  (d) expressible in the ALREADY FROZEN recipe vocabulary --
      field_access_under_lock or command_invocation_confined_to.

Nothing about benchmark outcomes enters this. (d) is checked because the
experiment tests the CLOSURE LOOP, not arbitrary repository understanding: a
package the frozen vocabulary cannot express would measure the vocabulary's
narrowness instead. If NO package satisfies (d), that is itself the finding --
the vocabulary is too narrow -- and no vocabulary may be added in response.
"""
import ast, json, os, re, subprocess, sys
from collections import defaultdict

CORPUS = {"internal/agent","internal/architect","internal/assist","internal/decision",
          "internal/gitx","internal/session","internal/setup","internal/tui"}
EXCLUDE = CORPUS | {"internal/event"}

def packages():
    files = subprocess.run(["git","ls-files","*.go"],capture_output=True,text=True).stdout.split()
    pkgs = defaultdict(list)
    for f in files:
        if f.endswith("_test.go"): continue
        pkgs[os.path.dirname(f)].append(f)
    return dict(sorted(pkgs.items()))

MUTEX = re.compile(r'\bsync\.(RW)?Mutex\b')
# A struct carrying a mutex AND at least one other field poses a
# field_access_under_lock question: does that field move only under that lock?
STRUCT = re.compile(r'type\s+(\w+)\s+struct\s*\{(.*?)\n\}', re.S)
FIELD  = re.compile(r'^\s*(\w+)\s+([^\s/][^\n]*)$', re.M)

def lock_questions(files):
    out=[]
    for f in files:
        src=open(f,errors="ignore").read()
        for m in STRUCT.finditer(src):
            typ,body=m.group(1),m.group(2)
            lock=None; others=[]
            for fm in FIELD.finditer(body):
                name,typ_=fm.group(1),fm.group(2).strip()
                if name in ("struct","interface"): continue
                if MUTEX.search(typ_): lock=lock or name
                elif not typ_.startswith("//"): others.append(name)
            if lock and others:
                out.append({"kind":"field_access_under_lock","type":typ,"lock":lock,
                            "fields":others[:6],"file":f})
    return out

EXEC = re.compile(r'exec\.Command(?:Context)?\(\s*(?:ctx\s*,\s*)?"([^"]+)"')
def command_questions(files):
    cmds=set()
    for f in files:
        for m in EXEC.finditer(open(f,errors="ignore").read()):
            cmds.add(m.group(1))
    return [{"kind":"command_invocation_confined_to","command":c} for c in sorted(cmds)]

def covered_names():
    blob=""
    for root,_,fs in os.walk("docs/awareness"):
        for f in fs:
            if f.endswith((".yaml",".yml")):
                blob+=open(os.path.join(root,f),errors="ignore").read()
    return blob

def main():
    blob=covered_names()
    rows=[]
    chosen=None
    for pkg,files in packages().items():
        if pkg in EXCLUDE:
            rows.append({"package":pkg,"verdict":"EXCLUDED",
                         "why":"benchmark corpus" if pkg in CORPUS else "holds the only pre-existing recipe"})
            continue
        covered=[f for f in files if f in blob]
        lq=lock_questions(files); cq=command_questions(files)
        row={"package":pkg,"files":len(files),"files_named_in_graph":len(covered),
             "lock_questions":len(lq),"command_questions":len(cq)}
        if covered:
            row["verdict"]="NOT_UNCOVERED"; row["why"]=f"{len(covered)} file(s) named in awareness sources"
        elif not lq and not cq:
            row["verdict"]="NOT_EXPRESSIBLE"
            row["why"]="no field_access_under_lock or command_invocation_confined_to relation found"
        else:
            row["verdict"]="ELIGIBLE"
            row["example_questions"]=(lq[:2]+cq[:2])
            if chosen is None:
                chosen=pkg; row["verdict"]="SELECTED"
        rows.append(row)
    json.dump({"predicate":__doc__,"ordering":"lexicographic by package directory",
               "selected":chosen,"candidates":rows},
              open("experiments/coldstart-v1/selection.json","w"),indent=2)
    for r in rows:
        if r["verdict"] in ("SELECTED","ELIGIBLE"):
            print(f"  {r['verdict']:16s} {r['package']}  files={r.get('files')} "
                  f"lockQ={r.get('lock_questions')} cmdQ={r.get('command_questions')}")
    print(f"\nSELECTED: {chosen}")
    print(f"eligible: {sum(1 for r in rows if r['verdict'] in ('ELIGIBLE','SELECTED'))}, "
          f"excluded: {sum(1 for r in rows if r['verdict']=='EXCLUDED')}, "
          f"covered: {sum(1 for r in rows if r['verdict']=='NOT_UNCOVERED')}, "
          f"inexpressible: {sum(1 for r in rows if r['verdict']=='NOT_EXPRESSIBLE')}")
main()

# ---------------------------------------------------------------------------
# CORRECTION, recorded before any encounter was run and before any outcome was
# observed. Two defects in the predicate above, both about the SETUP and neither
# about a result:
#
#   1. GRANULARITY. The refusal being studied triggers per FILE -- "only 1 of 2
#      requested file(s) are examined in the graph". The predicate selects per
#      PACKAGE. The observed case, internal/setup/checks.go, is a covered
#      package containing an unknown file, which a package-level predicate can
#      never select.
#
#   2. THE INSTRUMENT. Both eligible packages, cmd/proofbench and
#      internal/proofbench, were first committed 2026-08-24 -- by this campaign,
#      for this campaign. They are uncovered because they are NEW, not because
#      they are an un-indexed part of the architecture. Investigating them
#      measures the measuring instrument.
#
# Neither correction was chosen for the subject it would select. Both are
# refusals of a subject that cannot reproduce the condition under study.
