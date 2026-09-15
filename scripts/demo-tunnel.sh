#!/usr/bin/env bash
# demo-tunnel.sh — one command to get a temporary public demo URL.
#
# Starts (or reuses) the five local services, an allowlist reverse proxy
# (cmd/publicproxy) and a Cloudflare Quick Tunnel pointed at the proxy. The
# gateway is never tunneled directly: it serves POST /internal/push
# unauthenticated and disables websocket origin checks.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

DEMO_DIR="${DEMO_DIR:-.demo}"
GATEWAY_PORT="${GATEWAY_PORT:-8090}"
MATCHMAKING_PORT="${MATCHMAKING_PORT:-8091}"
SIMULATION_PORT="${SIMULATION_PORT:-8092}"
MAZE_PORT="${MAZE_PORT:-8093}"
STATS_PORT="${STATS_PORT:-8094}"
PROXY_PORT="${PROXY_PORT:-18090}"
CAFFEINATE_SECONDS="${CAFFEINATE_SECONDS:-14400}"

BIN_DIR="$DEMO_DIR/bin"
URL_FILE="$DEMO_DIR/url.txt"
SERVICES="maze stats simulation matchmaking gateway"

mkdir -p "$DEMO_DIR"

pid_file() { printf '%s/%s.pid\n' "$DEMO_DIR" "$1"; }
log_file() { printf '%s/%s.log\n' "$DEMO_DIR" "$1"; }

# alive <name> — true when the recorded pid is still running.
alive() {
  local f pid
  f="$(pid_file "$1")"
  [ -f "$f" ] || return 1
  pid="$(cat "$f" 2>/dev/null || true)"
  [ -n "$pid" ] || return 1
  kill -0 "$pid" 2>/dev/null
}

pid_of() {
  local f
  f="$(pid_file "$1")"
  [ -f "$f" ] && cat "$f" 2>/dev/null || true
}

http_code() { curl -s -o /dev/null -w '%{http_code}' -m 15 "$@" || true; }

build_binaries() {
  echo "==> building binaries into $BIN_DIR"
  mkdir -p "$BIN_DIR"
  GOFLAGS=-mod=vendor go build -o "$BIN_DIR" ./cmd/...
}

start_service() {
  local name="$1"
  shift
  echo "==> starting $name"
  nohup "$@" > "$(log_file "$name")" 2>&1 &
  echo $! > "$(pid_file "$name")"
}

start_stack() {
  if curl -fsS -m 5 "http://127.0.0.1:$GATEWAY_PORT/healthz" >/dev/null 2>&1; then
    echo "==> reusing already-running gateway on 127.0.0.1:$GATEWAY_PORT (no services started)"
    return 0
  fi

  build_binaries

  local gw="http://127.0.0.1:$GATEWAY_PORT"
  local mm="http://127.0.0.1:$MATCHMAKING_PORT"
  local sim="http://127.0.0.1:$SIMULATION_PORT"
  local maze="http://127.0.0.1:$MAZE_PORT"
  local stats="http://127.0.0.1:$STATS_PORT"

  start_service maze "$BIN_DIR/maze" -port "$MAZE_PORT"
  start_service stats "$BIN_DIR/stats" -port "$STATS_PORT" -data data
  start_service simulation "$BIN_DIR/simulation" -port "$SIMULATION_PORT" \
    -maze-url "$maze" -gateway-url "$gw" -stats-url "$stats"
  start_service matchmaking "$BIN_DIR/matchmaking" -port "$MATCHMAKING_PORT" \
    -gateway-url "$gw" -simulation-url "$sim"
  start_service gateway "$BIN_DIR/gateway" -port "$GATEWAY_PORT" \
    -matchmaking "$mm" -simulation "$sim" -stats "$stats" -web web

  local i
  for i in $(seq 1 40); do
    if curl -fsS -m 5 "http://127.0.0.1:$GATEWAY_PORT/healthz" >/dev/null 2>&1; then
      echo "==> gateway healthy on 127.0.0.1:$GATEWAY_PORT"
      return 0
    fi
    sleep 0.5
  done
  echo "ERROR: gateway never became healthy on 127.0.0.1:$GATEWAY_PORT" >&2
  tail -n 40 "$(log_file gateway)" >&2 || true
  exit 1
}

start_proxy() {
  if curl -fsS -m 5 "http://127.0.0.1:$PROXY_PORT/healthz" >/dev/null 2>&1; then
    echo "==> reusing allowlist proxy on 127.0.0.1:$PROXY_PORT"
    return 0
  fi
  [ -x "$BIN_DIR/publicproxy" ] || build_binaries
  start_service proxy "$BIN_DIR/publicproxy" -port "$PROXY_PORT" \
    -upstream "http://127.0.0.1:$GATEWAY_PORT"

  local i
  for i in $(seq 1 20); do
    if curl -fsS -m 5 "http://127.0.0.1:$PROXY_PORT/healthz" >/dev/null 2>&1; then
      echo "==> proxy healthy on 127.0.0.1:$PROXY_PORT"
      return 0
    fi
    sleep 0.5
  done
  echo "ERROR: proxy never became healthy on 127.0.0.1:$PROXY_PORT" >&2
  tail -n 40 "$(log_file proxy)" >&2 || true
  exit 1
}

