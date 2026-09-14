.PHONY: build build-all check clean cover fmt-check lint test tidy-check validate verify vet

GO ?= go
GOLANGCI_LINT ?= golangci-lint
VERSION ?= dev
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

build:
	$(GO) build $(LDFLAGS) -o prox ./cmd/prox

build-all: build
	$(GO) build ./...
	cd sdk && $(GO) build ./...
	mkdir -p dist
	cd examples/plugin-auth && $(GO) build -o ../../dist/plugin-auth .

test:
	$(GO) test -race -count=1 ./...
	cd sdk && $(GO) test -race -count=1 ./...
	cd examples/plugin-auth && $(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...
	cd sdk && $(GO) vet ./...
	cd examples/plugin-auth && $(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run --config=.golangci.yml ./...
	cd sdk && $(GOLANGCI_LINT) run --config=../.golangci.yml ./...
	cd examples/plugin-auth && $(GOLANGCI_LINT) run --config=../../.golangci.yml ./...

fmt-check:
	test -z "$$(gofmt -l .)"

tidy-check:
	$(GO) mod tidy -diff
	cd sdk && $(GO) mod tidy -diff
	cd examples/plugin-auth && $(GO) mod tidy -diff

verify:
	$(GO) mod verify
	cd sdk && $(GO) mod verify
	cd examples/plugin-auth && $(GO) mod verify

check: fmt-check tidy-check verify test vet build-all lint

cover:
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

clean:
	rm -f prox coverage.out coverage.html

validate: build
	./prox validate -config example.json5
