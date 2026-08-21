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
# --exclude-tests drops *_test.go symbols, which the authored corpus already
# models as Test nodes.
SCIP_GO ?= go run github.com/scip-code/scip-go/cmd/scip-go@latest
symbol-index:
	$(SCIP_GO) index --output index.scip
	sensei scip-ingest --scip index.scip --exclude-tests --out docs/awareness/generated
	mv docs/awareness/generated/code_symbols.yaml docs/awareness/generated/awareness_graph_scip_symbols.yaml
	mv docs/awareness/generated/code_references.yaml docs/awareness/generated/awareness_graph_scip_references.yaml
	rm -f index.scip
	sensei check
