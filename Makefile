.PHONY: build test vet clean lint fmt fmt-check e2e smoke tools

BINARY      := bin/cmux-notify-bridge
GOLANGCI    := golangci-lint
GOFUMPT     := gofumpt
GOFUMPT_VER := v0.7.0
LINT_VER    := v2.0.2

build:
	go build -o $(BINARY) ./cmd/cmux-notify-bridge

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint:
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { \
	  echo "golangci-lint not found. Run 'make tools' to install it."; exit 1; }
	$(GOLANGCI) run

fmt:
	@command -v $(GOFUMPT) >/dev/null 2>&1 || { \
	  echo "gofumpt not found. Run 'make tools' to install it."; exit 1; }
	$(GOFUMPT) -w .

fmt-check:
	@command -v $(GOFUMPT) >/dev/null 2>&1 || { \
	  echo "gofumpt not found. Run 'make tools' to install it."; exit 1; }
	@diff=$$( $(GOFUMPT) -l . ); \
	if [ -n "$$diff" ]; then \
	  echo "gofumpt would rewrite:"; echo "$$diff"; \
	  $(GOFUMPT) -d .; \
	  exit 1; \
	fi

tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VER)
	go install mvdan.cc/gofumpt@$(GOFUMPT_VER)

# `make e2e` is an alias for the Go integration test file; `make smoke`
# is the shell-level smoke that boots the real binary and uses curl.
e2e:
	go test -race -count=1 -run '^TestE2E' ./internal/server/...

smoke: build
	bash scripts/e2e.sh

clean:
	rm -rf bin dist
