package simulation

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// threeRunnerRequest seats p1..p3 so a single catch leaves the match running.
func threeRunnerRequest() protocol.SimCreateMatchRequest {
	req := twoRunnerRequest()
	req.Config.MinPlayers = 3
	req.Players = append(req.Players, protocol.RoomPlayer{PlayerID: "p3", Name: "Cid", Color: "#333333"})
	return req
}

// wallMaze is a 3x3 open maze with an interior wall between (1,0) and (2,0).
func wallMaze() protocol.Maze {
	m := openMaze(3, 3)
	m.Walls[0*3+1] |= protocol.WallEast
	m.Walls[0*3+2] |= protocol.WallWest
	return m
}

func stepN(f *fixture, m *Match, n int) StepResult {
	var last StepResult
	for i := 0; i < n && !last.Ended; i++ {
		last = f.svc.Step(context.Background(), m)
	}
	return last
}

func player(t *testing.T, m *Match, id string) protocol.SnapshotPlayer {
	t.Helper()
	for _, p := range m.State().Snapshot.Players {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("player %s not in snapshot", id)
	return protocol.SnapshotPlayer{}
}

func TestTickLoopAdvancesClockAndTimeLeft(t *testing.T) {
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
	if m.Tick() != 0 || m.Ended() {
		t.Fatalf("fresh match tick=%d ended=%v", m.Tick(), m.Ended())
	}
	for i := 1; i <= 5; i++ {
		res := f.svc.Step(context.Background(), m)
		if res.Ended || len(res.Frames) != 1 {
			t.Fatalf("tick %d: %+v", i, res)
		}
		var snap protocol.Snapshot
		_ = json.Unmarshal(res.Frames[0], &snap)
		if snap.Tick != int64(i) || m.Tick() != int64(i) {
			t.Fatalf("tick %d reported as %d/%d", i, snap.Tick, m.Tick())
		}
		if want := int64(30000 - i*protocol.TickMillis); snap.TimeLeftMS != want {
			t.Fatalf("tick %d time_left=%d want %d", i, snap.TimeLeftMS, want)
		}
		if snap.AliveCount != 2 {
			t.Fatalf("alive %d", snap.AliveCount)
		}
	}
	if len(f.rec.pushes) != 5 {
		t.Fatalf("every tick must push exactly one frame batch, got %d", len(f.rec.pushes))
	}
}

func TestRunnerStopsAtInteriorWallAndCanTurnAway(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: wallMaze(), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 2.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	m, _, err := f.svc.CreateMatch(context.Background(), twoRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	m.Input("p1", 1, protocol.DirRight)
	// 4 cells/s * 50ms = 0.2 per tick; center of (1,0) is 1.0 away = 5 ticks.
	stepN(f, m, 5)
	p := player(t, m, "p1")
	if math.Abs(p.X-1.5) > 1e-9 || p.Y != 0.5 {
		t.Fatalf("after 5 ticks %+v", p)
	}
	// The wall east of (1,0) blocks further travel; the runner parks on the center.
	stepN(f, m, 10)
	p = player(t, m, "p1")
	if p.X != 1.5 || p.Y != 0.5 || p.Dir != protocol.DirNone || !p.Alive {
		t.Fatalf("runner should be blocked at (1.5,0.5): %+v", p)
	}
	// Turning down is open: movement resumes along the new lane.
	m.Input("p1", 2, protocol.DirDown)
	stepN(f, m, 5)
	p = player(t, m, "p1")
	if p.X != 1.5 || math.Abs(p.Y-1.5) > 1e-9 || p.Dir != protocol.DirDown {
		t.Fatalf("runner should have moved down to (1.5,1.5): %+v", p)
	}
	// And the outer boundary is a wall too: keep going down and park at row 2.
	stepN(f, m, 20)
	p = player(t, m, "p1")
	if p.X != 1.5 || p.Y != 2.5 || p.Dir != protocol.DirNone {
		t.Fatalf("runner should be blocked at the south boundary: %+v", p)
	}
	if m.Ended() {
		t.Fatal("moving into walls must not end the match")
	}
}

func TestRunnerCannotLeaveMazeThroughBoundary(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 1.5, Y: 2.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	m, _, err := f.svc.CreateMatch(context.Background(), twoRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	m.Input("p1", 1, protocol.DirLeft)
	m.Input("p2", 1, protocol.DirRight)
	stepN(f, m, 10)
	if p := player(t, m, "p1"); p.X != 0.5 || p.Y != 0.5 || p.Dir != protocol.DirNone {
		t.Fatalf("p1 left the maze: %+v", p)
	}
	if p := player(t, m, "p2"); p.X != 2.5 || p.Y != 2.5 || p.Dir != protocol.DirNone {
		t.Fatalf("p2 left the maze: %+v", p)
	}
}

func TestDeadRunnerIgnoresInputAndMatchContinuesWithThree(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}, {X: 2.5, Y: 0.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 1.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirUp} })
	m, _, err := f.svc.CreateMatch(context.Background(), threeRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	// Catcher closes 0.2 per tick from distance 1.0: caught on tick 3.
	res := stepN(f, m, 3)
	if res.Ended || m.Tick() != 3 {
		t.Fatalf("one catch with three runners must not end the match: ended=%v tick=%d", res.Ended, m.Tick())
	}
	if len(res.Frames) != 2 {
		t.Fatalf("catch tick should push snapshot + event, got %d frames", len(res.Frames))
	}
	var ev protocol.Event
	_ = json.Unmarshal(res.Frames[1], &ev)
	if ev.Kind != protocol.EventCaught || ev.PlayerID != "p1" || ev.CatcherID != "c1" || ev.Tick != 3 {
		t.Fatalf("caught event %+v", ev)
	}
	var snap protocol.Snapshot
	_ = json.Unmarshal(res.Frames[0], &snap)
	if snap.AliveCount != 2 {
		t.Fatalf("alive after catch %d", snap.AliveCount)
	}
	dead := player(t, m, "p1")
	if dead.Alive || dead.Dir != protocol.DirNone {
		t.Fatalf("dead runner %+v", dead)
	}
	// Input from the eliminated runner is accepted (still in match) but has no effect.
	if !m.Input("p1", 5, protocol.DirRight) {
		t.Fatal("eliminated runner should still be recognised")
	}
	stepN(f, m, 5)
	if p := player(t, m, "p1"); p.X != dead.X || p.Y != dead.Y || p.Alive || p.Dir != protocol.DirNone {
		t.Fatalf("dead runner moved: %+v", p)
	}
	// No duplicate caught event for the same runner.
	if n := len(f.rec.frames(protocol.TypeEvent)); n != 1 {
		t.Fatalf("expected exactly one caught event, got %d", n)
	}
	if m.Ended() {
		t.Fatal("two survivors: match must continue")
	}
}

func TestSecondCatchLeavesLastRunnerStanding(t *testing.T) {
	// Catcher sweeps up column 0: p1 at (0,0), p3 at (0,2) below... use a 1x5
	// corridor so a fixed "up" mover crosses both runners in order.
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(1, 5), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 2.5}, {X: 0.5, Y: 0.5}, {X: 0.5, Y: 4.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 3.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirUp} })
	m, _, err := f.svc.CreateMatch(context.Background(), threeRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	// Distance to p1 is 1.0 -> tick 3; then to p2 another 2.0 -> tick 13.
	first := stepN(f, m, 3)
	if first.Ended || player(t, m, "p1").Alive {
		t.Fatalf("p1 should be caught on tick 3 without ending: %+v", first)
	}
	var last StepResult
	for i := 0; i < 50 && !last.Ended; i++ {
		last = f.svc.Step(context.Background(), m)
	}
	if !last.Ended || last.End == nil || m.Tick() != 13 {
		t.Fatalf("expected end on tick 13, tick=%d ended=%v", m.Tick(), last.Ended)
	}
	end := *last.End
	if len(end.WinnerIDs) != 1 || end.WinnerIDs[0] != "p3" {
		t.Fatalf("winners %v", end.WinnerIDs)
	}
	if len(end.Standings) != 3 {
		t.Fatalf("standings %+v", end.Standings)
	}
	// Survivor first, then eliminations ordered by survival time descending.
	if end.Standings[0].PlayerID != "p3" || end.Standings[0].Place != 1 || end.Standings[0].SurvivedMS != 13*protocol.TickMillis {
		t.Fatalf("standing[0] %+v", end.Standings[0])
	}
	if end.Standings[1].PlayerID != "p2" || end.Standings[1].Place != 2 || end.Standings[1].SurvivedMS != 13*protocol.TickMillis {
		t.Fatalf("standing[1] %+v", end.Standings[1])
	}
	if end.Standings[2].PlayerID != "p1" || end.Standings[2].Place != 3 || end.Standings[2].SurvivedMS != 3*protocol.TickMillis {
		t.Fatalf("standing[2] %+v", end.Standings[2])
	}
	events := f.rec.frames(protocol.TypeEvent)
	if len(events) != 2 {
		t.Fatalf("expected two caught events, got %d", len(events))
	}
	if len(f.rec.results) != 1 || len(f.rec.results[0].Standings) != 3 || f.rec.results[0].WinnerIDs[0] != "p3" {
		t.Fatalf("stats %+v", f.rec.results)
	}
}

