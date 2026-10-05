.PHONY: help build test vet fmt check docs docs-start docs-stop docs-status docs-setup docs-logs

BIN := .local/bin/docs-viewer
DOCS_ADDR ?= 127.0.0.1:7900

## help: Show the targets
help:
	@awk '/^## /{sub(/^## /, ""); print}' $(MAKEFILE_LIST)

## build: Compile the command into .local/bin
build:
	@go build -o $(BIN) ./cmd/docs-viewer

## test: Run the tests
test:
	go test ./...

## vet: Run go vet
vet:
	go vet ./...

## fmt: Format the Go files
fmt:
	gofmt -w .

## check: Run the checks for a commit
check: vet test
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

## docs: Browse docs/ and api/ on DOCS_ADDR, live reload
## docs-start: Start it in the background (.local/logs/docs.log)
## docs-stop: Stop the background viewer
## docs-status: Show whether it runs, and its URL
## docs-setup: Fetch the pinned Mermaid, KaTeX, and Redoc assets
# A built binary, not `go run`: `docs-start` executes it again, detached.
docs docs-start docs-stop docs-status docs-setup: build
	@$(BIN) -addr $(DOCS_ADDR) -vaults docs,api -spec-vault api $(if $(filter docs,$@),run,$(@:docs-%=%))

## docs-logs: Follow the log of the background viewer
docs-logs:
	@tail -n 50 -F .local/logs/docs.log
