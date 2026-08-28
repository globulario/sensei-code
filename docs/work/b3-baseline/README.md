# B3 baseline — machine-bound

Measured on the **subject world**: sensei-code source `7ae7236e218480c0779a2960c01d41027e169e1b`
(main before any Phase-B control document existed), checked out detached, with
`sensei preflight -addr localhost:10122 --domain github.com/globulario/sensei-code --mode compact -json`
per S1 surface, and `sensei metadata -json` for the graph's identity at the
same moment. Producer binary: sensei `f79f96f9` (the E3 instrument). These
files are the baseline; the prose in `../b3-self-grounding.md` quotes them.

```
graph.metadata.json                                  sha256 31572b44bb14cdee
internal_derived_derived.preflight.json              sha256 22b52a9bf73f57bf
internal_session_store.preflight.json                sha256 1095a18a2be4ded2
internal_workflow_engine.preflight.json              sha256 05ecc23860353d9e
internal_workflow_premise.preflight.json             sha256 00d43da623d8f698
internal_workflow_prospective.preflight.json         sha256 7940692d11056894
internal_workflow_suppliedplan.preflight.json        sha256 7411d1ca3a2096cd
internal_workflow_testedit.preflight.json            sha256 128c38e2b9470e1c
```