func TestSimultaneousCatchTiesShareStanding(t *testing.T) {
	// Two runners parked right next to a catcher spawn: both caught on tick 1.
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 1.3, Y: 1.5}, {X: 1.7, Y: 1.5}, {X: 0.5, Y: 0.5}},
		CatcherSpawns: []protocol.Point{{X: 1.5, Y: 1.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	m, _, err := f.svc.CreateMatch(context.Background(), threeRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	res := f.svc.Step(context.Background(), m)
	if !res.Ended || res.End == nil {
		t.Fatalf("both caught should leave one runner and end: %+v", res)
	}
	if len(res.Frames) != 4 { // snapshot, 2 events, match_end
		t.Fatalf("frames %d", len(res.Frames))
	}
	st := res.End.Standings
	if st[0].PlayerID != "p3" || st[0].Place != 1 || st[1].Place != 2 || st[2].Place != 2 {
		t.Fatalf("tied eliminations should share place 2: %+v", st)
	}
}

func TestRoundTimerExpiryWithThreeSurvivorsAllWin(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}, {X: 2.5, Y: 0.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 2.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	req := threeRunnerRequest()
	req.Config.RoundSeconds = 31
	m, _, err := f.svc.CreateMatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantTicks := 31 * 1000 / protocol.TickMillis
	var last StepResult
	ticks := 0
	for !last.Ended && ticks < wantTicks+10 {
		last = f.svc.Step(context.Background(), m)
		ticks++
		if ticks < wantTicks && last.Ended {
			t.Fatalf("ended early on tick %d", ticks)
		}
	}
	if ticks != wantTicks || last.End == nil {
		t.Fatalf("round of 31s should end on tick %d, got %d", wantTicks, ticks)
	}
	end := *last.End
	if len(end.WinnerIDs) != 3 {
		t.Fatalf("all survivors win: %v", end.WinnerIDs)
	}
	for _, s := range end.Standings {
		if s.Place != 1 || s.SurvivedMS != 31000 {
			t.Fatalf("standing %+v", s)
		}
	}
	var snap protocol.Snapshot
	_ = json.Unmarshal(last.Frames[0], &snap)
	if snap.TimeLeftMS != 0 || snap.AliveCount != 3 {
		t.Fatalf("final snapshot %+v", snap)
	}
	if len(f.rec.frames(protocol.TypeEvent)) != 0 {
		t.Fatal("no caught events expected")
	}
	if len(f.rec.results) != 1 || len(f.rec.results[0].WinnerIDs) != 3 {
		t.Fatalf("stats %+v", f.rec.results)
	}
}

func TestTimerExpiryAfterOneEliminationRanksSurvivorsFirst(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 1.5, Y: 1.5}, {X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 1.5, Y: 1.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	m, _, err := f.svc.CreateMatch(context.Background(), threeRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	var last StepResult
	for !last.Ended {
		last = f.svc.Step(context.Background(), m)
	}
	if m.Tick() != 600 {
		t.Fatalf("timer should end the match at tick 600, got %d", m.Tick())
	}
	end := *last.End
	if len(end.WinnerIDs) != 2 || end.WinnerIDs[0] != "p2" || end.WinnerIDs[1] != "p3" {
		t.Fatalf("winners %v", end.WinnerIDs)
	}
	if end.Standings[0].Place != 1 || end.Standings[1].Place != 1 || end.Standings[2].PlayerID != "p1" || end.Standings[2].Place != 3 || end.Standings[2].SurvivedMS != protocol.TickMillis {
		t.Fatalf("standings %+v", end.Standings)
	}
}

func TestCatcherSpeedScalesWithConfig(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(5, 1), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 4.5, Y: 0.5}, {X: 3.5, Y: 0.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 0.5}},
	}
	for _, tc := range []struct {
		speed float64
		want  float64
	}{{0.6, 0.5 + 0.12*5}, {1.0, 0.5 + 0.2*5}, {1.4, 0.5 + 0.28*5}} {
		f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirRight} })
		req := twoRunnerRequest()
		req.Config.CatcherSpeed = tc.speed
		m, _, err := f.svc.CreateMatch(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		stepN(f, m, 5)
		c := m.State().Snapshot.Catchers[0]
		if math.Abs(c.X-tc.want) > 1e-9 || c.Dir != protocol.DirRight {
			t.Fatalf("speed %.1f: catcher at %+v want x=%.2f", tc.speed, c, tc.want)
		}
	}
}

