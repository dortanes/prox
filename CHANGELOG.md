# Changelog

## 1.0.0 (2026-09-14)

- **HTTP and TCP on one listener.** prox reads the SNI hostname from the TLS ClientHello before termination. Matching connections can be relayed unchanged to raw TCP upstreams, while other domains terminate TLS and continue through HTTP routing.
- **Flexible routing and load balancing.** Match HTTP traffic by domain, path, method, or headers. Balance L4 and L7 connections using round-robin, random, or least-connections strategies, with health checks and dynamic targets.
- **Automatic HTTPS.** Issue and renew certificates through Let's Encrypt, ZeroSSL, or custom ACME CAs using TLS-ALPN-01, HTTP-01, or Cloudflare DNS-01. Includes automatic zone discovery, wildcard certificates, OCSP stapling, CA fallback, and S3-compatible certificate storage.
- **Readable JSON5 configuration.** Split configuration across files, validate it before startup, watch for changes, and apply valid reloads atomically without dropping active connections.
- **External plugins and Go SDK.** Add authorization, header and response changes, connection gates, speed limits, and dynamic upstream discovery without running extension code inside the proxy process.
- **Streaming and persistent connections.** Proxy HTTP/1.1, HTTP/2, h2c, WebSocket, streaming, and raw TCP traffic with per-route bandwidth controls.
- **Operational API.** Inspect health, routes, services, certificates, plugins, and balancers or trigger a configuration reload through the optional authenticated Admin API.
