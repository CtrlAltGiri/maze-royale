package simulation

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// openMaze returns a w x h maze with only the outer walls.
func openMaze(w, h int) protocol.Maze {
	walls := make(protocol.Walls, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var b byte
			if y == 0 {
				b |= protocol.WallNorth
			}
			if y == h-1 {
				b |= protocol.WallSouth
			}
			if x == 0 {
				b |= protocol.WallWest
			}
			if x == w-1 {
				b |= protocol.WallEast
			}
			walls[y*w+x] = b
		}
	}
	return protocol.Maze{W: w, H: h, Walls: walls}
}

type fixedMover struct {
	dir   protocol.Direction
	calls int
}

func (f *fixedMover) Next(g gridmath.Grid, c gridmath.Cell, _ protocol.Direction, _ []gridmath.Vec) protocol.Direction {
	f.calls++
	if g.CanMove(c, f.dir) {
		return f.dir
	}
	return protocol.DirNone
}

type recorder struct {
	mu      sync.Mutex
	pushes  []protocol.GatewayPush
	results []protocol.StatsResultRequest
	mazeReq []protocol.MazeGenerateRequest
}

func (r *recorder) frames(t string) []json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []json.RawMessage
	for _, p := range r.pushes {
		for _, f := range p.Frames {
			var env protocol.Envelope
			_ = json.Unmarshal(f, &env)
			if env.T == t {
				out = append(out, f)
			}
		}
	}
	return out
}

type fixture struct {
	svc  *Service
	rec  *recorder
	maze protocol.MazeGenerateResponse
}

func newFixture(t *testing.T, gen protocol.MazeGenerateResponse, mover func(int) Mover) *fixture {
	t.Helper()
	rec := &recorder{}
	mazeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/generate" || r.Header.Get(httpx.HeaderRequestID) == "" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		var req protocol.MazeGenerateRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		rec.mu.Lock()
		rec.mazeReq = append(rec.mazeReq, req)
		rec.mu.Unlock()
		httpx.WriteJSON(w, http.StatusOK, gen)
	}))
	t.Cleanup(mazeSrv.Close)
	now := time.UnixMilli(1_000_000)
	cfg := Config{
		MazeURL:    mazeSrv.URL,
		ManualTick: true,
		Now:        func() time.Time { return now },
		Push: func(_ context.Context, p protocol.GatewayPush) error {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.pushes = append(rec.pushes, p)
			return nil
		},
		Report: func(_ context.Context, r protocol.StatsResultRequest) error {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.results = append(rec.results, r)
			return nil
		},
	}
	if mover != nil {
		cfg.NewMover = func(_ int64, i int) Mover { return mover(i) }
	}
	return &fixture{svc: New(cfg), rec: rec, maze: gen}
}

func twoRunnerRequest() protocol.SimCreateMatchRequest {
	seed := int64(7)
	return protocol.SimCreateMatchRequest{
		RoomCode: "ABCD",
		Config:   protocol.RoomConfig{MazeSize: protocol.MazeSmall, Catchers: 1, CatcherSpeed: 1.0, RoundSeconds: 30, MinPlayers: 2, Seed: &seed},
		Players: []protocol.RoomPlayer{
			{PlayerID: "p1", Name: "Ann", Color: "#111111", IsCreator: true},
			{PlayerID: "p2", Name: "Bob", Color: "#222222"},
		},
	}
}

func postJSON(t *testing.T, h http.Handler, path string, body any, out any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if out != nil && w.Code < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, w.Body.String())
		}
	}
	return w
}

