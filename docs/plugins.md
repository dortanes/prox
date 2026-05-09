# Plugins

Plugins are external executables that extend prox at runtime. A plugin can dynamically manage balancer targets based on external data sources — service discovery, DNS, network mesh, etc.

## Overview

- Plugins communicate with prox over **stdin/stdout** using line-delimited JSON
- Each plugin runs as a **child process**, automatically restarted on crash
- Plugins operate on the **control plane only** — they never handle traffic
- Target updates are applied **atomically** — zero lock contention on the data plane

## Configuration

Add a `plugins` array to any route that has a `balancer`:

```json5
{
  match: { domain: "*.**", path: "/ws" },
  plugins: ["./plugins/resolver.go"],
  balancer: {
    type: "leastconn",
    targets: [],   // empty — plugin will populate
  },
  action: {
    type: "proxy",
    upstream: "{target}",
  },
}
```

### Rules

- Plugin paths are resolved relative to the config file's directory
- Absolute paths are used as-is
- **`.go` source files are compiled automatically** — no manual build step needed
- Pre-compiled binaries are used as-is (must be executable)
- A `balancer` is required on routes with plugins (plugins need a target pool to manage)
- `targets: []` is valid when plugins are present — the plugin populates the list
- Multiple plugins can be attached to a single route

### Auto-compilation

Prox can compile plugin sources automatically — no manual build step needed.

**Single file** — path ends in `.go`:

```
plugins: ["./plugins/resolver.go"]  →  go build -o ./plugins/resolver
```

**Directory** — path points to a Go package directory:

```
plugins: ["./plugins/resolver/"]  →  go build -o ./plugins/resolver
```

Compiled binaries are placed next to the source. Rebuilds are **skipped** if the binary is newer than the source (mtime check). On config reload, modified sources are recompiled automatically.

#### Third-party packages

Plugins can import any third-party package. The build runs from the plugin's directory, so `go.mod` is resolved naturally by the Go toolchain:

- **Plugin inside a host module** — imports resolve from the host's `go.mod`
- **Plugin directory with its own `go.mod`** — fully standalone, independent dependencies

## Protocol

Communication uses **line-delimited JSON** over stdin/stdout. Each message is a single JSON object terminated by `\n`.

### Prox → Plugin

#### `configure`

Sent once after the plugin starts, and again on config reload. Tells the plugin which route it's managing.

```json
{
  "method": "configure",
  "params": {
    "route_id": "gateway:0",
    "match": {
      "domain": "*.**",
      "path": "/ws"
    }
  }
}
```

- `route_id` — stable identifier in the format `service:routeIndex`
- `match` — the route's match criteria (domain pattern, path pattern)

### Plugin → Prox

#### `set_targets`

Push new targets to the route's balancer. Sent whenever the target pool changes.

**Flat mode** — all requests share the same pool:

```json
{
  "method": "set_targets",
  "params": {
    "route_id": "gateway:0",
    "targets": ["10.0.1.1:3505", "10.0.1.2:3505"]
  }
}
```

**Grouped mode** — targets are keyed by the domain wildcard capture. The balancer uses the first `*` from the domain pattern as the lookup key:

```json
{
  "method": "set_targets",
  "params": {
    "route_id": "gateway:0",
    "groups": {
      "de": ["de-node1.internal:8080", "de-node2.internal:8080"],
      "fi": ["fi-node1.internal:8080"],
      "us": ["us-node1.internal:8080", "us-node2.internal:8080"]
    }
  }
}
```

With domain pattern `*.**`, a request to `de.example.com` captures `de` → the balancer picks from the `"de"` group only.

- `targets` — flat replacement list (not a diff). Previous targets are discarded.
- `groups` — keyed replacement map. Each key gets its own sub-balancer with the route's strategy.
- Empty `targets` or empty group (`[]`) is accepted — requests will get 502 until new targets arrive.
- Use `targets` OR `groups`, not both in the same message.

## Lifecycle

```
1. prox starts → spawns plugin process
2. prox sends "configure" for each bound route
3. plugin pushes "set_targets" whenever data changes
4. on config reload → prox sends new "configure"
5. on prox shutdown → stdin is closed → plugin should exit
```

### Crash Recovery

If a plugin process exits unexpectedly:

1. Targets **freeze** at the last known state
2. Prox restarts the plugin with **exponential backoff** (1s → 2s → 4s → ... → 30s max)
3. After restart, prox re-sends `configure` for all bound routes
4. The backoff resets after a successful `set_targets` push

### Stderr

Plugin stderr is forwarded to prox's logger at `debug` level. Use it for diagnostics.

## Writing a Plugin

A plugin is any executable that reads JSON from stdin and writes JSON to stdout. Here's a minimal example in Go:

```go
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "os"
    "time"
)

type Message struct {
    Method string          `json:"method"`
    Params json.RawMessage `json:"params"`
}

type ConfigureParams struct {
    RouteID string `json:"route_id"`
    Match   struct {
        Domain string `json:"domain"`
        Path   string `json:"path"`
    } `json:"match"`
}

type SetTargets struct {
    Method string `json:"method"`
    Params struct {
        RouteID string   `json:"route_id"`
        Targets []string `json:"targets"`
    } `json:"params"`
}

func main() {
    scanner := bufio.NewScanner(os.Stdin)

    for scanner.Scan() {
        var msg Message
        if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
            continue
        }

        switch msg.Method {
        case "configure":
            var params ConfigureParams
            json.Unmarshal(msg.Params, &params)

            // Respond with OK
            fmt.Println(`{"result":"ok"}`)

            // Start pushing targets
            go func() {
                for {
                    targets := discoverTargets(params.Match.Domain)
                    push := SetTargets{}
                    push.Method = "set_targets"
                    push.Params.RouteID = params.RouteID
                    push.Params.Targets = targets
                    data, _ := json.Marshal(push)
                    fmt.Println(string(data))
                    time.Sleep(10 * time.Second)
                }
            }()
        }
    }
}

func discoverTargets(domainPattern string) []string {
    // Your discovery logic here
    return []string{"10.0.1.1:3505", "10.0.1.2:3505"}
}
```

### Shell Script Example

Plugins can be written in any language. Here's a minimal bash plugin:

```bash
#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r '.method')
    if [ "$method" = "configure" ]; then
        route_id=$(echo "$line" | jq -r '.params.route_id')
        echo '{"result":"ok"}'
        # Push initial targets
        echo "{\"method\":\"set_targets\",\"params\":{\"route_id\":\"$route_id\",\"targets\":[\"10.0.1.1:3505\"]}}"
    fi
done
```

## Performance Impact

| Operation | Impact |
|---|---|
| Request routing (hot path) | **None** — plugins don't participate |
| `balancer.Next()` | Unchanged — atomic operations only |
| Target swap (`set_targets`) | O(1) atomic pointer store |
| Plugin communication | Off hot path, async, buffered |
| Plugin crash | Targets freeze, auto-restart with backoff |
