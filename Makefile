BINARY := clash-sub
LDFLAGS := -s -w

.PHONY: all run build clean vendor dist setup

all: build

# One-shot after cloning: resolve modules, fetch the editor libraries.
setup: go.sum vendor

go.sum: go.mod
	go mod tidy

vendor: web/assets/vendor/codemirror.min.js

web/assets/vendor/codemirror.min.js:
	./scripts/fetch-vendor.sh

build: go.sum vendor
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) .

run: go.sum vendor
	CLASH_SUB_DEBUG=1 go run . -listen :8088

# Everything is embedded, so each of these is a single self-contained file.
dist: go.sum vendor
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-darwin-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-windows-amd64.exe .
	@ls -lh dist

clean:
	rm -rf $(BINARY) dist
