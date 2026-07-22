.PHONY: assets build check dev fmt npm-audit staticcheck test test-race vet vuln

assets:
	npm run build:assets
	npm run check:assets

build: assets
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/blog ./cmd/blog

dev: assets
	go run ./cmd/blog

fmt:
	find cmd content internal web -name '*.go' -print0 | xargs -0 gofmt -w

npm-audit:
	npm audit --audit-level=moderate

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...

check: assets npm-audit vet staticcheck test test-race vuln build
