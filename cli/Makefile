VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS  = -ldflags "-X github.com/momhq/mom/cli/internal/cmd.Version=$(VERSION) -X github.com/momhq/mom/cli/internal/cmd.Commit=$(COMMIT)"

.PHONY: build test lint clean install brew-audit

build:
	go build $(LDFLAGS) -o bin/mom ./cmd/mom

install:
	@go install $(LDFLAGS) ./cmd/mom
	@GOBIN=$$(go env GOPATH)/bin; \
	echo "mom installed to $$GOBIN/mom"; \
	if ! echo "$$PATH" | tr ':' '\n' | grep -q "$$GOBIN"; then \
		echo ""; \
		echo "  WARNING: $$GOBIN is not in your PATH."; \
		echo "  Add this to your shell profile (~/.zshrc or ~/.bashrc):"; \
		echo ""; \
		echo "    export PATH=\"$$GOBIN:\$$PATH\""; \
		echo ""; \
		echo "  Then restart your terminal or run: source ~/.zshrc"; \
	fi

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

brew-audit:
	brew audit --formula ../Formula/mom.rb

# Cross-compilation targets
.PHONY: build-all
build-all:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o bin/mom-darwin-arm64 ./cmd/mom
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o bin/mom-darwin-amd64 ./cmd/mom
	GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o bin/mom-linux-arm64  ./cmd/mom
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o bin/mom-linux-amd64  ./cmd/mom
