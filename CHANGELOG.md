# Changelog

## [1.3.0](https://github.com/dortanes/prox/compare/v1.2.1...v1.3.0) (2026-10-31)


### Features

* **acme:** ✨ rediscover DNS zones periodically ([cdc8afe](https://github.com/dortanes/prox/commit/cdc8afe94215cddc4e88df7020f122f8b4f4980f))


### Bug Fixes

* **acme:** 🐛 stop renewing domains no longer managed ([18db266](https://github.com/dortanes/prox/commit/18db266d8ac43321ca8f22beb1281bd26ff468f2))
* **server:** 🐛 retry zone discovery after a failed start ([cf8ee6e](https://github.com/dortanes/prox/commit/cf8ee6e557ab8433facd7183ef3f56492e60503b))

## [1.2.1](https://github.com/dortanes/prox/compare/v1.2.0...v1.2.1) (2026-10-28)


### Bug Fixes

* **plugin:** 🐛 close caller pool without races ([8dfae4c](https://github.com/dortanes/prox/commit/8dfae4c98cf5c446e56299bafe5ab17e7288bc24))
* **router:** 🐛 route HTTP/2 CONNECT by SNI ([a1d416d](https://github.com/dortanes/prox/commit/a1d416d3e6cd451ac586453f8ad85cc55482868a))
* **server:** 🐛 honor h2: false on every worker ([960ab8d](https://github.com/dortanes/prox/commit/960ab8d89a365df3093944c788d53448d6d4c625))

## [1.2.0](https://github.com/dortanes/prox/compare/v1.1.0...v1.2.0) (2026-10-22)


### Features

* **balancer:** ✨ eject and retry failed targets ([a475536](https://github.com/dortanes/prox/commit/a4755361127fff654b02ac2aadc4ad819cfe45bb))

## [1.1.0](https://github.com/dortanes/prox/compare/v1.0.0...v1.1.0) (2026-10-22)


### Features

* **plugin:** ✨ add session usage and drop hooks ([476d416](https://github.com/dortanes/prox/commit/476d416c8babd1266602f235881327b961601291))
* **sdk:** ✨ add session, usage and drop APIs ([1d1069e](https://github.com/dortanes/prox/commit/1d1069ebaa3d6bca1f4db8283449ae857ec22409))


### Bug Fixes

* **proxy:** 🐛 relay upgrades on static routes ([8e859ce](https://github.com/dortanes/prox/commit/8e859ce83452098c6498368168ddde27624f48d5))


### Performance Improvements

* **bench:** ⚡ make results reproducible ([da9fca7](https://github.com/dortanes/prox/commit/da9fca70e240e2c0425504cbf1646aec80f3dbaa))

## 1.0.0 (2026-09-14)

- **HTTP and TCP on one listener.** prox reads the SNI hostname from the TLS ClientHello before termination. Matching connections can be relayed unchanged to raw TCP upstreams, while other domains terminate TLS and continue through HTTP routing.
- **Flexible routing and load balancing.** Match HTTP traffic by domain, path, method, or headers. Balance L4 and L7 connections using round-robin, random, or least-connections strategies, with health checks and dynamic targets.
- **Automatic HTTPS.** Issue and renew certificates through Let's Encrypt, ZeroSSL, or custom ACME CAs using TLS-ALPN-01, HTTP-01, or Cloudflare DNS-01. Includes automatic zone discovery, wildcard certificates, OCSP stapling, CA fallback, and S3-compatible certificate storage.
- **Readable JSON5 configuration.** Split configuration across files, validate it before startup, watch for changes, and apply valid reloads atomically without dropping active connections.
- **External plugins and Go SDK.** Add authorization, header and response changes, connection gates, speed limits, and dynamic upstream discovery without running extension code inside the proxy process.
- **Streaming and persistent connections.** Proxy HTTP/1.1, HTTP/2, h2c, WebSocket, streaming, and raw TCP traffic with per-route bandwidth controls.
- **Operational API.** Inspect health, routes, services, certificates, plugins, and balancers or trigger a configuration reload through the optional authenticated Admin API.
