# Maze Royale

Go services (gateway, matchmaking, simulation, maze, stats) plus a dependency-free
browser client in `web/`. Dependencies are vendored; the Makefile exports
`GOFLAGS=-mod=vendor` — pass `-mod=vendor` to direct `go` commands.

## Commands

- `make run` — start all five services (gateway serves http://localhost:8090)
- `make test` — `go test ./...` for all packages
- `make vet` — `go vet ./...`
- `make build` — compile binaries into ./bin
- `make fmt` — gofmt cmd/ and internal/
- `make demo-up` — start/reuse the stack behind an allowlist proxy and print a
  temporary public demo URL (`make demo-status`, `make demo-down`)
- `node --test web/powerups.test.mjs` — canvas/HUD tests for the web client using
  Node's built-in runner and a fake DOM/canvas (not a real browser). `web/*.js`
  has no package.json, so tests load modules via `data:` URL imports.

## Ports

Defaults are 8090-8094 (gateway, matchmaking, simulation, maze, stats) on
purpose: the sibling `webapp` and `disney-streaming-demo` Docker stacks occupy
8080-8082, so `make run` works alongside them. The defaults live in
`internal/protocol/services.go`.

Each service takes `-port`. Peer URLs are also settable via `MATCHMAKING_URL`,
`SIMULATION_URL`, `STATS_URL`, `WEB_DIR` (gateway), `MM_GATEWAY_URL`,
`MM_SIMULATION_URL` (matchmaking) and `SIM_MAZE_URL`, `SIM_GATEWAY_URL`,
`SIM_STATS_URL` (simulation).

## Temporary public demo URL (Cloudflare Quick Tunnel)

For demos only. Requires `cloudflared` on `PATH` (`brew install cloudflared`);
no Cloudflare account or token is needed, the URL is random, and a new one is
issued on every restart.

```
make demo-up      # start/reuse everything, print the public URL
make demo-status  # component PIDs, saved URL, local + public health
make demo-down    # stop only what the script started
```

`scripts/demo-tunnel.sh url` prints the current URL. `demo-up` reuses an
already-running gateway when `/healthz` answers, so it will not start duplicate
services. Ports are overridable via `GATEWAY_PORT`, `MATCHMAKING_PORT`,
`SIMULATION_PORT`, `MAZE_PORT`, `STATS_PORT` and `PROXY_PORT` (defaults 8090-8094,
matching the app defaults, and 18090); PID files and logs live in `.demo/` (`DEMO_DIR`).

Do **not** tunnel the gateway directly: it serves `POST /internal/push`
unauthenticated and `websocket.AcceptOptions` sets `InsecureSkipVerify: true`
(origin checks disabled) because it assumes localhost-only traffic. Exposing it
would let anyone inject simulation frames. `cmd/publicproxy` is the allowlist
reverse proxy the tunnel points at instead: it binds `127.0.0.1`, permits only
`GET`/`HEAD` (`GET` only for `/ws`) and only the paths `/`, `/index.html`,
`/style.css`, `/lobby.js`, `/net.js`, `/game.js`, `/render.js`, `/ws`,
`/leaderboard`, `/healthz`. Its `-origin` flag pins the required websocket
`Origin` header. `demo-up` verifies that `POST /internal/push` returns 404
through the tunnel and fails loudly otherwise.

Everything is started with `nohup` — processes started inside an agent/CI shell
are killed when that session ends (this shows up as Cloudflare "Error 1033").
Sleep also drops the tunnel, so the script holds the machine awake with
`caffeinate -dimsu -t $CAFFEINATE_SECONDS` (default 4h) on macOS.
