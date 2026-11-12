VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet fmt dist clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o gofer ./cmd/gofer

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Cross-compile all release targets into dist/ (in parallel).
dist:
	@mkdir -p dist
	@for t in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64 windows/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ $$os = windows ] && ext=".exe"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/gofer_$${os}_$${arch}$$ext ./cmd/gofer & \
	done; wait
	@cd dist && shasum -a 256 gofer_* > checksums.txt
	@ls -lh dist

clean:
	rm -rf gofer dist
