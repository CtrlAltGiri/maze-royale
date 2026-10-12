package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func roundTrip(t *testing.T, in any) any {
	t.Helper()
	raw, err := Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := Decode(raw)
	if err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

func TestRoundTripAllMessages(t *testing.T) {
	seed := int64(42)
	cfg := RoomConfig{MazeSize: MazeMedium, Catchers: 2, CatcherSpeed: 1.0, RoundSeconds: 120, MinPlayers: 3, Seed: &seed}
	cases := []any{
		&Hello{T: TypeHello, Name: "giri"},
		&CreateRoom{T: TypeCreateRoom, Config: cfg},
		&JoinRoom{T: TypeJoinRoom, Code: "ABCD"},
		&LeaveRoom{T: TypeLeaveRoom},
		&SetReady{T: TypeSetReady, Ready: true},
		&StartRoom{T: TypeStartRoom},
		&Input{T: TypeInput, Seq: 7, Dir: DirLeft},
		&Shoot{T: TypeShoot},
		&Ping{T: TypePing, TS: 1234},
		&Welcome{T: TypeWelcome, PlayerID: "p1"},
		&RoomState{T: TypeRoomState, Code: "ABCD", Config: cfg, Players: []RoomPlayer{{PlayerID: "p1", Name: "a", Color: "#fff", Ready: true, IsCreator: true}}, CountdownMS: 5000},
		&MatchStart{T: TypeMatchStart, MatchID: "m1", Maze: Maze{W: 3, H: 3, Walls: Walls{15, 15, 15, 15, 0, 15, 15, 15, 15}},
			You:      You{PlayerID: "p1", Role: RoleRunner, Color: "#fff"},
			Players:  []MatchPlayer{{PlayerID: "p1", Name: "a", Role: RoleRunner, Color: "#fff", X: 0.5, Y: 0.5}},
			Catchers: []MatchCatcher{{ID: "c1", X: 2.5, Y: 2.5}},
			PowerUps: []PowerUp{{ID: "power1", Kind: PowerUpSpeed, X: 1.5, Y: 1.5}, {ID: "power2", Kind: PowerUpInvisibility, X: 0.5, Y: 2.5}},
			RoundMS:  120000},
		&Snapshot{T: TypeSnapshot, Tick: 10, TS: 99, Players: []SnapshotPlayer{{ID: "p1", X: 1.5, Y: 0.5, Dir: DirRight, Alive: true, SpeedBoostMS: 2500, InvisibleMS: 1000, HasGun: true}},
			Catchers: []SnapshotCatcher{{ID: "c1", X: 2.5, Y: 2.5, Dir: DirNone, DownMS: 1500}},
			PowerUps: []PowerUp{{ID: "power2", Kind: PowerUpInvisibility, X: 0.5, Y: 2.5}},
			Bullets:  []Bullet{{ID: "b1", X: 1.2, Y: 0.5, Dir: DirRight}}, TimeLeftMS: 1000, AliveCount: 1},
		&Event{T: TypeEvent, Kind: EventCaught, PlayerID: "p1", CatcherID: "c1", Tick: 10},
		&Event{T: TypeEvent, Kind: EventGunFired, PlayerID: "p1", Tick: 11},
		&Event{T: TypeEvent, Kind: EventCatcherDown, CatcherID: "c1", Tick: 12, MS: 3000},
		&Event{T: TypeEvent, Kind: EventCatcherRespawn, CatcherID: "c1", Tick: 72},
		&Event{T: TypeEvent, Kind: EventPowerUpCollected, PlayerID: "p1", Tick: 10, MS: 5000, PowerUpID: "power1", PowerUpKind: PowerUpSpeed},
		&Event{T: TypeEvent, Kind: EventCountdown, MS: 3000},
		&MatchEnd{T: TypeMatchEnd, MatchID: "m1", WinnerIDs: []string{"p1"}, Standings: []Standing{{PlayerID: "p1", Name: "a", SurvivedMS: 5000, Place: 1}}},
		&Error{T: TypeError, Code: ErrBadRequest, Message: "nope"},
		&Pong{T: TypePong, TS: 1234},
	}
	for _, in := range cases {
		out := roundTrip(t, in)
		if !reflect.DeepEqual(in, out) {
			t.Errorf("round trip mismatch:\n in=%#v\nout=%#v", in, out)
		}
	}
}

func TestDecodeUnknownType(t *testing.T) {
	if _, err := Decode([]byte(`{"t":"bogus"}`)); err == nil {
		t.Fatal("expected error for unknown type")
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Fatal("expected error for bad json")
	}
}

func TestWireFieldNames(t *testing.T) {
	raw, _ := Encode(Snapshot{T: TypeSnapshot, Players: []SnapshotPlayer{}, Catchers: []SnapshotCatcher{}, PowerUps: []PowerUp{}, Bullets: []Bullet{}})
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"t", "tick", "ts", "players", "catchers", "power_ups", "bullets", "time_left_ms", "alive_count"} {
		if _, ok := m[k]; !ok {
			t.Errorf("snapshot missing wire field %q in %s", k, raw)
		}
	}
	if _, ok := m["power_ups"].([]any); !ok {
		t.Errorf("power_ups must encode as [] not null in %s", raw)
	}
	if _, ok := m["bullets"].([]any); !ok {
		t.Errorf("bullets must encode as [] not null in %s", raw)
	}
	raw, _ = Encode(RoomState{T: TypeRoomState, Players: []RoomPlayer{{}}})
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	p := m["players"].([]any)[0].(map[string]any)
	for _, k := range []string{"player_id", "name", "color", "ready", "is_creator"} {
		if _, ok := p[k]; !ok {
			t.Errorf("room_state player missing %q", k)
		}
	}
}

