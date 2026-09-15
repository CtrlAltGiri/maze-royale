# Architecture

Five Go processes on localhost, each with its own port, speaking JSON over HTTP
to each other, plus a static canvas client. One command starts everything:
`make run` (`cmd/devrun`).

```
                     browser tabs (web/, one <canvas> each)
                              |  websocket, JSON text frames
                              v
                    +---------------------+
                    |  gateway  :8080     |  static files, /ws, fanout
                    +----+-----------+----+
       lobby intents     |           |  input intents          ^
                         v           v                         |  POST /internal/push
            +----------------+   +------------------+          |  (room_state, event,
            | matchmaking    |-->| simulation :8082 |----------+   match_start, snapshot,
            |   :8081        |   | 20 Hz tick loop  |              match_end)
            +----------------+   +---+----------+---+
                                     |          |
                     POST /generate  v          v  POST /results
                          +-------------+   +------------+
                          | maze :8083  |   | stats :8084|-- ./data/stats.json
                          +-------------+   +------------+
```

Shared packages: `internal/protocol` (frozen wire contract), `internal/gridmath`
(cell math and the reference movement function), `internal/httpx` (JSON client
and server helpers with `X-Request-Id` propagation).

## The request path of a single keypress

1. The player presses `→`. `web/game.js` maps it to `dir: "right"` and
   `web/net.js` sends `{"t":"input","seq":42,"dir":"right"}` over the
   websocket. The client does not move its own runner.
2. `internal/gateway` decodes the frame, looks up the connection's room and the
   room's `match_id`, mints an `X-Request-Id`, and posts
   `SimInputRequest {player_id, seq, dir}` to
   `simulation:8082 POST /matches/{match_id}/input`. It returns immediately.
3. `internal/simulation` stores the intent as the runner's desired direction
   (latest `seq` wins). Nothing moves yet.
4. On the next tick (every 50 ms) the match loop calls
   `gridmath.Grid.Step(pos, dir, 4.0, 0.05)` for every live runner, moves
   catchers through their `Mover`, checks every catcher against every live
   runner with `gridmath.Caught`, marks eliminations, and evaluates the win
   condition and round timer.
5. The loop encodes one `snapshot` frame (plus any `event{kind:caught}` or
   `match_end` frames) and posts a `GatewayPush {match_id, room_code, frames}`
   to `gateway:8080 POST /internal/push`.
6. The gateway writes every frame to every websocket connection in that room,
   players and spectators alike.
7. Each tab's `web/game.js` buffers the snapshot and `web/render.js`
   interpolates from the previous snapshot toward it, so all four tabs show
   the runner in the same place within one tick.

## Lobby path

`create_room` / `join_room` / `set_ready` / `start_room` go gateway ->
matchmaking. Every mutation returns the new `RoomState` plus the recipient
list; the gateway fans it out. When start criteria are met, matchmaking runs
the 5 s countdown (pushing `event{kind:countdown}` through the gateway), then
calls `simulation POST /matches`, which asks `maze POST /generate` for a
layout and spawn points and returns one `match_start` per player. Matchmaking
pushes those to the gateway with `player_ids` set so each tab receives its own
`you` block.

## Reconnect

A tab that reloads mid match sends `hello` and `join_room` again. The gateway
sees the room is in a match, fetches `simulation GET /matches/{id}` and sends a
`match_start` with `you.role = spectator` followed by the latest snapshot. The
tick loop is never touched.

## Hooks for later

- `simulation.Mover` interface: drop in a learned catcher.
- `stats` is a read model over an append-only results file; swap the file for
  a database behind the same HTTP routes.
- `protocol.Version` gates the contract.
