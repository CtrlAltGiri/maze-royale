package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestGatewayPushRoundTrip(t *testing.T) {
	snap, _ := Encode(Snapshot{T: TypeSnapshot, Tick: 1, Players: []SnapshotPlayer{}, Catchers: []SnapshotCatcher{}})
	in := GatewayPush{MatchID: "m1", RoomCode: "ABCD", PlayerIDs: []string{"p1"}, Frames: []json.RawMessage{snap}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out GatewayPush
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.MatchID != "m1" || out.RoomCode != "ABCD" || len(out.Frames) != 1 || !reflect.DeepEqual(out.PlayerIDs, in.PlayerIDs) {
		t.Fatalf("mismatch %+v", out)
	}
	msg, err := Decode(out.Frames[0])
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := msg.(*Snapshot); !ok || s.Tick != 1 {
		t.Fatalf("frame did not decode to snapshot: %#v", msg)
	}
	// frames must be embedded as raw JSON, not base64 strings
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	if _, ok := generic["frames"].([]any)[0].(map[string]any); !ok {
		t.Fatalf("frames should embed raw JSON objects: %s", raw)
	}
}

func TestServiceBodiesRoundTrip(t *testing.T) {
	cases := []any{
		&MazeGenerateRequest{W: 21, H: 21, Seed: 7, Runners: 4, Catchers: 2},
		&MazeGenerateResponse{Maze: Maze{W: 1, H: 1, Walls: Walls{15}}, Seed: 7, RunnerSpawns: []Point{{0.5, 0.5}}, CatcherSpawns: []Point{{0.5, 0.5}}},
		&MMCreateRoomRequest{PlayerID: "p1", Name: "a", Config: DefaultRoomConfig()},
		&MMRoomResponse{Room: RoomState{T: TypeRoomState, Code: "X", Config: DefaultRoomConfig(), Players: []RoomPlayer{}}, Recipients: []string{"p1"}},
		&SimCreateMatchRequest{RoomCode: "X", Config: DefaultRoomConfig(), Players: []RoomPlayer{{PlayerID: "p1"}}},
		&SimCreateMatchResponse{MatchID: "m", Starts: map[string]MatchStart{"p1": {T: TypeMatchStart, MatchID: "m", Maze: Maze{Walls: Walls{}}, Players: []MatchPlayer{}, Catchers: []MatchCatcher{}}}},
		&SimInputRequest{PlayerID: "p1", Seq: 1, Dir: DirUp},
		&StatsResultRequest{MatchID: "m", EndedAt: 1, WinnerIDs: []string{"p1"}, Standings: []Standing{{PlayerID: "p1", Name: "a", SurvivedMS: 1, Place: 1}}},
		&LeaderboardResponse{Entries: []LeaderboardEntry{{Name: "a", Wins: 1, Rounds: 1, TotalSurvive: 5}}, Total: 1},
	}
	for _, in := range cases {
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		out := reflect.New(reflect.TypeOf(in).Elem()).Interface()
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%T: %v", in, err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Errorf("%T mismatch:\n in=%#v\nout=%#v", in, in, out)
		}
	}
}
