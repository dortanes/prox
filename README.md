# prox

prox is a single-binary reverse proxy for HTTP and raw TCP traffic. JSON5 configuration defines listeners, routing, upstream actions, TLS, and optional plugin middleware.

[![CI](https://github.com/dortanes/prox/actions/workflows/ci.yml/badge.svg)](https://github.com/dortanes/prox/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/dortanes/prox.svg)](https://pkg.go.dev/github.com/dortanes/prox)
[![Release](https://img.shields.io/github/v/release/dortanes/prox?logo=github&color=blue)](https://github.com/dortanes/prox/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[Documentation](https://dortanes.github.io/prox) · [Getting started](https://dortanes.github.io/prox/getting-started) · [Configuration](https://dortanes.github.io/prox/configuration/) · [Plugins](https://dortanes.github.io/prox/plugins/) · [Deployment](https://dortanes.github.io/prox/deployment)

## Capabilities

- Route HTTP traffic by domain, path, method, and headers.
- Combine HTTP routing and SNI-based TCP pass-through on one listener.
- Balance upstream traffic with round-robin, random, or least-connections strategies.
- Extend routing with external plugins for authorization, response changes, and target discovery.
- Terminate TLS with supplied certificates or manage certificates through ACME.

## Installation

prox requires Go 1.25 or later.

Install the v1.0.0 release:

```bash
go install github.com/dortanes/prox/cmd/prox@v1.0.0
prox version
```

Build the same release from source:

```bash
git clone https://github.com/dortanes/prox.git
cd prox
git checkout v1.0.0
make build VERSION=v1.0.0
./prox version
```

Release archives and container images are available from [GitHub Releases](https://github.com/dortanes/prox/releases) and `ghcr.io/dortanes/prox:1.0.0`.

## Configuration

Create `config.json5`:

```json5
{
  services: {
    web: {
      listen: ":8080",
      routes: [
        { match: { path: "/*" }, action: "api" },
      ],
    },
  },
  actions: {
    api: { type: "proxy", upstream: "localhost:3000" },
  },
}
```

Every service requires a `listen` address and at least one route. See the [configuration reference](https://dortanes.github.io/prox/configuration/) for actions, routing, TLS, timeouts, and split configuration files.

Validate the configuration, then start the proxy:

```bash
prox validate -config config.json5
prox serve -config config.json5
```

Configuration files are watched by default. Valid changes are applied atomically; rejected reloads are logged and the last valid configuration remains active.

## Performance

The table is a local snapshot from an Apple M4 Pro running HTTP/1.1 without TLS. Results vary by hardware, operating system, proxy versions, and configuration.

| Proxy    |      Req/s |         Avg |         P99 |
| -------- | ---------: | ----------: | ----------: |
| HAProxy  |     90,080 |     2.78 ms |     4.12 ms |
| **prox** | **88,643** | **2.87 ms** | **3.96 ms** |
| Nginx    |     87,768 |     2.90 ms |     3.74 ms |
| Traefik  |     82,737 |     3.08 ms |     5.84 ms |

`bash bench/run.sh` builds the current checkout and records the best of three runs using `wrk -t4 -c256 -d10s`. Record the commit and tool versions when publishing new results.

## Development

```bash
make build
make test
make vet
make lint
make validate
(cd sdk && go test -race ./... && go vet ./...)
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution and release requirements, [SECURITY.md](SECURITY.md) for private vulnerability reporting, and [LICENSE](LICENSE) for the MIT license.
