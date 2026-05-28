# Contributing to prox

Thanks for your interest in contributing!

## Development

```bash
# Clone
git clone https://github.com/dortanes/prox.git
cd prox

# Build
make build

# Run tests
make test

# Run linter
make lint

# Generate coverage report
make cover
```

## Submitting Changes

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/my-change`)
3. Write tests for your changes
4. Ensure `make test` and `make vet` pass
5. Submit a pull request

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Add doc comments to all exported types and functions
- Keep packages small and focused
- Zero external dependencies where possible — prefer stdlib

## Reporting Bugs

Open an issue with:
- prox version (`prox version`)
- Your config (redacted if sensitive)
- Expected vs actual behavior
- Relevant log output (`-log-level debug`)

## Architecture

```
config.json5 ─┐
  web.json5 ──┤ Load + Merge → Validate → Build Router + Actions + Dispatcher → Start Listeners
  api.json5 ──┘                                                          │
                                                           File watcher / SIGHUP
                                                                         │
                                                            Reload → Validate → Atomic swap
```

```
prox/
├── cmd/prox/           CLI entrypoint (serve, validate, build)
├── internal/
│   ├── config/         Config types, loader, validator
│   ├── server/         HTTP(S) lifecycle, hot reload
│   ├── dispatcher/     L4 TCP dispatching (SNI peek, pass-through relay)
│   ├── router/         L7 domain + path + method matching
│   ├── action/         Handlers: proxy, static, serve, pass
│   ├── balancer/       Load balancing strategies (round-robin, random, least-conn)
│   ├── plugin/         Plugin lifecycle and IPC (stdin/stdout, Unix sockets)
│   ├── throttle/       Speed limiting (per-connection and shared budgets)
│   ├── logger/         Structured logging, colorized console output
│   ├── resource/       Content resolver
│   └── watcher/        File change detection (polling)
├── sdk/                Plugin SDK — separate Go module for building plugins
├── Dockerfile
├── Makefile
└── go.mod
```

