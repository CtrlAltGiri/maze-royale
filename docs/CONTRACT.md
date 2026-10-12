# Maze Royale wire contract (v2)

Source of truth: `internal/protocol` (`protocol.go` for client frames, `services.go`
for inter-service HTTP). This document mirrors it. **Fan-out agents must not edit
either.** If the contract is wrong, work around it locally and report it in
`contract_issues`.

Every websocket frame is a JSON text message: an object with a `t` field naming
the type. Inter-service calls are JSON over HTTP; non-2xx responses carry
`{code, message}`. Every request carries `X-Request-Id` (see `internal/httpx`),
which services forward to their peers so one keypress is traceable end to end.

## Ports

| Service | Port | Responsibility |
| --- | --- | --- |
| gateway | 8090 | static `web/`, player websockets at `/ws`, fanout, routes intents |
| matchmaking | 8091 | rooms, config, ready state, start criteria, roles/colors |
| simulation | 8092 | authoritative 20 Hz tick loop, collision, elimination, win |
| maze | 8093 | seeded maze generation and validation |
| stats | 8094 | match results and leaderboard |

Every service answers `GET /healthz` -> `{service, version, ok}`.

## Client -> gateway (websocket `/ws`)

| `t` | fields |
| --- | --- |
| `hello` | `name` |
| `create_room` | `config` (RoomConfig) |
| `join_room` | `code` |
| `leave_room` | – |
| `set_ready` | `ready` (bool) |
| `start_room` | – (creator only) |
| `input` | `seq` (int, monotonically increasing per client), `dir` ∈ `up`,`down`,`left`,`right`,`none` |
| `shoot` | – |
| `ping` | `ts` (client millis) |

Clients send intent only. They never assert position. `hello` must precede
every frame except `ping`, which is answered with `pong` at any time.

## Gateway -> client

| `t` | fields |
| --- | --- |
| `welcome` | `player_id` |
| `room_state` | `code`, `config`, `players: [{player_id, name, color, ready, is_creator, is_bot?}]`, `countdown_ms` (0 when idle) |
| `match_start` | `match_id`, `maze: {w, h, walls}`, `you: {player_id, role, color}`, `players: [{player_id, name, role, color, x, y, is_bot?}]`, `catchers: [{id, x, y}]`, `power_ups: [{id, kind, x, y}]`, `round_ms` |
| `snapshot` | `tick`, `ts` (server millis), `players: [{id, x, y, dir, alive, speed_boost_ms?, invisible_ms?, has_gun?}]`, `catchers: [{id, x, y, dir, down_ms?}]`, `power_ups: [{id, kind, x, y}]`, `bullets: [{id, x, y, dir}]` (encoded `[]` when none), `time_left_ms`, `alive_count` |
| `event` | `kind` ∈ `caught` (`player_id`, `catcher_id`, `tick`), `countdown` (`ms`), `bot_joined` (`player_id`, `name`), `power_up_collected` (`player_id`, `tick`, `ms` effect duration, `power_up_id`, `power_up_kind`), `gun_fired` (`player_id`, `tick`), `catcher_down` (`catcher_id`, `tick`, `ms` down duration), `catcher_respawn` (`catcher_id`, `tick`) |
| `match_end` | `match_id`, `winner_ids: []`, `standings: [{player_id, name, survived_ms, place}]` |
| `error` | `code`, `message` |
| `pong` | `ts` (echo of ping) |

`you.role` is `runner` for players, `spectator` for a reconnecting or late client.

### RoomConfig

```json
{
  "maze_size": "small | medium | large",     // 15x15, 21x21, 31x31
  "catchers": positive integer,
  "catcher_speed": 0.6..1.4,
  "round_seconds": 30..300,
  "min_players": 2..8,
  "fill_bots": false,
  "seed": 12345                              // optional; omit for random
}
```

There is no fixed catcher-count cap. The chosen maze must have enough spawn space for the catchers and keep every runner at least 6 cells away; configurations that cannot be placed are rejected when the match starts.

### Maze encoding

`walls` is a **row major JSON array of integers 0..15**, one per cell,
`walls[y*w + x]`. Bit 0 = north wall, bit 1 = east, bit 2 = south, bit 3 = west.
Positions `x`, `y` are floats in cell units; `(0.5, 0.5)` is the center of the
top left cell. Mazes are square and odd sized.

### Movement constants (`internal/protocol`)

- Tick rate 20 Hz (`TickMillis` = 50).
- Runners move 4.0 cells/s; catchers `4.0 * catcher_speed`.
- Catch when distance(catcher, live runner) < 0.45 cells.
- Catchers choose a new direction only at intersections and never reverse
  unless at a dead end.
- Countdown before first tick: 5000 ms.
- Entities travel along cell centers; `gridmath.Grid.Step` is the reference
  movement function and must be used by the simulation.
