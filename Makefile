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

BASH_TEST_TIMEOUT := 60

# Tests known to time out due to feature gaps we don't plan to implement:
#   coproc  — full coprocess support (bashy subshells are goroutines,
#             no kernel coproc pipes)
#   jobs    — job control / kernel job table (same goroutine constraint)
#   trap    — signal trap subset that requires the missing job control
# Skipping these saves ~60s each on every `make test-bash` run.
BASH_TEST_SKIP := coproc jobs trap

# Tests whose bash run-* helper strips lines starting with `expect ` from
# the captured output before diffing against the .right file. The
# convention is local to a handful of tests: most embed `expect` echoes
# directly in the .right file (so filtering them would break the diff).
BASH_TEST_FILTER_EXPECT := attr exp extglob extglob2 invert invocation more-exp new-exp nquote nquote1 nquote2 nquote3 nquote5 posix2 varenv

## test-bash: Run bash 5.3 native test suite against bashy (with per-test timeout)
test-bash: build test-bash-helpers
	@echo "Running bash 5.3 test suite against bashy ($(BASH_TEST_TIMEOUT)s timeout per test)..."
	@BASHY_ABS=$$(pwd)/$(BASHY); cd $(BASH_TESTS_DIR) && \
		export THIS_SH=$$BASHY_ABS && \
		export PATH=$$PWD:/usr/bin:/bin:/usr/local/bin && \
		export BASH_TSTOUT=$${TMPDIR:-/tmp}/bashy-tstout-$$$$ && \
		export BASH_TSTRAW=$${TMPDIR:-/tmp}/bashy-tstraw-$$$$ && \
		passed=0 && failed=0 && skipped=0 && timeout_count=0 && \
		for runner in run-*; do \
			[ "$$runner" = "run-all" ] && continue; \
			name=$${runner#run-}; \
			test_file="$$name.tests"; \
			right_file="$$name.right"; \
			if [ ! -f "$$test_file" ] || [ ! -f "$$right_file" ]; then \
				skipped=$$((skipped + 1)); \
				continue; \
			fi; \
			case " $(BASH_TEST_SKIP) " in \
				*" $$name "*) \
					skipped=$$((skipped + 1)); \
					printf "  SKIP  %s\n" "$$name"; \
					continue ;; \
			esac; \
			perl -e 'setpgrp; exec @ARGV' $$THIS_SH ./$$test_file >$$BASH_TSTRAW 2>&1 & \
			test_pid=$$!; \
			( sleep $(BASH_TEST_TIMEOUT) && kill -KILL -- -$$test_pid 2>/dev/null ) & \
			timer_pid=$$!; \
			wait $$test_pid 2>/dev/null; \
			rc=$$?; \
			kill -KILL -- -$$test_pid 2>/dev/null; \
			kill $$timer_pid 2>/dev/null; wait $$timer_pid 2>/dev/null; \
			case " $(BASH_TEST_FILTER_EXPECT) " in \
				*" $$name "*) \
					grep -av '^expect' <$$BASH_TSTRAW >$$BASH_TSTOUT 2>/dev/null || : ;; \
				*) \
					cp $$BASH_TSTRAW $$BASH_TSTOUT 2>/dev/null || : ;; \
			esac; \
			if [ $$rc -eq 137 ] 2>/dev/null; then \
				timeout_count=$$((timeout_count + 1)); \
				printf "  TIME  %s\n" "$$name"; \
			elif diff -q $$BASH_TSTOUT $$right_file > /dev/null 2>&1; then \
				passed=$$((passed + 1)); \
				printf "  PASS  %s\n" "$$name"; \
			else \
				failed=$$((failed + 1)); \
				printf "  FAIL  %s\n" "$$name"; \
			fi; \
			rm -f $$BASH_TSTOUT $$BASH_TSTRAW; \
		done; \
		echo ""; \
		echo "Results: $$passed passed, $$failed failed, $$skipped skipped, $$timeout_count timed out"; \
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
