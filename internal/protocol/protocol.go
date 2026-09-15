// Package protocol defines the frozen wire contract for Maze Royale.
// Every websocket frame and every inter-service HTTP body is a JSON object
// with a "t" field naming the message type. See docs/CONTRACT.md.
//
// This package must not be edited by fan-out agents.
package protocol

import (
	"encoding/json"
	"fmt"
)

// Version is the wire contract version. Bump only with docs/CONTRACT.md.
const Version = "1"

// Message type tags, client -> gateway.
const (
	TypeHello      = "hello"
	TypeCreateRoom = "create_room"
	TypeJoinRoom   = "join_room"
	TypeLeaveRoom  = "leave_room"
	TypeSetReady   = "set_ready"
	TypeStartRoom  = "start_room"
	TypeInput      = "input"
	TypePing       = "ping"
)

// Message type tags, gateway -> client.
const (
	TypeWelcome    = "welcome"
	TypeRoomState  = "room_state"
	TypeMatchStart = "match_start"
	TypeSnapshot   = "snapshot"
	TypeEvent      = "event"
	TypeMatchEnd   = "match_end"
	TypeError      = "error"
	TypePong       = "pong"
)

// Event kinds carried by Event.Kind.
const (
	EventCaught    = "caught"
	EventCountdown = "countdown"
	EventBotJoined = "bot_joined"
)

// Roles assigned to entities in a match.
const (
	RoleRunner    = "runner"
	RoleCatcher   = "catcher"
	RoleSpectator = "spectator"
)

// Direction values carried by Input.Dir and snapshot entities.
type Direction string

const (
	DirUp    Direction = "up"
	DirDown  Direction = "down"
	DirLeft  Direction = "left"
	DirRight Direction = "right"
	DirNone  Direction = "none"
)

// Valid reports whether d is one of the contract directions.
func (d Direction) Valid() bool {
	switch d {
	case DirUp, DirDown, DirLeft, DirRight, DirNone:
		return true
	}
	return false
}

// Wall bits for maze cells. walls is row major, one byte per cell.
const (
	WallNorth byte = 1 << 0
	WallEast  byte = 1 << 1
	WallSouth byte = 1 << 2
	WallWest  byte = 1 << 3
)

// Movement constants (cells per second, cells).
const (
	RunnerSpeed  = 4.0
	CatchRadius  = 0.45
	TickRate     = 20
	TickMillis   = 1000 / TickRate
	CountdownMS  = 5000
	ServerTickHz = TickRate
)

// Room configuration limits.
const (
	MinCatchers     = 1
	MaxCatchers     = 4
	MinCatcherSpeed = 0.6
	MaxCatcherSpeed = 1.4
	MinRoundSeconds = 30
	MaxRoundSeconds = 300
	MinMinPlayers   = 2
	MaxMinPlayers   = 8
)

// Maze sizes. Width and height are always equal and odd.
const (
	MazeSmall  = "small"
	MazeMedium = "medium"
	MazeLarge  = "large"
)

// MazeDimension returns the side length for a maze size name.
func MazeDimension(size string) (int, bool) {
	switch size {
	case MazeSmall:
		return 15, true
	case MazeMedium:
		return 21, true
	case MazeLarge:
		return 31, true
	}
	return 0, false
}

// Envelope is the minimal shape every message shares. Decode into it first,
// then dispatch on T.
type Envelope struct {
	T string `json:"t"`
}

// RoomConfig is the configuration a creator sets on a room.
type RoomConfig struct {
	MazeSize     string  `json:"maze_size"`     // small | medium | large
	Catchers     int     `json:"catchers"`      // 1..4
	CatcherSpeed float64 `json:"catcher_speed"` // 0.6..1.4
	RoundSeconds int     `json:"round_seconds"` // 30..300
	MinPlayers   int     `json:"min_players"`   // 2..8
	FillBots     bool    `json:"fill_bots"`
	Seed         *int64  `json:"seed,omitempty"` // nil means random
}

