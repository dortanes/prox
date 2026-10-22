# Load Balancing

Routes can distribute requests across multiple upstream targets using a `balancer`. The balancer selects a target per request (L7) or per connection (L4), and the selected address is available as `{target}` in the action's `upstream` field.

```json5
{
  match: { domain: "*.**", path: "/ws" },
  balancer: {
    type: "roundrobin",
    targets: ["10.0.1.1:3505", "10.0.1.2:3505", "10.0.1.3:3505"],
  },
  action: {
    type: "proxy",
    upstream: "{target}",
  },
}
```

## Configuration

| Field      | Type     | Required | Description                                                      |
| ---------- | -------- | -------- | ---------------------------------------------------------------- |
| `type`     | string   | ✓        | Balancing strategy: `"roundrobin"`, `"random"`, or `"leastconn"` |
| `targets`  | string[] | ✓        | List of upstream addresses                                       |
| `fallback` | bool     |          | Pick a random target from all groups when the key has no match   |
| `eject`    | duration |          | How long a target that refused a connection is skipped. Default `"10s"`; `"0s"` disables [ejection](#failed-targets) |

## Strategies

| Type         | Description                                                           |
| ------------ | --------------------------------------------------------------------- |
| `roundrobin` | Distributes requests evenly in order across targets. Lock-free, O(1). |
| `random`     | Selects a target at random per request.                               |
| `leastconn`  | Routes to the target with the fewest active connections.              |

## The `{target}` Template

The `{target}` placeholder in `upstream` is replaced with the address selected by the balancer. It can be used standalone or embedded in a URL:

```json5
// Bare target — becomes http://10.0.1.1:3505
upstream: "{target}"

// Embedded — becomes http://10.0.1.1:3505/api/v1
upstream: "http://{target}/api/v1"
```

**Constraints:**

- Balancers are only supported with `proxy` and `pass` action types
- The action's `upstream` **must** contain `{target}` when a balancer is used
- Targets must be non-empty and unique within a balancer (unless [plugins](../plugins/index.md) populate them dynamically)

## L4 Pass-through

Balancers also work with `pass` routes for L4 TCP load balancing. Each new connection receives a target from the balancer:

```json5
{
  match: { domain: "*.cdn.example.com" },
  balancer: {
    type: "roundrobin",
    targets: ["10.0.0.5:443", "10.0.0.6:443"],
  },
  action: {
    type: "pass",
    upstream: "{target}",
  },
}
```

## WebSocket Pinning

WebSocket connections through a balanced route are handled transparently — the balancer selects the target before the upgrade handshake, and the entire WebSocket session remains pinned to that target.

## Connection Tracking (`leastconn`)

The `leastconn` strategy tracks active connections per target. A connection is counted from the moment the balancer selects it until the request completes (HTTP response sent, WebSocket closed, or TCP relay finished). This applies to both L7 proxy routes and L4 pass routes.

```json5
// Route to the target with the fewest active connections.
{
  match: { domain: "*.**", path: "/ws" },
  balancer: {
    type: "leastconn",
    targets: ["10.0.1.1:3505", "10.0.1.2:3505", "10.0.1.3:3505"],
  },
  action: {
    type: "proxy",
    upstream: "{target}",
  },
}
```

## Failed Targets

When prox cannot connect to the selected target (connection refused, dial timeout, no route to host), nothing of the request has reached the upstream yet. prox then:

1. **Ejects the target.** The balancer skips it for the `eject` cool-down, then offers it again. prox logs one warning when a target is ejected and one info line when it is readmitted.
2. **Retries the request** on another target of the same group (the same key for [grouped targets](#fallback)) that this request has not tried yet. Once every target of the group has failed, the request fails as it would without retries.

```json5
{
  match: { domain: "*.**" },
  balancer: {
    type: "leastconn",
    targets: ["10.0.1.1:3505", "10.0.1.2:3505"],
    eject: "30s",   // skip a refusing target for 30 seconds
  },
  action: {
    type: "proxy",
    upstream: "http://{target}",
  },
}
```

Retries apply to every request a `proxy` action sends to a balanced `{target}`: plain HTTP and HTTP/2, WebSocket and other upgrades, `stream` tunnels, CONNECT, and forward-proxy requests. A request is never retried once prox has started sending it, so an upstream that accepts the connection and then fails is handled as before. A target pinned by a plugin with [`sdk.WithTarget`](../plugins/sdk.md#per-request-target-pinning) is ejected but not replaced. L4 `pass` routes do not retry.

When every target of a group is ejected, the group counts as empty: `fallback: true` picks a target from another group, and the action's [`fallback`](actions.md) handles the request when no target is left. Pushing a new target list clears the ejection of targets that are no longer in it; config reloads start with no target ejected.

## Fallback

When using [plugins](../plugins/index.md) with grouped targets (`SetGroupedTargets` / `SetActionGroupedTargets`), the balancer normally returns an empty result if the requested key has no matching group. The key comes from the domain wildcard capture, unless the plugin's `on_request` hook overrides it with [`sdk.WithGroup`](../plugins/sdk.md#per-request-group-selection), or bypasses the choice altogether with [`sdk.WithTarget`](../plugins/sdk.md#per-request-target-pinning). With `fallback: true`, the balancer instead picks a **random target from all groups**.

This is useful for service discovery scenarios where a request may arrive for an unknown key — instead of failing, it gets routed to any available server.

```json5
{
  match: { domain: "*.**" },
  plugins: ["./plugins/discovery"],
  balancer: {
    type: "leastconn",
    fallback: true,
  },
  action: {
    type: "proxy",
    upstream: "{target}",
  },
}
```

Fallback activates when:

- The key has **no matching group** in the grouped target map
- The key's group exists but has an **empty target list**
- Every target of the key's group is [ejected](#failed-targets)

Fallback does **not** activate when:

- No groups are configured at all (delegates to the inner flat balancer)
- The key is empty (delegates to the inner flat balancer)

## Dynamic Targets

Balancer targets can be populated dynamically by [plugins](../plugins/index.md). Set `targets` to an empty array and attach a plugin that pushes targets at runtime:

```json5
{
  match: { domain: "*.**", path: "/ws" },
  plugins: ["./plugins/resolver"],
  balancer: {
    type: "leastconn",
    targets: [],   // plugin will populate
  },
  action: {
    type: "proxy",
    upstream: "{target}",
  },
}
```

See the [Plugins](../plugins/index.md) section for the target push API.

