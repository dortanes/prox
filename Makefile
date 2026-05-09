.PHONY: build test lint vet clean

VERSION ?= dev
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o prox ./cmd/prox

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

clean:
	rm -f prox coverage.out coverage.html

validate: build
	./prox validate -config example.json5