func TestCreateMatchConfigValidationBoundaries(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 2.5, Y: 2.5}},
		CatcherSpawns: []protocol.Point{{X: 2.5, Y: 0.5}},
	}
	bad := []func(c *protocol.RoomConfig){
		func(c *protocol.RoomConfig) { c.MazeSize = "" },
		func(c *protocol.RoomConfig) { c.MazeSize = "tiny" },
		func(c *protocol.RoomConfig) { c.Catchers = protocol.MinCatchers - 1 },
		func(c *protocol.RoomConfig) { c.Catchers = -1 },
		func(c *protocol.RoomConfig) { c.CatcherSpeed = protocol.MinCatcherSpeed - 0.01 },
		func(c *protocol.RoomConfig) { c.CatcherSpeed = protocol.MaxCatcherSpeed + 0.01 },
		func(c *protocol.RoomConfig) { c.RoundSeconds = protocol.MinRoundSeconds - 1 },
		func(c *protocol.RoomConfig) { c.RoundSeconds = protocol.MaxRoundSeconds + 1 },
		func(c *protocol.RoomConfig) { c.MinPlayers = protocol.MinMinPlayers - 1 },
		func(c *protocol.RoomConfig) { c.MinPlayers = protocol.MaxMinPlayers + 1 },
	}
	for i, mut := range bad {
		f := newFixture(t, gen, nil)
		req := twoRunnerRequest()
		mut(&req.Config)
		if _, _, err := f.svc.CreateMatch(context.Background(), req); err == nil {
			t.Fatalf("case %d: config %+v should be rejected", i, req.Config)
		}
		if len(f.rec.mazeReq) != 0 {
			t.Fatalf("case %d: invalid config must not call the maze service", i)
		}
	}
	good := []func(c *protocol.RoomConfig){
		func(c *protocol.RoomConfig) { c.MazeSize = protocol.MazeMedium },
		func(c *protocol.RoomConfig) { c.MazeSize = protocol.MazeLarge },
		func(c *protocol.RoomConfig) { c.Catchers = 5 },
		func(c *protocol.RoomConfig) { c.Catchers = 17 },
		func(c *protocol.RoomConfig) { c.Catchers = 32 },
		func(c *protocol.RoomConfig) { c.CatcherSpeed = protocol.MinCatcherSpeed },
		func(c *protocol.RoomConfig) { c.CatcherSpeed = protocol.MaxCatcherSpeed },
		func(c *protocol.RoomConfig) { c.RoundSeconds = protocol.MaxRoundSeconds },
		func(c *protocol.RoomConfig) { c.MinPlayers = protocol.MaxMinPlayers },
		func(c *protocol.RoomConfig) { c.Seed = nil },
	}
	for i, mut := range good {
		f := newFixture(t, gen, nil)
		req := twoRunnerRequest()
		mut(&req.Config)
		m, _, err := f.svc.CreateMatch(context.Background(), req)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if len(f.rec.mazeReq) != 1 {
			t.Fatalf("case %d: maze not requested", i)
		}
		mr := f.rec.mazeReq[0]
		dim, _ := protocol.MazeDimension(req.Config.MazeSize)
		if mr.W != dim || mr.H != dim || mr.Catchers != req.Config.Catchers {
			t.Fatalf("case %d: maze request %+v", i, mr)
		}
		if req.Config.Seed == nil && mr.Seed != 0 {
			t.Fatalf("nil seed should ask the maze service for a random one, sent %d", mr.Seed)
		}
		if got := len(m.State().Snapshot.Catchers); got != req.Config.Catchers {
			t.Fatalf("case %d: %d catchers spawned, want %d", i, got, req.Config.Catchers)
		}
	}
}

