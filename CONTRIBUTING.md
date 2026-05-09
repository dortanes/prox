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
