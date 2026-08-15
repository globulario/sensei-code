.PHONY: build test fmt

build:
	go build ./cmd/sensei-code

test:
	go test ./...

fmt:
	gofmt -w cmd internal
