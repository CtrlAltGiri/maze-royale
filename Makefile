GOFLAGS := -mod=vendor
export GOFLAGS

.PHONY: run test vet build fmt vendor demo-up demo-down demo-status

## run: start all five services with prefixed logs (http://localhost:8090)
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

## demo-up: start/reuse the stack behind an allowlist proxy and print a public demo URL
demo-up:
	bash scripts/demo-tunnel.sh up

## demo-down: stop the tunnel, proxy and any services demo-up started
demo-down:
	bash scripts/demo-tunnel.sh down

## demo-status: show demo tunnel state, URL and health checks
demo-status:
	bash scripts/demo-tunnel.sh status

## vendor: refresh vendor/ after changing go.mod (requires network)
vendor:
	GOFLAGS= go mod tidy && GOFLAGS= go mod vendor
