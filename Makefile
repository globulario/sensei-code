.PHONY: build test fmt symbol-index

build:
	go build ./cmd/sensei-code

test:
	go test ./...

fmt:
	gofmt -w cmd internal

# Regenerate docs/awareness/generated/ — the symbol index that puts this
# repository's own files inside the graph's observation surface.
#
# Without it, `sensei preflight` on an un-anchored file reports "no files
# examined" (Absent: the graph never looked) rather than "examined, no
# governing rule applies" (EmptyProven). Only the second is true here.
#
# This adds examination, never governance. Anchors come from the authored
# corpus alone; nothing here decides that a rule applies.
#
# Test files are INCLUDED, unlike Sensei's own index.
#
# Sensei passes --exclude-tests because its curated, annotation-derived
# code_symbols corpus already models test symbols and would collide. This
# repository has no such corpus, and excluding tests here had a cost that
# showed up immediately: a plan naming a source file and its test returned
# PREFLIGHT_STATUS_DEGRADED -- "only 1 of 2 requested file(s) are examined" --
# which the authority router reads as cannot-establish, so the run died before
# it began. No change that touches a test could be governed, and this
# repository requires a test with every change.
#
# Two ids do collide: required_tests.yaml names two doctor tests by the same
# path:symbol shape scip-ingest mints. They coexist -- a test function is both
# a code symbol and a required test -- and `sensei check` validates the result.
SCIP_GO ?= go run github.com/scip-code/scip-go/cmd/scip-go@latest
symbol-index:
	$(SCIP_GO) index --output index.scip
	sensei scip-ingest --scip index.scip --out docs/awareness/generated
	mv docs/awareness/generated/code_symbols.yaml docs/awareness/generated/awareness_graph_scip_symbols.yaml
	mv docs/awareness/generated/code_references.yaml docs/awareness/generated/awareness_graph_scip_references.yaml
	rm -f index.scip
	sensei check
