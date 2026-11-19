# Deployment

Production deployment patterns for prox, including containerized operation, configuration reloading, and L4/L7 mixed-mode dispatching.

## Docker

Run prox with Docker Compose:

```yaml
# docker-compose.yml
services:
  prox:
    image: ghcr.io/dortanes/prox:1.0.0
    ports:
      - "443:443"
      - "8080:8080"
    volumes:
      - ./config/:/etc/prox/config/
      - ./certs/:/etc/prox/certs/
    command: ["prox", "serve", "-config", "/etc/prox/config/"]
```

Standalone container:

```bash
docker run -v ./config.json5:/etc/prox/config.json5 -p 8080:8080 ghcr.io/dortanes/prox:1.0.0
```

## Hot Reload

Configuration changes are detected automatically via file watcher. Manual reload is also supported via `SIGHUP`. Both L4 and L7 routes are swapped atomically — in-flight connections complete with the previous configuration, new connections use the updated one. Invalid configurations are rejected and the error is logged; the last valid configuration remains active. A reload also re-reads TLS certificates and starts or stops plugins. Settings tied to a running listener need a restart: they are logged as warnings and listed by [`POST /api/reload`](admin-api.md#post-apireload).

All loaded files are watched. Editing a nested service fragment or route include triggers a full reload.

```
prox serve -config config.json5            # watcher enabled by default
prox serve -config config.json5 -watch=false
```

## Restart Without Dropping Traffic

Send `SIGUSR2` to replace the process, for example after installing a new binary. The new process starts from the same command line on the same listening sockets, and the old one finishes its connections for up to 15 seconds and exits. If the new process fails to start, the old one keeps serving. This also applies settings that a reload cannot.

The new process gets a new PID and is a child of the old one, so a supervisor must not treat the old PID's exit as a failure. Inside a container the old process is PID 1 and its exit stops the container; use a rolling replacement there. `SIGUSR2` is not available on Windows.

## L4 Dispatching

When a TLS service contains any `pass` routes, prox activates an L4 dispatcher on that listener. Adding the first `pass` route or removing the last one takes effect on reload. The dispatcher intercepts raw TCP connections **before** TLS termination:

```
Client → :443 TCP
  → Peek SNI from TLS ClientHello (5s timeout)
  → Walk routes in config order
    → First match is "pass" → raw TCP relay to upstream
    → First match is "drop" → close connection (when dispatcher is active)
    → First match is L7     → TLS termination → HTTP routing
    → No match              → TLS termination → HTTP 404, or a failed handshake with strict_sni and no certificate
```

!!! note
    Without `pass` routes, a `drop` route with a domain and no path or method condition fails the TLS handshake for that name, and other `drop` routes close the connection at L7. When the dispatcher is active, `drop` routes with domain patterns close connections before the TLS handshake.

**Route order matters.** The dispatcher evaluates all routes — not just `pass` routes — in configuration order. The first domain match wins.

```json5
{
  services: {
    gateway: {
      listen: ":443",
      tls: true,
      tls_cert: "/etc/prox/certs/",
      routes: [
        // L4: *.cdn.example.com → raw TCP relay (no TLS termination)
        {
          match: { domain: "*.cdn.example.com" },
          action: { type: "pass", upstream: "10.0.0.5:3504" },
        },

        // L7: everything else on *.example.com → TLS termination + HTTP
        {
          match: { domain: "*.example.com" },
          action: "web_app",
        },
      ],
    },
  },
}
```

A single listener can mix L4 (TCP pass-through) and L7 (HTTP) routes. Hot reload updates both L4 and L7 routes atomically.
