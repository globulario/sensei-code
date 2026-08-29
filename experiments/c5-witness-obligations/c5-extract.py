#!/usr/bin/env python3
"""Mechanical extraction from the governor's own event log (obligation 6).

Preserves EVERY reviewer attempt, not just the last one. #122 made the hosted
lane fall back from Codex to Gemini; a run in which one provider fails and
another delivers must record both, or the record says a provider reviewed
when a different one did.

usage: c5-extract.py <log> <receipts-out>
"""
import json, sys

log, rec = sys.argv[1], sys.argv[2]
task_id = ""
attempts = []          # ordered trail of every agent./review. event
providers = []         # every provider named by any of them, in order
receipts = []
resolved = ""
final_verdict = final_provider = final_digest = ""

for line in open(log, errors="replace"):
    line = line.strip()
    if not line.startswith("{"):
        continue
    try:
        d = json.loads(line)
    except Exception:
        continue
    k = d.get("kind", "")
    p = d.get("payload") or {}
    if not isinstance(p, dict):
        p = {}
    prov = p.get("provider", "") or (p.get("provenance") or {}).get("provider", "")
    if k == "task.created":
        task_id = d.get("task_id", "")
    if k.startswith("agent.") or k.startswith("review."):
        attempts.append("%s provider=%s %s" % (k, prov or "-", str(d.get("summary", ""))[:120]))
        if prov and prov not in providers:
            providers.append(prov)
    if k == "review.completed":
        pv = p.get("provenance") or {}
        final_verdict = p.get("decision", "") or final_verdict
        final_provider = prov or final_provider
        final_digest = pv.get("candidate_digest", "") or final_digest
    if k == "candidate.resolved":
        resolved = json.dumps(p)
    if "receipt" in k or k.startswith("derive"):
        receipts.append(line)

with open(rec, "w") as f:
    f.writelines(r + "\n" for r in receipts)

print("task id " + task_id)
print("reviewer attempts %d" % len(attempts))
print("reviewer providers attempted " + (", ".join(providers) or "NONE"))
print("reviewer provider " + (final_provider or "NONE -- no bounded reviewer identity recorded"))
print("reviewer verdict " + (final_verdict or "NONE -- UNREVIEWED, never clean by exhaustion"))
print("reviewer reviewed digest " + (final_digest or "NONE"))
print("candidate resolution " + (resolved or "NONE"))
print("reviewer trail:")
for a in attempts:
    print("  " + a)
