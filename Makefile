GOFLAGS := -mod=vendor
export GOFLAGS

.PHONY: run test vet build fmt vendor

## run: start all five services with prefixed logs (http://localhost:8080)
run:
	go run ./cmd/devrun

## test: run every package's tests (no network, no running server required)
test:
	go test ./...

## vet: static checks
vet:
	go vet ./...

## build: compile every binary into ./bin
build:
	mkdir -p bin
	go build -o bin ./cmd/...

fmt:
	gofmt -l -w cmd internal

## vendor: refresh vendor/ after changing go.mod (requires network)
vendor:
	GOFLAGS= go mod tidy && GOFLAGS= go mod vendor
