BINARY  := netbackup
LDFLAGS := -s -w

.PHONY: build test test-race vet fmt lint vendor cross clean init

# Single static binary, no cgo, dependencies taken from ./vendor (works offline).
build:
	CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/netbackup

test:
	CGO_ENABLED=0 go test -mod=vendor ./...

# The race detector needs cgo, so it is a separate target.
test-race:
	CGO_ENABLED=1 go test -mod=vendor -race ./...

vet:
	go vet -mod=vendor ./...

fmt:
	gofmt -l -w cmd internal

GOLANGCI_LINT := $(shell which golangci-lint 2>/dev/null || echo $(shell go env GOPATH 2>/dev/null)/bin/golangci-lint)

lint:
	@if [ -x "$$(command -v $(GOLANGCI_LINT) 2>/dev/null)" ] || [ -f "$(GOLANGCI_LINT)" ]; then \
		$(GOLANGCI_LINT) run ./...; \
	else \
		echo "golangci-lint not found. Running go vet and checking formatting..."; \
		unformatted=$$(gofmt -l cmd internal); \
		if [ -n "$$unformatted" ]; then \
			echo "The following files are not formatted with gofmt:"; echo "$$unformatted"; exit 1; \
		fi; \
		go vet -mod=vendor ./...; \
		echo "Format and vet passed. Run 'go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2' for full linting."; \
	fi

# Run once on a machine with internet access, then commit/copy ./vendor into the air gap.
vendor:
	go mod tidy
	go mod vendor

cross:
	@for t in linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ $$os = windows ] && ext=".exe"; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -mod=vendor -trimpath -ldflags '$(LDFLAGS)' \
			-o bin/$(BINARY)-$$os-$$arch$$ext ./cmd/netbackup || exit 1; \
	done

clean:
	rm -rf bin

init:
	@echo "Initializing configuration files from examples/..."
	@if [ ! -f .env ]; then \
		cp examples/env.example .env && chmod 600 .env && echo "  [+] Created .env (chmod 600)"; \
	else \
		echo "  [.] .env already exists, skipping"; \
	fi
	@if [ ! -f inventory.csv ]; then \
		cp examples/inventory.csv inventory.csv && echo "  [+] Created inventory.csv"; \
	else \
		echo "  [.] inventory.csv already exists, skipping"; \
	fi
	@echo "Initialization complete! Edit .env and inventory.csv to match your environment."


run: build
	./bin/$(BINARY)
