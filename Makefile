.PHONY: build tidy clean test test-quick test-full bashpp-race-gate help fmtcheck hooks

BIN_DIR := bin
CMDS := gosh shfmt

## build: Build all commands into bin/
build:
	@mkdir -p $(BIN_DIR)
	@for cmd in $(CMDS); do \
		echo "building $$cmd..."; \
		go build -o $(BIN_DIR)/$$cmd ./cmd/$$cmd; \
	done

## test: The quick tier (alias of test-quick) — what push CI runs
test: test-quick

## test-quick: Build + unit/mock tests: go test -short, without the `//go:build full` evaluator files (< 3 min)
test-quick:
	@/bin/bash ./scripts/test-quick.sh

## test-full: Every Go test (-tags full), including the toolchain-driven evaluator and external-shell tests
test-full:
	go test -tags full -timeout=30m ./...
	cd moreinterp && go test -timeout=30m ./...

## bashpp-race-gate: Run the Bash++ race/lifecycle gate and write local evidence
bashpp-race-gate:
	@/bin/bash ./scripts/bashpp-race-gate.sh

## tidy: Run go mod tidy, gofmt, and go vet
tidy:
	go mod tidy
	gofmt -s -w .
	go vet ./...

## clean: Remove built binaries
clean:
	rm -rf $(BIN_DIR)

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':'

fmtcheck:  ## gofmt gate — reports unformatted files, never rewrites them
	@./scripts/fmtcheck.sh

hooks:  ## install the pre-push formatting gate
	@git config core.hooksPath scripts/hooks
	@echo "hooks installed: core.hooksPath=scripts/hooks"