// Validate checks every field against the contract ranges.
func (c RoomConfig) Validate() error {
	if _, ok := MazeDimension(c.MazeSize); !ok {
		return fmt.Errorf("maze_size must be small, medium or large")
	}
	if c.Catchers < MinCatchers || c.Catchers > MaxCatchers {
		return fmt.Errorf("catchers must be between %d and %d", MinCatchers, MaxCatchers)
	}
	if c.CatcherSpeed < MinCatcherSpeed || c.CatcherSpeed > MaxCatcherSpeed {
		return fmt.Errorf("catcher_speed must be between %.1f and %.1f", MinCatcherSpeed, MaxCatcherSpeed)
	}
	if c.RoundSeconds < MinRoundSeconds || c.RoundSeconds > MaxRoundSeconds {
		return fmt.Errorf("round_seconds must be between %d and %d", MinRoundSeconds, MaxRoundSeconds)
	}
	if c.MinPlayers < MinMinPlayers || c.MinPlayers > MaxMinPlayers {
		return fmt.Errorf("min_players must be between %d and %d", MinMinPlayers, MaxMinPlayers)
	}
	return nil
}

// DefaultRoomConfig is the config used when a client omits fields.
func DefaultRoomConfig() RoomConfig {
	return RoomConfig{MazeSize: MazeMedium, Catchers: 2, CatcherSpeed: 1.0, RoundSeconds: 120, MinPlayers: 2}
}

// ---- Client -> gateway ----

type Hello struct {
	T    string `json:"t"`
	Name string `json:"name"`
}

type CreateRoom struct {
	T      string     `json:"t"`
	Config RoomConfig `json:"config"`
}

type JoinRoom struct {
	T    string `json:"t"`
	Code string `json:"code"`
}

type LeaveRoom struct {
	T string `json:"t"`
}

type SetReady struct {
	T     string `json:"t"`
	Ready bool   `json:"ready"`
}

type StartRoom struct {
	T string `json:"t"`
}

type Input struct {
	T   string    `json:"t"`
	Seq int64     `json:"seq"`
	Dir Direction `json:"dir"`
}

type Ping struct {
	T  string `json:"t"`
	TS int64  `json:"ts"`
}

// ---- Gateway -> client ----

type Welcome struct {
	T        string `json:"t"`
	PlayerID string `json:"player_id"`
}

// RoomPlayer is one entry in the lobby roster.
type RoomPlayer struct {
	PlayerID  string `json:"player_id"`
	Name      string `json:"name"`
	Color     string `json:"color"` // CSS hex, e.g. "#4fc3f7"
	Ready     bool   `json:"ready"`
	IsCreator bool   `json:"is_creator"`
	IsBot     bool   `json:"is_bot,omitempty"`
}

type RoomState struct {
	T           string       `json:"t"`
	Code        string       `json:"code"`
	Config      RoomConfig   `json:"config"`
	Players     []RoomPlayer `json:"players"`
	CountdownMS int64        `json:"countdown_ms"` // 0 when no countdown is running
}

// Walls is a row major array of cell wall bytes: bit0 N, bit1 E, bit2 S, bit3 W.
// It marshals as a JSON array of integers (not base64) so the JS client can
// index it directly.
type Walls []byte

func (w Walls) MarshalJSON() ([]byte, error) {
	ints := make([]int, len(w))
	for i, b := range w {
		ints[i] = int(b)
	}
	return json.Marshal(ints)
}

func (w *Walls) UnmarshalJSON(data []byte) error {
	var ints []int
	if err := json.Unmarshal(data, &ints); err != nil {
		return err
	}
	out := make(Walls, len(ints))
	for i, v := range ints {
		if v < 0 || v > 15 {
			return fmt.Errorf("protocol: wall byte %d out of range at %d", v, i)
		}
		out[i] = byte(v)
	}
	*w = out
	return nil
}

// Maze is the wall layout. len(Walls) == W*H. Cell (x, y) is Walls[y*W+x].
type Maze struct {
	W     int   `json:"w"`
	H     int   `json:"h"`
	Walls Walls `json:"walls"`
}