func TestCreateMatchAndSpectatorState(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 2.5, Y: 0.5}},
	}
	f := newFixture(t, gen, nil)
	h := f.svc.Handler()

	var resp protocol.SimCreateMatchResponse
	if w := postJSON(t, h, "/matches", twoRunnerRequest(), &resp); w.Code != http.StatusOK {
		t.Fatalf("POST /matches: %d %s", w.Code, w.Body.String())
	}
	if resp.MatchID == "" || len(resp.Starts) != 2 {
		t.Fatalf("bad response %+v", resp)
	}
	if got := f.rec.mazeReq[0]; got.W != 15 || got.H != 15 || got.Seed != 7 || got.Runners != 2 || got.Catchers != 1 {
		t.Fatalf("maze request %+v", got)
	}
	for _, id := range []string{"p1", "p2"} {
		s := resp.Starts[id]
		if s.T != protocol.TypeMatchStart || s.MatchID != resp.MatchID || s.You.PlayerID != id || s.You.Role != protocol.RoleRunner {
			t.Fatalf("start for %s: %+v", id, s.You)
		}
		if len(s.Players) != 2 || len(s.Catchers) != 1 || s.RoundMS != 30000 || s.Maze.W != 3 {
			t.Fatalf("start payload %+v", s)
		}
	}
	if resp.Starts["p1"].You.Color != "#111111" || resp.Starts["p2"].Players[1].X != 2.5 {
		t.Fatalf("spawn/color wiring wrong: %+v", resp.Starts["p1"])
	}

	req := httptest.NewRequest(http.MethodGet, "/matches/"+resp.MatchID, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var st protocol.SimMatchStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != 200 {
		t.Fatalf("GET: %d %v", w.Code, err)
	}
	if st.Start.You.Role != protocol.RoleSpectator || st.Ended || st.Snapshot.AliveCount != 2 || st.Snapshot.TimeLeftMS != 30000 {
		t.Fatalf("state %+v", st)
	}

	req = httptest.NewRequest(http.MethodGet, "/matches/nope", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing match -> %d", w.Code)
	}
}

