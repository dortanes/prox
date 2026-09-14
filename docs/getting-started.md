# Getting Started

Install prox, create a minimal configuration, and start proxying traffic.

## Installation

Download the latest archive for your operating system and architecture from [GitHub Releases](https://github.com/dortanes/prox/releases/latest). Extract `prox` (`prox.exe` on Windows), place it on your `PATH`, then verify the installation:

```bash
prox version
```

Alternatively, install with Go 1.25 or later:

```bash
go install github.com/dortanes/prox/cmd/prox@latest
prox version
```

To build the current source checkout:

```bash
git clone https://github.com/dortanes/prox.git
cd prox
make build
./prox version
```

A multi-platform container image is also available:

```bash
docker run --rm ghcr.io/dortanes/prox:latest prox version
```

## Quick Start

Start a small local backend. Python is used only for this example:

```bash
mkdir -p prox-demo
printf 'prox works\n' > prox-demo/index.html
python3 -m http.server 3000 --bind 127.0.0.1 --directory prox-demo
```

Create `config.json5` in another terminal:

```json5
{
  services: {
    web: {
      listen: ":8080",
      routes: [
        {
          match: { path: "/*" },
          action: "backend",
        },
      ],
    },
  },
  actions: {
    backend: {
      type: "proxy",
      upstream: "127.0.0.1:3000",
    },
  },
}
```

This configuration listens on port 8080 and proxies all requests to the local backend.

## Validate

Validate configuration before starting the server. This is recommended as a CI/CD step.

```bash
prox validate -config config.json5
# ✅ configuration is valid: config.json5 (1 file(s))
```

## Start the Server

```bash
prox serve -config config.json5
```

Send a request through prox from a third terminal:

```bash
curl http://127.0.0.1:8080
# prox works
```

To enable debug-level logging:

```bash
prox serve -config config.json5 -log-level debug
```

## Hot Reload

Configuration changes are detected automatically while the server is running. Editing `config.json5` triggers an atomic reload — in-flight connections complete with the previous configuration, new connections use the updated one.

Manual reload via signal:

```bash
kill -HUP $(pgrep prox)
```

Invalid configurations are rejected gracefully. The server continues running with the last valid configuration.

## Directory Mode

Configuration can be split across multiple files in a directory. Each `.json5` file defines a separate service, with the filename (without extension) used as the service name.

```bash
mkdir config
# Create config/web.json5, config/api.json5, etc.
prox serve -config ./config/
```

See [Configuration](configuration/index.md) for the full configuration reference.

## CLI Reference

```
prox <command> [flags]

Commands:
  serve      Start the proxy server
  build      Compile plugin sources defined in config
  validate   Validate configuration (CI/CD)
  version    Print version
  help       Show help

Flags:
  -config string      Config file or directory (default "config.json5")
  -log-level string   debug, info, warn, error (default "info")
  -watch              Auto-reload on file change (default true)
```
