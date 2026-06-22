.PHONY: lsp install test fmt clean

# Build the language server into ./bin.
lsp:
	cd lsp && go build -ldflags "-X main.version=$(shell git describe --tags --always 2>/dev/null || echo dev)" -o ../bin/nexus-lsp ./nexus-lsp

# Install nexus-lsp onto your PATH ($(go env GOPATH)/bin) — what the extension needs.
install:
	cd lsp && go install ./nexus-lsp

test:
	cd lsp && go test ./...

fmt:
	cd lsp && gofmt -w .

clean:
	rm -rf bin target
