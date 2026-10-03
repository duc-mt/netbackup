BINARY  := netbackup
LDFLAGS := -s -w

.PHONY: build test test-race vet fmt vendor cross clean

# Single static binary, no cgo, dependencies taken from ./vendor (works offline).
build:
	CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

test:
	CGO_ENABLED=0 go test -mod=vendor ./...

# The race detector needs cgo, so it is a separate target.
test-race:
	CGO_ENABLED=1 go test -mod=vendor -race ./...

vet:
	go vet -mod=vendor ./...

fmt:
	gofmt -l -w main.go internal

# Run once on a machine with internet access, then commit/copy ./vendor into the air gap.
vendor:
	go mod tidy
	go mod vendor

cross:
	@for t in linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ $$os = windows ] && ext=".exe"; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -mod=vendor -trimpath -ldflags '$(LDFLAGS)' \
			-o bin/$(BINARY)-$$os-$$arch$$ext . || exit 1; \
	done

clean:
	rm -f bin/$(BINARY)