func TestCreateMatchRejectsBadConfig(t *testing.T) {
	f := newFixture(t, protocol.MazeGenerateResponse{}, nil)
	req := twoRunnerRequest()
	req.Config.Catchers = 9
	if w := postJSON(t, f.svc.Handler(), "/matches", req, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
	req = twoRunnerRequest()
	req.Players = nil
	if w := postJSON(t, f.svc.Handler(), "/matches", req, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestInputMovesRunnerAndLatestSeqWins(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 2.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	h := f.svc.Handler()
	var resp protocol.SimCreateMatchResponse
	postJSON(t, h, "/matches", twoRunnerRequest(), &resp)
	m, _ := f.svc.Match(resp.MatchID)

	path := "/matches/" + resp.MatchID + "/input"
	if w := postJSON(t, h, path, protocol.SimInputRequest{PlayerID: "p1", Seq: 2, Dir: protocol.DirRight}, nil); w.Code != 200 {
		t.Fatalf("input: %d %s", w.Code, w.Body.String())
	}
	// Stale seq must not override.
	postJSON(t, h, path, protocol.SimInputRequest{PlayerID: "p1", Seq: 1, Dir: protocol.DirDown}, nil)
	if w := postJSON(t, h, path, protocol.SimInputRequest{PlayerID: "zz", Seq: 1, Dir: protocol.DirDown}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown player -> %d", w.Code)
	}
	if w := postJSON(t, h, path, protocol.SimInputRequest{PlayerID: "p1", Seq: 3, Dir: "diagonal"}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad dir -> %d", w.Code)
	}

	res := f.svc.Step(context.Background(), m)
	if res.Ended || len(res.Frames) != 1 {
		t.Fatalf("step result %+v", res)
	}
	var snap protocol.Snapshot
	_ = json.Unmarshal(res.Frames[0], &snap)
	if snap.T != protocol.TypeSnapshot || snap.Tick != 1 || snap.TS != 1_000_000 || snap.TimeLeftMS != 30000-50 {
		t.Fatalf("snapshot %+v", snap)
	}
	p1 := snap.Players[0]
	if math.Abs(p1.X-0.7) > 1e-9 || p1.Y != 0.5 || p1.Dir != protocol.DirRight || !p1.Alive {
		t.Fatalf("p1 after one tick %+v", p1)
	}
	if snap.Players[1].X != 2.5 || snap.Players[1].Dir != protocol.DirNone {
		t.Fatalf("idle runner moved %+v", snap.Players[1])
	}
	if len(f.rec.pushes) != 1 || f.rec.pushes[0].MatchID != resp.MatchID || f.rec.pushes[0].RoomCode != "ABCD" {
		t.Fatalf("push %+v", f.rec.pushes)
	}

	// Walking into the east wall stops at the last cell center.
	for i := 0; i < 40; i++ {
		f.svc.Step(context.Background(), m)
	}
	st := m.State()
	if st.Snapshot.Players[0].X != 2.5 || st.Snapshot.Players[0].Dir != protocol.DirNone {
		t.Fatalf("blocked runner %+v", st.Snapshot.Players[0])
	}
}

func TestCatchEliminatesAndLastRunnerWins(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 1.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirUp} })
	m, resp, err := f.svc.CreateMatch(context.Background(), twoRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	// Catcher at 4 cells/s closes 0.2 per tick; distance 1.0 -> 0.4 < 0.45 on tick 3.
	var last StepResult
	for i := 0; i < 10 && !last.Ended; i++ {
		last = f.svc.Step(context.Background(), m)
	}
	if !last.Ended || last.End == nil || m.Tick() != 3 {
		t.Fatalf("expected end at tick 3, got tick %d ended=%v", m.Tick(), last.Ended)
	}
	events := f.rec.frames(protocol.TypeEvent)
	if len(events) != 1 {
		t.Fatalf("events %v", events)
	}
	var ev protocol.Event
	_ = json.Unmarshal(events[0], &ev)
	if ev.Kind != protocol.EventCaught || ev.PlayerID != "p1" || ev.CatcherID != "c1" || ev.Tick != 3 {
		t.Fatalf("caught event %+v", ev)
	}
	end := *last.End
	if end.T != protocol.TypeMatchEnd || end.MatchID != resp.MatchID || len(end.WinnerIDs) != 1 || end.WinnerIDs[0] != "p2" {
		t.Fatalf("match_end %+v", end)
	}
	if len(end.Standings) != 2 || end.Standings[0].PlayerID != "p2" || end.Standings[0].Place != 1 || end.Standings[0].SurvivedMS != 150 ||
		end.Standings[1].PlayerID != "p1" || end.Standings[1].Place != 2 || end.Standings[1].SurvivedMS != 150 || end.Standings[1].Name != "Ann" {
		t.Fatalf("standings %+v", end.Standings)
	}
	// Last push carries snapshot, event and match_end in order.
	lastPush := f.rec.pushes[len(f.rec.pushes)-1]
	if len(lastPush.Frames) != 3 {
		t.Fatalf("final push frames %d", len(lastPush.Frames))
	}
	var snap protocol.Snapshot
	_ = json.Unmarshal(lastPush.Frames[0], &snap)
	if snap.AliveCount != 1 || snap.Players[0].Alive {
		t.Fatalf("final snapshot %+v", snap)
	}
	if len(f.rec.results) != 1 || f.rec.results[0].MatchID != resp.MatchID || f.rec.results[0].WinnerIDs[0] != "p2" || f.rec.results[0].EndedAt != 1_000_000 {
		t.Fatalf("stats %+v", f.rec.results)
	}
	if st := m.State(); !st.Ended {
		t.Fatal("state should report ended")
	}
	// Stepping an ended match is a no-op.
	if r := f.svc.Step(context.Background(), m); !r.Ended || len(r.Frames) != 0 || len(f.rec.results) != 1 {
		t.Fatalf("post-end step %+v", r)
	}
}

func TestRoundTimerExpiryMakesSurvivorsWinners(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 2.5, Y: 0.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	m, _, err := f.svc.CreateMatch(context.Background(), twoRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	var last StepResult
	ticks := 0
	for !last.Ended && ticks < 1000 {
		last = f.svc.Step(context.Background(), m)
		ticks++
	}
	if ticks != 600 {
		t.Fatalf("30 s round should end on tick 600, ended on %d", ticks)
	}
	if len(last.End.WinnerIDs) != 2 || last.End.Standings[0].Place != 1 || last.End.Standings[1].Place != 1 || last.End.Standings[1].SurvivedMS != 30000 {
		t.Fatalf("end %+v", last.End)
	}
	var snap protocol.Snapshot
	_ = json.Unmarshal(last.Frames[0], &snap)
	if snap.TimeLeftMS != 0 {
		t.Fatalf("time_left_ms %d", snap.TimeLeftMS)
	}
}

func TestCatcherOnlyTurnsAtCellCenters(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(5, 5), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 4.5, Y: 4.5}, {X: 4.5, Y: 0.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 0.5}},
	}
	mv := &fixedMover{dir: protocol.DirRight}
	f := newFixture(t, gen, func(int) Mover { return mv })
	req := twoRunnerRequest()
	req.Config.CatcherSpeed = 0.6 // 2.4 cells/s = 0.12 cells/tick
	m, _, err := f.svc.CreateMatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		f.svc.Step(context.Background(), m)
	}
	// 8 ticks cover 0.96 cells: still short of the next center, so one decision.
	if mv.calls != 1 {
		t.Fatalf("mover consulted %d times mid-corridor", mv.calls)
	}
	c := m.State().Snapshot.Catchers[0]
	if math.Abs(c.X-(0.5+0.96)) > 1e-9 || c.Dir != protocol.DirRight {
		t.Fatalf("catcher %+v", c)
	}
	f.svc.Step(context.Background(), m)
	if mv.calls != 2 {
		t.Fatalf("mover should decide again on reaching the center, calls=%d", mv.calls)
	}
}

