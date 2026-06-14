.PHONY: build tidy clean test help

BIN_DIR := bin
CMDS := gosh shfmt

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