func TestCreateMatchRejectsMalformedMaze(t *testing.T) {
	cases := map[string]protocol.MazeGenerateResponse{
		"short walls": {Maze: protocol.Maze{W: 3, H: 3, Walls: make(protocol.Walls, 4)},
			RunnerSpawns: []protocol.Point{{X: 0.5, Y: 0.5}}, CatcherSpawns: []protocol.Point{{X: 2.5, Y: 2.5}}},
		"no runner spawns":  {Maze: openMaze(3, 3), CatcherSpawns: []protocol.Point{{X: 2.5, Y: 2.5}}},
		"no catcher spawns": {Maze: openMaze(3, 3), RunnerSpawns: []protocol.Point{{X: 0.5, Y: 0.5}}},
	}
	for name, gen := range cases {
		f := newFixture(t, gen, nil)
		if _, _, err := f.svc.CreateMatch(context.Background(), twoRunnerRequest()); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestSingleRunnerOnlyEndsOnCatchOrTimer(t *testing.T) {
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(3, 3), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}},
		CatcherSpawns: []protocol.Point{{X: 2.5, Y: 2.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	req := twoRunnerRequest()
	req.Players = req.Players[:1]
	m, _, err := f.svc.CreateMatch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res := stepN(f, m, 50); res.Ended {
		t.Fatal("a lone runner is not 'last runner standing' at tick 1")
	}
	var last StepResult
	for !last.Ended {
		last = f.svc.Step(context.Background(), m)
	}
	if m.Tick() != 600 || len(last.End.WinnerIDs) != 1 || last.End.WinnerIDs[0] != "p1" {
		t.Fatalf("tick=%d end=%+v", m.Tick(), last.End)
	}
}
