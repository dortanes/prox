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

## Release process

Release Please maintains one release pull request from Conventional Commits pushed to `main`. Subsequent releasable commits update the same pull request. Merge it when the accumulated changes are ready to publish; do not create release tags manually.

The application and SDK share one version because the SDK protocol and API must match the application. A merged release pull request creates `vX.Y.Z` for the root module and `sdk/vX.Y.Z` for the nested SDK module at the same commit. The first release is `1.0.0`.

Merging the release pull request creates both GitHub Releases and runs the release workflow for the application tag. The workflow verifies every Go module, publishes application archives and checksums, and pushes `ghcr.io/dortanes/prox:X.Y.Z` and `latest`. The SDK tag publishes the Go module without separate artifacts.

After the workflow completes, verify the published artifacts from a clean environment, replacing `1.0.0` with the released version:

```bash
go install github.com/dortanes/prox/cmd/prox@v1.0.0
prox version
go list -m github.com/dortanes/prox/sdk@v1.0.0
docker pull ghcr.io/dortanes/prox:1.0.0
```