// MatchPlayer describes a runner at match start.
type MatchPlayer struct {
	PlayerID string  `json:"player_id"`
	Name     string  `json:"name"`
	Role     string  `json:"role"`
	Color    string  `json:"color"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	IsBot    bool    `json:"is_bot,omitempty"`
}

// MatchCatcher describes a catcher at match start.
type MatchCatcher struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

// You tells the receiving client which entity is theirs.
type You struct {
	PlayerID string `json:"player_id"`
	Role     string `json:"role"`
	Color    string `json:"color"`
}

type MatchStart struct {
	T        string         `json:"t"`
	MatchID  string         `json:"match_id"`
	Maze     Maze           `json:"maze"`
	You      You            `json:"you"`
	Players  []MatchPlayer  `json:"players"`
	Catchers []MatchCatcher `json:"catchers"`
	RoundMS  int64          `json:"round_ms"`
}

// SnapshotPlayer is a runner's authoritative state at a tick.
type SnapshotPlayer struct {
	ID    string    `json:"id"`
	X     float64   `json:"x"`
	Y     float64   `json:"y"`
	Dir   Direction `json:"dir"`
	Alive bool      `json:"alive"`
}

// SnapshotCatcher is a catcher's authoritative state at a tick.
type SnapshotCatcher struct {
	ID  string    `json:"id"`
	X   float64   `json:"x"`
	Y   float64   `json:"y"`
	Dir Direction `json:"dir"`
}

type Snapshot struct {
	T          string            `json:"t"`
	Tick       int64             `json:"tick"`
	TS         int64             `json:"ts"` // server unix millis
	Players    []SnapshotPlayer  `json:"players"`
	Catchers   []SnapshotCatcher `json:"catchers"`
	TimeLeftMS int64             `json:"time_left_ms"`
	AliveCount int               `json:"alive_count"`
}

// Event is a discrete in-match or lobby happening. Fields beyond Kind are
// populated depending on Kind:
//   - caught:     PlayerID (victim), CatcherID, Tick
//   - countdown:  MS remaining until start
//   - bot_joined: PlayerID, Name
type Event struct {
	T         string `json:"t"`
	Kind      string `json:"kind"`
	PlayerID  string `json:"player_id,omitempty"`
	CatcherID string `json:"catcher_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Tick      int64  `json:"tick,omitempty"`
	MS        int64  `json:"ms,omitempty"`
}

// Standing is one row of the end of match results.
type Standing struct {
	PlayerID   string `json:"player_id"`
	Name       string `json:"name"`
	SurvivedMS int64  `json:"survived_ms"`
	Place      int    `json:"place"`
}

type MatchEnd struct {
	T         string     `json:"t"`
	MatchID   string     `json:"match_id"`
	WinnerIDs []string   `json:"winner_ids"`
	Standings []Standing `json:"standings"`
}

// Error codes.
const (
	ErrBadRequest   = "bad_request"
	ErrNotFound     = "not_found"
	ErrForbidden    = "forbidden"
	ErrRoomFull     = "room_full"
	ErrInMatch      = "in_match"
	ErrNotInRoom    = "not_in_room"
	ErrInternal     = "internal"
	ErrUnsupported  = "unsupported"
	ErrNotImplement = "not_implemented"
)

type Error struct {
	T       string `json:"t"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Pong struct {
	T  string `json:"t"`
	TS int64  `json:"ts"`
}

// Decode unmarshals a raw frame into the concrete message type named by its
// "t" field. The returned value is a pointer to one of the message structs.
func Decode(raw []byte) (any, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("protocol: bad envelope: %w", err)
	}
	var msg any
	switch env.T {
	case TypeHello:
		msg = &Hello{}
	case TypeCreateRoom:
		msg = &CreateRoom{}
	case TypeJoinRoom:
		msg = &JoinRoom{}
	case TypeLeaveRoom:
		msg = &LeaveRoom{}
	case TypeSetReady:
		msg = &SetReady{}
	case TypeStartRoom:
		msg = &StartRoom{}
	case TypeInput:
		msg = &Input{}
	case TypePing:
		msg = &Ping{}
	case TypeWelcome:
		msg = &Welcome{}
	case TypeRoomState:
		msg = &RoomState{}
	case TypeMatchStart:
		msg = &MatchStart{}
	case TypeSnapshot:
		msg = &Snapshot{}
	case TypeEvent:
		msg = &Event{}
	case TypeMatchEnd:
		msg = &MatchEnd{}
	case TypeError:
		msg = &Error{}
	case TypePong:
		msg = &Pong{}
	default:
		return nil, fmt.Errorf("protocol: unknown message type %q", env.T)
	}
	if err := json.Unmarshal(raw, msg); err != nil {
		return nil, fmt.Errorf("protocol: decode %s: %w", env.T, err)
	}
	return msg, nil
}

// Encode marshals a message. It is a thin wrapper kept so call sites read
// symmetrically with Decode.
func Encode(msg any) ([]byte, error) {
	return json.Marshal(msg)
}

// NewError builds an Error frame.
func NewError(code, message string) Error {
	return Error{T: TypeError, Code: code, Message: message}
}
