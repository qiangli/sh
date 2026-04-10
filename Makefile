.PHONY: build tidy clean test test-bash test-bash-list help

BIN_DIR := bin
CMDS := bashy gosh shfmt
BASH_TESTS_DIR := external/bash-5.3/tests
BASHY := $(BIN_DIR)/bashy

## build: Build all commands into bin/
build:
	@mkdir -p $(BIN_DIR)
	@for cmd in $(CMDS); do \
		echo "building $$cmd..."; \
		go build -o $(BIN_DIR)/$$cmd ./cmd/$$cmd; \
	done

## test: Run all Go tests
test:
	go test ./...

## test-bash: Run bash 5.3 native test suite against bashy
test-bash: build test-bash-helpers
	@echo "Running bash 5.3 test suite against bashy..."
	@cd $(BASH_TESTS_DIR) && \
		export THIS_SH=$$(cd ../../.. && pwd)/$(BASHY) && \
		export PATH=$$PWD:$$PATH && \
		export BASH_TSTOUT=$${TMPDIR:-/tmp}/bashy-tstout-$$$$ && \
		passed=0 && failed=0 && skipped=0 && \
		for runner in run-*; do \
			[ "$$runner" = "run-all" ] && continue; \
			name=$${runner#run-}; \
			test_file="$$name.tests"; \
			right_file="$$name.right"; \
			if [ ! -f "$$test_file" ] || [ ! -f "$$right_file" ]; then \
				skipped=$$((skipped + 1)); \
				continue; \
			fi; \
			$$THIS_SH ./$$test_file > $$BASH_TSTOUT 2>&1; \
			if diff -q $$BASH_TSTOUT $$right_file > /dev/null 2>&1; then \
				passed=$$((passed + 1)); \
				printf "  PASS  %s\n" "$$name"; \
			else \
				failed=$$((failed + 1)); \
				printf "  FAIL  %s\n" "$$name"; \
			fi; \
			rm -f $$BASH_TSTOUT; \
		done; \
		echo ""; \
		echo "Results: $$passed passed, $$failed failed, $$skipped skipped"; \
		echo ""

## test-bash-list: List all available bash 5.3 tests
test-bash-list:
	@cd $(BASH_TESTS_DIR) && for runner in run-*; do \
		[ "$$runner" = "run-all" ] && continue; \
		echo "$${runner#run-}"; \
	done

## test-bash-helpers: Build helper programs needed by bash tests
test-bash-helpers:
	@cd $(BASH_TESTS_DIR) && \
		[ -f recho ] || cc -o recho ../support/recho.c 2>/dev/null; \
		[ -f zecho ] || cc -o zecho ../support/zecho.c 2>/dev/null; \
		true

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
