.PHONY: run demo test race build fmt vet

run:
	go run ./cmd/aiosight

demo:
	AIOSIGHT_DEMO=1 go run ./cmd/aiosight

test:
	go test ./...

race:
	go test -race ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/aiosight ./cmd/aiosight

fmt:
	gofmt -w .

vet:
	go vet ./...
