# External witness size — a maturity metric, frozen before optimisation

How much machinery *outside* Sensei is needed to establish trust in one
governed run? Not a correctness measure. Fewer lines are not more correct.
It measures whether the system carries its own evidence or makes the
laboratory reconstruct it.

## C5 baseline (commit 9f48806, measured before any run)

```text
c5-capture.sh      71   isolation capture + falsifier gate   (IRREDUCIBLE: the
                                                              governed process
                                                              cannot witness its
                                                              own environment)
c5-closure.sh     119   instrument completeness gate
c5-run.sh         218   materialisation, pinning, orchestration, classification
c5-validate.sh    203   the harness's own validation gate (35 cases)
c5-extract.py      62   reviewer/candidate extraction from the event log
                  ---
                  673   external lines
```

## Target

Once sensei-code emits a governed-run receipt, the external witness should
reduce to: pin, materialise, **isolation capture**, invoke, verify the
receipt by re-derivation, adjudicate. Order of magnitude ~100 lines.

The isolation capture never moves inside. A governed process cannot be the
sole witness that its own environment was isolated; a subject with a live
remote or an alternates file would report itself clean, because the
compromised thing is doing the reporting.

The metric only means something if the evidence standard holds or improves
while the number falls. A smaller witness that checks less is not progress.

*Not part of the C5 evidence record.*
