# Contributing to prox

## Requirements

- Go 1.25 or later. Release builds use Go 1.27.1.
- `golangci-lint` at the version pinned by CI.
- Git and Make.

The repository contains three Go modules: the root application, `sdk`, and `examples/plugin-auth`. Run checks in each affected module.

## Setup

```bash
git clone https://github.com/dortanes/prox.git
cd prox
go mod download
(cd sdk && go mod download)
(cd examples/plugin-auth && go mod download)
make build
./prox validate -config example.json5
```

## Required checks

Run these checks before opening a pull request:

```bash
test -z "$(gofmt -l .)"
make test
make vet
make lint
make validate
(cd sdk && go test -race ./... && go vet ./...)
(cd examples/plugin-auth && go test -race ./... && go vet ./...)
```

Add or update tests for behavior changes. Keep tests deterministic and avoid external network dependencies.

## Changes

- Use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) such as `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `perf:`, `build:`, `ci:`, and `chore:`. Add `!` and a `BREAKING CHANGE:` footer for incompatible changes.
- Preserve compatibility for exported Go APIs unless the change is explicitly breaking. Update SDK tests and plugin examples when the plugin protocol or SDK changes.
- Update validation, tests, `example.json5`, and the configuration reference when adding or changing a configuration field.
- Update README and relevant files under `docs/` when behavior, commands, defaults, or operational constraints change.
- Keep examples free of real credentials, private addresses, customer data, and machine-specific paths.

## Generated files

No generated source files are currently committed. If generated files are introduced, commit the generator or schema and document the exact regeneration command. Regenerate files from their source; do not edit generated output by hand. Do not commit binaries, coverage reports, local configuration, or generated documentation output.

## Pull requests

Keep each pull request focused. Describe the user-visible effect, note compatibility implications, and list the commands used for validation. Redact credentials and private infrastructure from configs and logs.

## v1.0.0 release process

The application and SDK are versioned independently. The root module uses `v1.0.0`; the nested SDK module uses `sdk/v1.0.0`. Both tags point to the same verified commit.

From a clean checkout of `main`:

```bash
git switch main
git pull --ff-only origin main
test -z "$(git status --porcelain)"

make build VERSION=v1.0.0
make test
make vet
make lint
make validate
(cd sdk && go test -race ./... && go vet ./...)
(cd examples/plugin-auth && go test -race ./... && go vet ./...)

git tag -a sdk/v1.0.0 -m "sdk v1.0.0"
git tag -a v1.0.0 -m "prox v1.0.0"
git push origin sdk/v1.0.0
git push origin v1.0.0
```

Push the SDK tag first so `github.com/dortanes/prox/sdk@v1.0.0` is available before the application release. The `v1.0.0` tag starts the release workflow, which publishes application archives, checksums, and container images. The SDK tag publishes the Go module only.

After the workflow completes, verify the published artifacts from a clean environment:

```bash
go install github.com/dortanes/prox/cmd/prox@v1.0.0
prox version
go list -m github.com/dortanes/prox/sdk@v1.0.0
docker pull ghcr.io/dortanes/prox:1.0.0
```
