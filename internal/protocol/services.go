package protocol

import "encoding/json"

// Service ports and base URLs. Every service is its own process on localhost.
const (
	PortGateway     = 8090
	PortMatchmaking = 8091
	PortSimulation  = 8092
	PortMaze        = 8093
	PortStats       = 8094

	GatewayURL     = "http://localhost:8090"
	MatchmakingURL = "http://localhost:8091"
	SimulationURL  = "http://localhost:8092"
	MazeURL        = "http://localhost:8093"
	StatsURL       = "http://localhost:8094"
)

// ---- maze service (8093) ----

// MazeGenerateRequest: POST /generate
type MazeGenerateRequest struct {
	W    int   `json:"w"`
	H    int   `json:"h"`
	Seed int64 `json:"seed"`
	// Runners is how many runner spawns to return; Catchers how many catcher spawns.
	Runners  int `json:"runners"`
	Catchers int `json:"catchers"`
}

// Point is a cell center in cell units.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// MazeGenerateResponse: runner spawns are at least 6 cells (manhattan) from every catcher spawn.
type MazeGenerateResponse struct {
	Maze          Maze    `json:"maze"`
	Seed          int64   `json:"seed"`
	RunnerSpawns  []Point `json:"runner_spawns"`
	CatcherSpawns []Point `json:"catcher_spawns"`
}

// ---- matchmaking service (8091) ----
// Gateway forwards lobby intents here; matchmaking returns the room state to fan out.

// MMCreateRoomRequest: POST /rooms
type MMCreateRoomRequest struct {
	PlayerID string     `json:"player_id"`
	Name     string     `json:"name"`
	Config   RoomConfig `json:"config"`
}

// MMJoinRoomRequest: POST /rooms/{code}/join
type MMJoinRoomRequest struct {
	PlayerID string `json:"player_id"`
	Name     string `json:"name"`
}

// MMPlayerRequest: POST /rooms/{code}/leave, POST /rooms/{code}/start
type MMPlayerRequest struct {
	PlayerID string `json:"player_id"`
}

// MMReadyRequest: POST /rooms/{code}/ready
type MMReadyRequest struct {
	PlayerID string `json:"player_id"`
	Ready    bool   `json:"ready"`
}

// MMMatchEndRequest: POST /rooms/{code}/end. Sent by the gateway when a
// match_end frame passes through /internal/push so the room returns to the
// lobby and can be started again.
type MMMatchEndRequest struct {
	MatchID string `json:"match_id"`
}

// MMRoomResponse is returned by every room mutation and GET /rooms/{code}.
// Recipients lists the player ids the gateway should fan the RoomState to.
type MMRoomResponse struct {
	Room       RoomState `json:"room"`
	Recipients []string  `json:"recipients"`
}

// ---- simulation service (8092) ----

// SimCreateMatchRequest: POST /matches. Called by matchmaking when a room starts.
type SimCreateMatchRequest struct {
	RoomCode string       `json:"room_code"`
	Config   RoomConfig   `json:"config"`
	Players  []RoomPlayer `json:"players"`
}

// SimCreateMatchResponse returns the per-player MatchStart frames the gateway
// must deliver (keyed by player id) so each client gets its own You block.
type SimCreateMatchResponse struct {
	MatchID string                `json:"match_id"`
	Starts  map[string]MatchStart `json:"starts"`
}

// SimInputRequest: POST /matches/{id}/input
type SimInputRequest struct {
	PlayerID string    `json:"player_id"`
	Seq      int64     `json:"seq"`
	Dir      Direction `json:"dir"`
}

// SimMatchStateResponse: GET /matches/{id}. Used for reconnect as spectator.
type SimMatchStateResponse struct {
	Start    MatchStart `json:"start"` // You is filled with role spectator
	Snapshot Snapshot   `json:"snapshot"`
	Ended    bool       `json:"ended"`
}

// ---- simulation -> gateway push (8090) ----

// GatewayPush: POST /internal/push. Simulation and matchmaking deliver frames
// to the gateway, which fans them out. Frames is a list of already encoded
// protocol messages (room_state, event, match_start, snapshot, match_end) as
// raw JSON. If PlayerIDs is empty every connection in the room receives every
// frame; otherwise only the listed players do (used for per-player
// match_start frames). RoomCode is always set; MatchID once a match exists.
type GatewayPush struct {
	MatchID   string            `json:"match_id,omitempty"`
	RoomCode  string            `json:"room_code"`
	PlayerIDs []string          `json:"player_ids,omitempty"`
	Frames    []json.RawMessage `json:"frames"`
}

// ---- stats service (8094) ----

// StatsResultRequest: POST /results. Sent by simulation at match end.
type StatsResultRequest struct {
	MatchID   string     `json:"match_id"`
	EndedAt   int64      `json:"ended_at"` // unix millis
	WinnerIDs []string   `json:"winner_ids"`
	Standings []Standing `json:"standings"`
}

// LeaderboardEntry is one row of GET /leaderboard?limit=&offset=.
type LeaderboardEntry struct {
	Name         string `json:"name"`
	Wins         int    `json:"wins"`
	Rounds       int    `json:"rounds"`
	TotalSurvive int64  `json:"total_survived_ms"`
}

type LeaderboardResponse struct {
	Entries []LeaderboardEntry `json:"entries"`
	Total   int                `json:"total"`
}

// ---- shared ----

// HTTPError is the body every service returns on a non-2xx response.
type HTTPError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HealthResponse: GET /healthz on every service.
type HealthResponse struct {
	Service string `json:"service"`
	Version string `json:"version"`
	OK      bool   `json:"ok"`
}
