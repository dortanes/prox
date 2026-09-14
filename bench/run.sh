#!/usr/bin/env bash
set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$BENCH_DIR")"
PORT=9876
UPSTREAM_PORT=9877
URL="http://127.0.0.1:${PORT}/"

# wrk parameters
DURATION=30
CONNECTIONS=256
THREADS=4
WARMUP=5
RUNS=5

GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${CYAN}[bench]${NC} $*"; }
ok()   { echo -e "${GREEN}[  ok ]${NC} $*"; }

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "Missing required command: $1" >&2
        exit 1
    }
}

kill_port() {
    local pids
    pids=$(lsof -ti:"$1" 2>/dev/null || true)
    [ -z "$pids" ] && return
    echo "$pids" | xargs kill 2>/dev/null || true
    sleep 0.5
    # Force kill any remaining
    pids=$(lsof -ti:"$1" 2>/dev/null || true)
    [ -z "$pids" ] && return
    echo "$pids" | xargs kill -9 2>/dev/null || true
    sleep 0.3
}

cleanup() {
    kill_port $PORT
    kill_port $UPSTREAM_PORT
    rm -f "$BENCH_DIR/prox-bin"
}

wait_ready() {
    local port=$1 tries=0
    while ! curl -sf -o /dev/null "http://127.0.0.1:${port}/" 2>/dev/null; do
        tries=$((tries + 1))
        [ $tries -ge 50 ] && { echo "FAIL: port $port not ready"; return 1; }
        sleep 0.1
    done
}

run_single() {
    wrk -t$THREADS -c$CONNECTIONS -d${DURATION}s --latency "$1" 2>&1
}

extract_rps()   { echo "$1" | grep "Requests/sec:" | awk '{printf "%.0f", $2}'; }
extract_avg()   { echo "$1" | grep "^[[:space:]]*Latency" | head -1 | awk '{print $2}'; }
extract_p99()   { echo "$1" | grep "99%" | awk '{print $2}'; }

bench_one() {
    local name=$1 target_url=${2:-$URL}
    local run_rps=() run_raw=()

    for run in $(seq 1 $RUNS); do
        wrk -t2 -c64 -d${WARMUP}s "$target_url" > /dev/null 2>&1 || true
        sleep 0.5

        local raw
        raw=$(run_single "$target_url")
        local rps
        rps=$(extract_rps "$raw")
        run_rps+=("${rps:-0}")
        run_raw+=("$raw")

        log "  Run $run: ${rps:-0} req/s"
    done

    local median_rps selected_raw=""
    median_rps=$(printf '%s\n' "${run_rps[@]}" | sort -n | awk '{ values[NR] = $1 } END { print values[int((NR + 1) / 2)] }')
    for i in "${!run_rps[@]}"; do
        if [ "${run_rps[$i]}" = "$median_rps" ]; then
            selected_raw=${run_raw[$i]}
            break
        fi
    done

    local avg p99
    avg=$(extract_avg "$selected_raw")
    p99=$(extract_p99 "$selected_raw")
    NAMES+=("$name")
    RPS_LIST+=("$median_rps")
    AVG_LIST+=("${avg:-N/A}")
    P99_LIST+=("${p99:-N/A}")
}

NAMES=()
RPS_LIST=()
AVG_LIST=()
P99_LIST=()

for command_name in go git wrk nginx haproxy caddy traefik curl lsof; do
    require_command "$command_name"
done

trap cleanup EXIT

echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  Reverse Proxy Benchmark${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════════════════${NC}"
echo ""
echo "  Date:         $(date -u +'%Y-%m-%dT%H:%M:%SZ')"
echo "  Revision:     $(git -C "$PROJECT_DIR" describe --always --dirty)"
echo "  OS:           $(sw_vers -productVersion) ($(uname -m))"
echo "  Machine:      $(sysctl -n machdep.cpu.brand_string)"
echo "  Cores:        $(sysctl -n hw.ncpu)"
echo "  RAM:          $(( $(sysctl -n hw.memsize) / 1024 / 1024 / 1024 )) GB"
echo "  Go:           $(go version)"
echo "  wrk:          $(wrk --version 2>&1 | head -1)"
echo "  Nginx:        $(nginx -v 2>&1)"
echo "  HAProxy:      $(haproxy -v 2>&1 | head -1)"
echo "  Caddy:        $(caddy version 2>&1 | head -1)"
echo "  Traefik:      $(traefik version 2>&1 | awk -F': ' '/^Version:/ { print $2; exit }')"
echo "  wrk:          ${THREADS} threads, ${CONNECTIONS} connections, ${DURATION}s"
echo "  Runtime:      3 execution threads per proxy, default garbage collection"
echo "  Runs:         ${RUNS} per target (median used)"
echo ""

# Start upstream
log "Starting upstream on :${UPSTREAM_PORT}..."
kill_port $UPSTREAM_PORT
(cd "$BENCH_DIR" && go run upstream.go &) 2>/dev/null
wait_ready $UPSTREAM_PORT
ok "Upstream ready"
echo ""

log "Benchmarking ${BOLD}upstream baseline${NC}..."
bench_one "upstream" "http://127.0.0.1:${UPSTREAM_PORT}/"
echo ""

# ─── prox ─────────────────────────────────────────────────────────────────
log "Building prox..."
(cd "$PROJECT_DIR" && go build -ldflags="-s -w" -o bench/prox-bin ./cmd/prox) 2>&1
log "Benchmarking ${BOLD}prox${NC}..."
kill_port $PORT
LOG_LEVEL=error GOMAXPROCS=3 "$BENCH_DIR/prox-bin" serve -config "$BENCH_DIR/prox.json5" &>/dev/null &
wait_ready $PORT
bench_one "prox"
kill_port $PORT
sleep 0.5
echo ""

# ─── nginx ────────────────────────────────────────────────────────────────
log "Benchmarking ${BOLD}nginx${NC}..."
kill_port $PORT
nginx -c "$BENCH_DIR/nginx.conf" 2>/dev/null
wait_ready $PORT
bench_one "nginx"
nginx -s quit 2>/dev/null || kill_port $PORT
sleep 0.5
echo ""

# ─── haproxy ──────────────────────────────────────────────────────────────
log "Benchmarking ${BOLD}haproxy${NC}..."
kill_port $PORT
haproxy -f "$BENCH_DIR/haproxy.cfg" -D 2>/dev/null
wait_ready $PORT
bench_one "haproxy"
kill_port $PORT
sleep 0.5
echo ""

# ─── caddy ────────────────────────────────────────────────────────────────
log "Benchmarking ${BOLD}caddy${NC}..."
kill_port $PORT
GOMAXPROCS=3 caddy run --config "$BENCH_DIR/Caddyfile" --adapter caddyfile &>/dev/null &
wait_ready $PORT
bench_one "caddy"
kill_port $PORT
sleep 0.5
echo ""

# ─── traefik ──────────────────────────────────────────────────────────────
log "Benchmarking ${BOLD}traefik${NC}..."
kill_port $PORT
GOMAXPROCS=3 traefik --configfile="$BENCH_DIR/traefik.yaml" &>/dev/null &
wait_ready $PORT
bench_one "traefik"
kill_port $PORT
sleep 0.5
echo ""

# ─── Results ──────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  Results (median of ${RUNS} runs)${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════════════════${NC}"
echo ""
printf "  ${BOLD}%-12s %10s %10s %10s${NC}\n" "Proxy" "Req/s" "Avg Lat" "P99 Lat"
printf "  %-12s %10s %10s %10s\n" "───────────" "─────────" "─────────" "─────────"
for i in "${!NAMES[@]}"; do
    printf "  %-12s %10s %10s %10s\n" "${NAMES[$i]}" "${RPS_LIST[$i]}" "${AVG_LIST[$i]}" "${P99_LIST[$i]}"
done
echo ""
echo "  Machine: $(sysctl -n machdep.cpu.brand_string), $(sysctl -n hw.ncpu) cores"
echo "  Test: wrk -t${THREADS} -c${CONNECTIONS} -d${DURATION}s, proxy → localhost:${UPSTREAM_PORT}"
echo ""