func TestRandomWalkNeverReversesExceptDeadEnd(t *testing.T) {
	// 3x1 corridor.
	g := gridmath.Grid{M: openMaze(3, 1)}
	rw := NewRandomWalk(1)
	if d := rw.Next(g, gridmath.Cell{X: 1, Y: 0}, protocol.DirRight, nil); d != protocol.DirRight {
		t.Fatalf("corridor: want right, got %s", d)
	}
	if d := rw.Next(g, gridmath.Cell{X: 2, Y: 0}, protocol.DirRight, nil); d != protocol.DirLeft {
		t.Fatalf("dead end: want left, got %s", d)
	}
	// Open grid intersection: never the reverse, always passable.
	open := gridmath.Grid{M: openMaze(3, 3)}
	for i := 0; i < 200; i++ {
		d := rw.Next(open, gridmath.Cell{X: 1, Y: 1}, protocol.DirUp, nil)
		if d == protocol.DirDown || !open.CanMove(gridmath.Cell{X: 1, Y: 1}, d) {
			t.Fatalf("intersection picked %s", d)
		}
	}
}

func TestMatchEndPostsToGatewayAndStatsOverHTTP(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 1.5}},
	}
	var mu sync.Mutex
	var pushes []protocol.GatewayPush
	var results []protocol.StatsResultRequest
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p protocol.GatewayPush
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		pushes = append(pushes, p)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer gw.Close()
	stats := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p protocol.StatsResultRequest
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		results = append(results, p)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer stats.Close()
	mazeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, gen)
	}))
	defer mazeSrv.Close()

	svc := New(Config{MazeURL: mazeSrv.URL, GatewayURL: gw.URL, StatsURL: stats.URL, ManualTick: true,
		NewMover: func(int64, int) Mover { return &fixedMover{dir: protocol.DirUp} }})
	m, _, err := svc.CreateMatch(context.Background(), twoRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	for !svc.Step(context.Background(), m).Ended {
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		np, nr := len(pushes), len(results)
		mu.Unlock()
		if np == 3 && nr == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pushes=%d results=%d", np, nr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if pushes[0].RoomCode != "ABCD" || pushes[0].MatchID != m.ID() || len(pushes[2].Frames) != 3 {
		t.Fatalf("pushes %+v", pushes)
	}
}
