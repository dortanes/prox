# Benchmark harness

This directory contains a local macOS benchmark for comparing the current prox checkout with Nginx, HAProxy, Caddy, and Traefik. It measures HTTP/1.1 proxying to the same local Go upstream without TLS.

The harness reports the direct upstream baseline before testing the proxies. Each target receives a five-second warmup followed by five 30-second runs with 256 connections and four `wrk` threads. The table uses the run with the median requests per second and keeps that run's average and P99 latency together.

prox, Caddy, and Traefik run with `GOMAXPROCS=3`; Nginx uses three workers and HAProxy uses three threads. All Go programs use the default garbage collector. Proxies run in a fixed order, so the output is useful for local regression checks rather than portable rankings.

## Requirements

- macOS
- Go 1.25 or later
- `wrk`
- Nginx
- HAProxy
- Caddy
- Traefik
- `curl` and `lsof`

The harness uses ports 9876 and 9877 and stops processes already listening on either port. Run it only when those ports are available for the benchmark.

## Run

```bash
bash bench/run.sh
```

The output records the UTC timestamp, git revision and dirty state, macOS version, architecture, processor, memory, workload, and tool versions. Preserve the complete output whenever publishing a result.
