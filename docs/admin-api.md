# Admin API

prox includes an optional HTTP management API for external integration. When configured, it exposes REST endpoints for health checks, configuration reload, certificate monitoring, and runtime inspection.

!!! note "Zero Overhead"
    The admin API is disabled by default. No goroutines, listeners, or allocations are created unless the `admin` block is present in your configuration.

## Configuration

Add the `admin` block to your root config:

=== "TCP"

    ```json5
    {
      admin: {
        listen: "127.0.0.1:9090",
        token: "your-secret-token",
      },
      // ... services, actions, etc.
    }
    ```

=== "Unix Socket"

    ```json5
    {
      admin: {
        listen: "unix:///var/run/prox.sock",
      },
      // ... services, actions, etc.
    }
    ```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `listen` | string | Yes | TCP address (`"127.0.0.1:9090"`) or Unix socket (`"unix:///var/run/prox.sock"`) |
| `token` | string | No | Bearer token for authentication. If set, all requests must include `Authorization: Bearer <token>` |

!!! warning "Security"
    The admin API has no default listen address; `listen` is required. For production use, bind to localhost or a Unix socket, configure a token, and apply network-level access control before exposing the API.

## Authentication

When `token` is configured, all requests must include the `Authorization` header:

```bash
curl -H "Authorization: Bearer $PROX_TOKEN" http://127.0.0.1:9090/api/health
```

Requests without a valid token receive a `401 Unauthorized` response.

## Endpoints

### `GET /api/health`

Returns server health and status information.

```json
{
  "status": "ok",
  "version": "1.0.0",
  "uptime": "2h15m0s",
  "routes": 12,
  "services": 3,
  "config_valid": true
}
```

### `POST /api/reload`

Triggers a synchronous configuration reload. The response indicates success or failure with details.

```json
// Success
{
  "ok": true,
  "routes": 14,
  "services": 2,
  "applied": ["service \"web\": routes, actions, certificates", "plugins"],
  "restart_required": ["service \"web\": config.max_connections"]
}

// Error
{
  "ok": false,
  "error": "service \"web\" route #3: action \"api\" not found in actions",
  "applied": [],
  "restart_required": []
}
```

`restart_required` lists settings that differ from the ones the process started with and keep their old value until a restart: added or removed services, `listen`, `tls`, `h2`, ACME account settings, `read_timeout`, `write_timeout`, `idle_timeout`, `max_connections`, `admin`, `logging.level`, and `logging.error_log`.

!!! tip
    Unlike SIGHUP or file-watcher reloads, the admin API reload is synchronous — you get the result in the response. A mutex prevents concurrent reloads from any source.

### `GET /api/certs`

Returns the loaded manual certificates and all ACME-managed certificates. `source` is the certificate file or `acme`, and `serial` is hexadecimal.

```json
[
  {
    "domain": "api.example.com",
    "status": "active",
    "expires": "2026-08-29T12:00:00Z",
    "issuer": "Let's Encrypt",
    "names": ["api.example.com"],
    "serial": "4a3f01",
    "source": "acme"
  },
  {
    "domain": "www.example.com",
    "status": "active",
    "expires": "2026-09-01T00:00:00Z",
    "issuer": "Example CA",
    "names": ["www.example.com", "example.com"],
    "serial": "ff",
    "source": "/etc/prox/certs/example.com.crt"
  },
  {
    "domain": "staging.example.com",
    "status": "pending",
    "source": "acme"
  }
]
```

| Status | Meaning |
|--------|---------|
| `active` | Certificate is loaded, or issued and cached |
| `expired` | Manual certificate is past its expiry date |
| `pending` | Certificate is being obtained or not yet available |

### `GET /api/routes`

Returns all configured routes with match patterns, actions, and plugins.

```json
[
  {
    "service": "web",
    "index": 0,
    "match": { "domain": "*.example.com", "path": "/api/*" },
    "action": "proxy_backend",
    "balancer": "roundrobin",
    "plugins": ["auth"]
  },
  {
    "service": "web",
    "index": 1,
    "action": "frontend"
  }
]
```

### `GET /api/services`

Returns metadata about each configured service.

```json
[
  { "name": "web", "listen": ":443", "tls": true, "acme": true, "routes": 5 },
  { "name": "internal", "listen": ":8080", "tls": false, "acme": false, "routes": 3 }
]
```

### `GET /api/plugins`

Returns all configured plugins.

```json
[
  { "name": "auth", "path": "./plugins/auth" },
  { "name": "resolver", "path": "./plugins/resolver" }
]
```

### `GET /api/balancers`

Returns the current state of all route balancers, including their live target pools.

```json
[
  {
    "service": "web",
    "route_index": 0,
    "type": "roundrobin",
    "action": "proxy_backend",
    "targets": ["10.0.0.1:8080", "10.0.0.2:8080", "10.0.0.3:8080"]
  }
]
```

!!! tip "Service Discovery"
    This endpoint is particularly useful for verifying that plugin-managed targets are correctly registered and active.

### `GET /api/metrics`

Returns traffic counters per route and per upstream since the process started. Counting is active only when the `admin` block is configured. `errors` counts `5xx` responses, aborted requests, and failed `pass` connections. The table keeps up to 4096 upstreams.

```json
{
  "routes": [
    { "id": "web:api", "active": 2, "requests": 1200, "errors": 3, "bytes_in": 52000, "bytes_out": 910000 }
  ],
  "upstreams": [
    { "id": "de-1:8080", "active": 1, "requests": 600, "errors": 1, "bytes_in": 26000, "bytes_out": 455000 }
  ]
}
```

### `GET /api/config`

Returns the current configuration with sensitive fields redacted.

```json
{
  "services": { "..." },
  "actions": { "..." },
  "admin": {
    "listen": "127.0.0.1:9090",
    "token": "[REDACTED]"
  }
}
```

Fields named `token` are automatically replaced with `"[REDACTED]"` in the response.

## Integration Examples

### Health Check (monitoring)

```bash
# Simple liveness probe
curl -sf http://127.0.0.1:9090/api/health | jq .status

# Kubernetes-style health check
curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:9090/api/health
```

### CI/CD Reload

```bash
# Deploy new config, then reload
cp new-config.json5 /etc/prox/config.json5
curl -X POST -H "Authorization: Bearer $PROX_TOKEN" \
  http://127.0.0.1:9090/api/reload | jq .
```

### Certificate Monitoring

```bash
# Check for expiring certificates
curl -s -H "Authorization: Bearer $PROX_TOKEN" \
  http://127.0.0.1:9090/api/certs | \
  jq '.[] | select(.status == "active") | {domain, expires}'
```