- Bullets travel 12.0 cells/s (`BulletSpeed`); a bullet within 0.45 cells
  (`BulletRadius`) of a catcher downs it for 3000 ms (`CatcherDownMS`), after
  which it respawns at the maze center.

### Power-ups

The simulation spawns up to four maze pickups (stable ids `power1`..`power4`,
two each of `speed` and `invisibility`) on open cell centers at least 2 cells (euclidean) from every
living runner and catcher; collected pickups respawn after 8000 ms under the
same placement rules. `power_ups` lists only active pickups (encoded `[]` when
none). A runner on the same cell within 0.35 cells collects the pickup:

- `speed`: 1.5× movement for 5000 ms (`speed_boost_ms` remaining on the
  snapshot player; applies from the tick after collection).
- `invisibility`: immune to catching and hidden from catcher brains for
  3000 ms (`invisible_ms` remaining; protects on the collection tick itself).

A fifth pickup (stable id `power5`, kind `gun`) is always active at the maze
center — it bypasses the random placement/clearance rules and never respawns
once collected (one per match). Collecting it sets `has_gun` on the snapshot
player. While holding the gun a runner may send `shoot`; on the next tick a
bullet spawns at the runner's position travelling in the direction the runner
is moving (else last direction moved, else current intent). If none of those
yields a direction the gun is kept. Downed catchers neither move nor catch and
report `down_ms` remaining; they respawn at the maze center when the down
expires.

Re-collecting the same kind refreshes the duration; both kinds can be active
at once. Collection is announced with `event{kind:power_up_collected}`.
Clients only render pickups and effects — there is no pickup input frame.

## Inter-service HTTP

### maze (8093)

`POST /generate` `MazeGenerateRequest {w, h, seed, runners, catchers}` ->
`MazeGenerateResponse {maze, seed, runner_spawns: [{x,y}], catcher_spawns: [{x,y}]}`.
Every cell reachable; each runner spawn ≥ 6 cells (manhattan) from every catcher spawn.

### matchmaking (8091)

Gateway forwards lobby intents; every mutation returns
`MMRoomResponse {room: RoomState, recipients: [player_id]}` and the gateway fans
`room` to `recipients`.

| route | body |
| --- | --- |
| `POST /rooms` | `{player_id, name, config}` |
| `GET /rooms/{code}` | – |
| `POST /rooms/{code}/join` | `{player_id, name}` |
| `POST /rooms/{code}/leave` | `{player_id}` |
| `POST /rooms/{code}/ready` | `{player_id, ready}` |
| `POST /rooms/{code}/start` | `{player_id}` (creator only) |
| `POST /rooms/{code}/end` | `{match_id}` (from the gateway when it relays `match_end`); room returns to the lobby, humans un-readied, and the gateway fans `room` to `recipients` |

Start criteria: `min_players` reached and all ready, or creator calls start.
Matchmaking then runs the 5 s countdown (gateway learns of it through
`room_state.countdown_ms` and `event{kind:countdown}` frames pushed via
`POST /internal/push` on the gateway with `room_code` set) and finally calls
`POST /matches` on simulation, forwarding the resulting per-player
`match_start` frames to the gateway via `/internal/push`.

### simulation (8092)

| route | body / response |
| --- | --- |
| `POST /matches` | `SimCreateMatchRequest {room_code, config, players}` -> `SimCreateMatchResponse {match_id, starts: {player_id: match_start}}` |
| `GET /matches/{id}` | `SimMatchStateResponse {start (you.role = spectator), snapshot, ended}` |
| `POST /matches/{id}/input` | `SimInputRequest {player_id, seq, dir}` |
| `POST /matches/{id}/shoot` | `SimShootRequest {player_id}` |

Simulation calls the maze service for the layout and pushes `snapshot`,
`event` and `match_end` frames (as `GatewayPush` with `match_id` and `room_code` set) to the gateway with `POST /internal/push`
`GatewayPush {match_id, room_code, frames: [encoded frames]}`. At match end it
posts `StatsResultRequest` to stats.

### gateway (8090)

| route | body |
| --- | --- |
| `GET /ws` | websocket upgrade |
| `POST /internal/push` | `GatewayPush {match_id?, room_code, player_ids?, frames: [raw JSON frame, ...]}` from simulation and matchmaking. Empty `player_ids` = every connection in the room; otherwise only those players (per-player `match_start`). The gateway records `room_code -> match_id` from the first push that carries `match_id`, so later `input` frames from that room route to `POST /matches/{match_id}/input`. |
| `GET /leaderboard` | proxies stats `GET /leaderboard` and renders a simple HTML table |
| `/` | static files from `web/` |

### stats (8094)

| route | body |
| --- | --- |
| `POST /results` | `StatsResultRequest {match_id, ended_at, winner_ids, standings}` |
| `GET /leaderboard?limit=&offset=` | `LeaderboardResponse {entries: [{name, wins, rounds, total_survived_ms}], total}` sorted by wins desc, then total survived desc |

Persistence is `./data/stats.json`, written atomically (temp file + rename).