func TestMazeWallsEncodeAsIntArray(t *testing.T) {
	raw, _ := json.Marshal(Maze{W: 2, H: 1, Walls: Walls{1, 15}})
	if string(raw) != `{"w":2,"h":1,"walls":[1,15]}` {
		t.Fatalf("unexpected maze encoding %s", raw)
	}
	var m Maze
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Walls) != 2 || m.Walls[1] != 15 {
		t.Fatalf("decode: %v %+v", err, m)
	}
	if err := json.Unmarshal([]byte(`{"w":1,"h":1,"walls":[16]}`), &m); err == nil {
		t.Fatal("wall byte 16 should be rejected")
	}
}

func TestRoomConfigValidate(t *testing.T) {
	good := DefaultRoomConfig()
	if err := good.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	bad := []RoomConfig{
		func() RoomConfig { c := good; c.MazeSize = "huge"; return c }(),
		func() RoomConfig { c := good; c.Catchers = 0; return c }(),
		func() RoomConfig { c := good; c.Catchers = -1; return c }(),
		func() RoomConfig { c := good; c.CatcherSpeed = 0.5; return c }(),
		func() RoomConfig { c := good; c.CatcherSpeed = 1.5; return c }(),
		func() RoomConfig { c := good; c.RoundSeconds = 29; return c }(),
		func() RoomConfig { c := good; c.RoundSeconds = 301; return c }(),
		func() RoomConfig { c := good; c.MinPlayers = 1; return c }(),
		func() RoomConfig { c := good; c.MinPlayers = 9; return c }(),
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d should be invalid: %+v", i, c)
		}
	}
}

func TestDirectionValid(t *testing.T) {
	for _, d := range []Direction{DirUp, DirDown, DirLeft, DirRight, DirNone} {
		if !d.Valid() {
			t.Errorf("%q should be valid", d)
		}
	}
	if Direction("diagonal").Valid() {
		t.Error("diagonal should be invalid")
	}
}

func TestMazeDimension(t *testing.T) {
	for size, want := range map[string]int{MazeSmall: 15, MazeMedium: 21, MazeLarge: 31} {
		if got, ok := MazeDimension(size); !ok || got != want {
			t.Errorf("%s: got %d,%v want %d", size, got, ok, want)
		}
	}
	if _, ok := MazeDimension("x"); ok {
		t.Error("unknown size accepted")
	}
}

func TestRoomConfigAllowsCatchersAboveFormerCaps(t *testing.T) {
	for _, count := range []int{5, 17, 32, 1000} {
		c := DefaultRoomConfig()
		c.Catchers = count
		if err := c.Validate(); err != nil {
			t.Fatalf("%d catchers: %v", count, err)
		}
	}
}