start_tunnel() {
  if ! command -v cloudflared >/dev/null 2>&1; then
    echo "ERROR: cloudflared not found on PATH. Install it with:" >&2
    echo "  brew install cloudflared" >&2
    exit 1
  fi
  if alive tunnel && [ -s "$URL_FILE" ]; then
    echo "==> reusing cloudflared tunnel (pid $(pid_of tunnel)): $(cat "$URL_FILE")"
    return 0
  fi

  : > "$(log_file tunnel)"
  start_service tunnel cloudflared tunnel --url "http://127.0.0.1:$PROXY_PORT" --no-autoupdate

  local i url
  for i in $(seq 1 60); do
    url="$(grep -Eo 'https://[a-z0-9-]*\.trycloudflare\.com' "$(log_file tunnel)" | head -n 1 || true)"
    if [ -n "$url" ]; then
      printf '%s\n' "$url" > "$URL_FILE"
      echo "==> tunnel URL: $url"
      return 0
    fi
    sleep 0.5
  done
  echo "ERROR: cloudflared did not report a trycloudflare.com URL" >&2
  tail -n 40 "$(log_file tunnel)" >&2 || true
  exit 1
}

start_caffeinate() {
  command -v caffeinate >/dev/null 2>&1 || return 0
  if alive caffeinate; then
    echo "==> caffeinate already running (pid $(pid_of caffeinate))"
    return 0
  fi
  echo "==> starting caffeinate for ${CAFFEINATE_SECONDS}s (sleep drops the tunnel)"
  nohup caffeinate -dimsu -t "$CAFFEINATE_SECONDS" >/dev/null 2>&1 &
  echo $! > "$(pid_file caffeinate)"
}

verify() {
  local url root health push i
  url="$(cat "$URL_FILE")"
  echo
  echo "==> verifying $url"

  # The hostname is logged before Cloudflare's DNS has propagated, so wait for
  # the edge to answer at all (000 = curl could not connect/resolve) before
  # judging the responses.
  for i in $(seq 1 30); do
    [ "$(http_code "$url/healthz")" != "000" ] && break
    sleep 2
  done

  root="$(http_code "$url/")"
  health="$(http_code "$url/healthz")"
  push="$(http_code -X POST "$url/internal/push")"

  echo "    GET  /              -> $root"
  echo "    GET  /healthz       -> $health"
  echo "    POST /internal/push -> $push (expected 404)"

  echo
  echo "Public demo URL: $url"
  echo "NOTE: this is a random temporary Cloudflare Quick Tunnel URL. It requires"
  echo "      no Cloudflare account and changes on every restart."
  echo "State and logs: $DEMO_DIR/ (stop with: bash scripts/demo-tunnel.sh down)"

  if [ "$push" = "000" ]; then
    echo
    echo "WARNING: the tunnel hostname is not reachable from here yet (DNS for a" >&2
    echo "         fresh Quick Tunnel can take a minute, and a local resolver may" >&2
    echo "         have cached the miss). The URL is usually fine in a browser;" >&2
    echo "         re-check with: bash scripts/demo-tunnel.sh status" >&2
    exit 1
  fi
  if [ "$push" != "404" ]; then
    echo
    echo "!!! SECURITY WARNING: POST /internal/push returned $push, not 404." >&2
    echo "!!! The gateway's unauthenticated internal API may be publicly reachable." >&2
    echo "!!! Shut the tunnel down now: bash scripts/demo-tunnel.sh down" >&2
    exit 1
  fi
  if [ "$root" != "200" ]; then
    echo "WARNING: GET / returned $root, expected 200" >&2
  fi
}

cmd_up() {
  start_stack
  start_proxy
  start_tunnel
  start_caffeinate
  verify
}

cmd_down() {
  local name pid stopped=0
  for name in tunnel proxy caffeinate $SERVICES; do
    pid="$(pid_of "$name")"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      echo "stopped $name (pid $pid)"
      stopped=1
    elif [ -n "$pid" ]; then
      echo "$name (pid $pid) was not running"
    fi
    rm -f "$(pid_file "$name")"
  done
  rm -f "$URL_FILE"
  [ "$stopped" = 1 ] || echo "nothing was running"
}

cmd_status() {
  local name pid
  for name in tunnel proxy caffeinate $SERVICES; do
    pid="$(pid_of "$name")"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      echo "$name: running (pid $pid)"
    else
      echo "$name: stopped"
    fi
  done
  echo
  if [ -s "$URL_FILE" ]; then
    echo "url: $(cat "$URL_FILE")"
  else
    echo "url: (none)"
  fi
  echo "local gateway  http://127.0.0.1:$GATEWAY_PORT/healthz -> $(http_code "http://127.0.0.1:$GATEWAY_PORT/healthz")"
  echo "local proxy    http://127.0.0.1:$PROXY_PORT/healthz -> $(http_code "http://127.0.0.1:$PROXY_PORT/healthz")"
  if [ -s "$URL_FILE" ]; then
    echo "public         $(cat "$URL_FILE")/healthz -> $(http_code "$(cat "$URL_FILE")/healthz")"
  fi
}

cmd_url() {
  if [ -s "$URL_FILE" ]; then
    cat "$URL_FILE"
  else
    echo "no tunnel URL recorded in $URL_FILE" >&2
    exit 1
  fi
}

usage() {
  cat >&2 <<'EOF'
usage: scripts/demo-tunnel.sh <up|down|status|url>

  up      start/reuse services, allowlist proxy and Cloudflare Quick Tunnel
  down    stop only the processes this script started
  status  show component state, saved URL and health checks
  url     print the saved public URL

Env: DEMO_DIR GATEWAY_PORT MATCHMAKING_PORT SIMULATION_PORT MAZE_PORT
     STATS_PORT PROXY_PORT CAFFEINATE_SECONDS
EOF
  exit 2
}

case "${1:-}" in
  up) cmd_up ;;
  down) cmd_down ;;
  status) cmd_status ;;
  url) cmd_url ;;
  *) usage ;;
esac
