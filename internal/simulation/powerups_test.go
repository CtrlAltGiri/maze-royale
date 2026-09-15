package simulation

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

func seededPowerUpFixture(t *testing.T) (*fixture, *Match, protocol.SimCreateMatchResponse) {
	t.Helper()
	gen := protocol.MazeGenerateResponse{
		Maze: openMaze(15, 15), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 14.5, Y: 14.5}},
		CatcherSpawns: []protocol.Point{{X: 7.5, Y: 7.5}},
	}
	f := newFixture(t, gen, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	m, resp, err := f.svc.CreateMatch(context.Background(), twoRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	return f, m, resp
}

func legalPowerUpSpots(t *testing.T, m *Match, p protocol.PowerUp) {
	t.Helper()
	cell := gridmath.CellOf(gridmath.Vec{X: p.X, Y: p.Y})
	g := m.grid
	if !g.InBounds(cell) {
		t.Fatalf("powerup %s out of bounds at %v", p.ID, cell)
	}
	if center := cell.Center(); center.X != p.X || center.Y != p.Y {
		t.Fatalf("powerup %s not on a cell center: %v", p.ID, p)
	}
	if len(g.OpenDirs(cell)) == 0 {
		t.Fatalf("powerup %s in a walled cell %v", p.ID, cell)
	}
	pos := gridmath.Vec{X: p.X, Y: p.Y}
	for _, r := range m.runners {
		if r.alive && gridmath.Dist(pos, r.pos) < 2 {
			t.Fatalf("powerup %s too close to runner %s: %v vs %v", p.ID, r.id, pos, r.pos)
		}
	}
	for _, c := range m.catchers {
		if gridmath.Dist(pos, c.pos) < 2 {
			t.Fatalf("powerup %s too close to catcher %s: %v vs %v", p.ID, c.id, pos, c.pos)
		}
	}
}

func TestSeededPowerUpSpawnsDeterministicAndLegal(t *testing.T) {
	_, m1, resp1 := seededPowerUpFixture(t)
	_, m2, resp2 := seededPowerUpFixture(t)

	snap1 := m1.State().Snapshot
	snap2 := m2.State().Snapshot
	if len(snap1.PowerUps) != 4 {
		t.Fatalf("want 4 powerups, got %v", snap1.PowerUps)
	}
	if !slices.Equal(snap1.PowerUps, snap2.PowerUps) {
		t.Fatalf("same seed must place identical powerups:\n%v\n%v", snap1.PowerUps, snap2.PowerUps)
	}
	ids := map[string]bool{}
	kinds := map[string]int{}
	for i, p := range snap1.PowerUps {
		if ids[p.ID] {
			t.Fatalf("duplicate powerup id %s", p.ID)
		}
		ids[p.ID] = true
		kinds[p.Kind]++
		legalPowerUpSpots(t, m1, p)
		for j, o := range snap1.PowerUps {
			if i != j && o.X == p.X && o.Y == p.Y {
				t.Fatalf("powerups %s and %s overlap at %v", p.ID, o.ID, p)
			}
		}
	}
	if kinds[protocol.PowerUpSpeed] != 2 || kinds[protocol.PowerUpInvisibility] != 2 {
		t.Fatalf("want two of each kind, got %v", kinds)
	}
	if !slices.Equal(resp1.Starts["p1"].PowerUps, snap1.PowerUps) {
		t.Fatalf("match_start powerups %v differ from snapshot %v", resp1.Starts["p1"].PowerUps, snap1.PowerUps)
	}
	if !slices.Equal(resp2.Starts["p1"].PowerUps, resp1.Starts["p1"].PowerUps) {
		t.Fatal("match_start powerups not deterministic across identical seeds")
	}
}

func controlledPowerUpMatch() *Match {
	return &Match{
		id:      "m-test",
		grid:    gridmath.Grid{M: openMaze(20, 3)},
		roundMS: 30000,
		runners: []*runner{
			{id: "p1", name: "Ann", pos: gridmath.Vec{X: 0.5, Y: 0.5}, want: protocol.DirNone, alive: true},
		},
	}
}

func placePowerUp(m *Match, id, kind string, x, y float64) {
	m.powerUps = append(m.powerUps, pickup{
		PowerUp: protocol.PowerUp{ID: id, Kind: kind, X: x, Y: y},
		active:  true,
	})
}

func lastSnapshot(t *testing.T, res StepResult) protocol.Snapshot {
	t.Helper()
	var snap protocol.Snapshot
	if err := json.Unmarshal(res.Frames[0], &snap); err != nil {
		t.Fatalf("snapshot decode: %v", err)
	}
	return snap
}

func powerUpEvents(t *testing.T, res StepResult) []protocol.Event {
	t.Helper()
	var out []protocol.Event
	for _, f := range res.Frames {
		var ev protocol.Event
		if err := json.Unmarshal(f, &ev); err == nil && ev.Kind == protocol.EventPowerUpCollected {
			out = append(out, ev)
		}
	}
	return out
}

func TestSpeedPickupBoostsAndExpires(t *testing.T) {
	m := controlledPowerUpMatch()
	placePowerUp(m, "power1", protocol.PowerUpSpeed, 0.5, 0.5)

	res := m.Step(1)
	if res.Ended {
		t.Fatal("single runner must not end the match early")
	}
	events := powerUpEvents(t, res)
	if len(events) != 1 {
		t.Fatalf("want one collection event, got %v", res.Frames)
	}
	ev := events[0]
	if ev.PlayerID != "p1" || ev.PowerUpID != "power1" || ev.PowerUpKind != protocol.PowerUpSpeed || ev.MS != protocol.PowerUpSpeedMS || ev.Tick != 1 {
		t.Fatalf("collection event %+v", ev)
	}
	var env protocol.Envelope
	_ = json.Unmarshal(res.Frames[0], &env)
	if env.T != protocol.TypeSnapshot {
		t.Fatalf("first frame should be snapshot, got %s", res.Frames[0])
	}
	snap := lastSnapshot(t, res)
	if len(snap.PowerUps) != 0 {
		t.Fatalf("collected pickup still listed: %v", snap.PowerUps)
	}
	if snap.Players[0].SpeedBoostMS != 5000 {
		t.Fatalf("speed_boost_ms %d want 5000", snap.Players[0].SpeedBoostMS)
	}

	m.Input("p1", 1, protocol.DirRight)
	res = m.Step(1_000_000_000)
	snap = lastSnapshot(t, res)
	if math.Abs(snap.Players[0].X-0.8) > 1e-9 {
		t.Fatalf("boosted displacement %.4f want 0.3", snap.Players[0].X-0.5)
	}
	if snap.Players[0].SpeedBoostMS != 4950 {
		t.Fatalf("speed_boost_ms %d want 4950", snap.Players[0].SpeedBoostMS)
	}

	m.tick = m.runners[0].speedUntil - 1
	res = m.Step(2)
	snap = lastSnapshot(t, res)
	if math.Abs(snap.Players[0].X-1.0) > 1e-9 {
		t.Fatalf("expired displacement %.4f want 0.2", snap.Players[0].X-0.8)
	}
	if snap.Players[0].SpeedBoostMS != 0 {
		t.Fatalf("speed_boost_ms %d want 0", snap.Players[0].SpeedBoostMS)
	}
}

func TestSpeedPickupDoesNotPassWalls(t *testing.T) {
	m := &Match{
		id:      "m-wall",
		grid:    gridmath.Grid{M: wallMaze()},
		roundMS: 30000,
		runners: []*runner{
			{id: "p1", name: "Ann", pos: gridmath.Vec{X: 0.5, Y: 0.5}, want: protocol.DirRight, alive: true},
		},
	}
	placePowerUp(m, "power1", protocol.PowerUpSpeed, 0.5, 0.5)
	for i := 0; i < 20; i++ {
		m.Step(int64(i))
	}
	p := m.State().Snapshot.Players[0]
	if p.X != 1.5 || p.Y != 0.5 {
		t.Fatalf("boosted runner should still stop at the east wall of (1,0): %+v", p)
	}
}

func TestCollectionRadiusAndNoSkipping(t *testing.T) {
	m := controlledPowerUpMatch()
	m.runners[0].pos = gridmath.Vec{X: 0.14, Y: 0.5}
	placePowerUp(m, "power1", protocol.PowerUpSpeed, 0.5, 0.5)
	res := m.Step(1)
	if events := powerUpEvents(t, res); len(events) != 0 {
		t.Fatalf("0.36 away must be outside the radius: %+v", events)
	}
	if !m.powerUps[0].active {
		t.Fatal("pickup should remain active")
	}
	m.runners[0].pos = gridmath.Vec{X: 0.16, Y: 0.5}
	res = m.Step(1)
	if events := powerUpEvents(t, res); len(events) != 1 {
		t.Fatalf("0.34 away must collect: %+v", events)
	}

	m2 := controlledPowerUpMatch()
	m2.runners[0].want = protocol.DirRight
	m2.runners[0].speedUntil = 100
	placePowerUp(m2, "power1", protocol.PowerUpSpeed, 1.5, 0.5)
	want := []float64{0.8, 1.1, 1.4}
	for i, x := range want {
		res = m2.Step(int64(i))
		if events := powerUpEvents(t, res); len(events) != 0 && i < 2 {
			t.Fatalf("tick %d: pickup collected early from %v", i+1, events)
		}
		snap := lastSnapshot(t, res)
		if math.Abs(snap.Players[0].X-x) > 1e-9 {
			t.Fatalf("tick %d: runner at %.4f want %.2f", i+1, snap.Players[0].X, x)
		}
		if i < 2 && !m2.powerUps[0].active {
			t.Fatalf("tick %d: pickup gone before the runner reached it", i+1)
		}
	}
	if events := powerUpEvents(t, res); len(events) != 1 || events[0].PowerUpID != "power1" {
		t.Fatalf("boosted runner must collect on arrival tick: %+v", events)
	}
	if m2.powerUps[0].active {
		t.Fatal("pickup should be collected")
	}

	m3 := &Match{
		id:      "m-wall2",
		grid:    gridmath.Grid{M: wallMaze()},
		roundMS: 30000,
		runners: []*runner{
			{id: "p1", name: "Ann", pos: gridmath.Vec{X: 1.5, Y: 0.5}, want: protocol.DirRight, alive: true},
		},
	}
	placePowerUp(m3, "power1", protocol.PowerUpSpeed, 2.5, 0.5)
	for i := 0; i < 10; i++ {
		res = m3.Step(int64(i))
	}
	if events := powerUpEvents(t, res); len(events) != 0 {
		t.Fatalf("pickup behind a wall must not be collected: %+v", events)
	}
	if !m3.powerUps[0].active {
		t.Fatal("pickup behind a wall should still be active")
	}
}

type targetRecorder struct {
	last []gridmath.Vec
}

func (r *targetRecorder) Next(_ gridmath.Grid, _ gridmath.Cell, _ protocol.Direction, runners, _ []gridmath.Vec) protocol.Direction {
	r.last = append([]gridmath.Vec(nil), runners...)
	return protocol.DirNone
}

func TestInvisibilitySurvivesOverlapAndExpires(t *testing.T) {
	maze := openMaze(20, 3)
	gen := protocol.MazeGenerateResponse{
		Maze: maze, Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 10.5, Y: 0.5}},
		CatcherSpawns: []protocol.Point{{X: 0.5, Y: 0.5}},
	}
	rec := &targetRecorder{}
	f := newFixture(t, gen, func(int) Mover { return rec })
	m, _, err := f.svc.CreateMatch(context.Background(), twoRunnerRequest())
	if err != nil {
		t.Fatal(err)
	}
	m.powerUps = nil
	placePowerUp(m, "power1", protocol.PowerUpInvisibility, 0.5, 0.5)

	res := f.svc.Step(context.Background(), m)
	if res.Ended {
		t.Fatal("invisible runner must survive the overlap tick")
	}
	events := powerUpEvents(t, res)
	if len(events) != 1 || events[0].PowerUpKind != protocol.PowerUpInvisibility || events[0].MS != protocol.PowerUpInvisibilityMS {
		t.Fatalf("events %+v", events)
	}
	snap := lastSnapshot(t, res)
	if snap.Players[0].InvisibleMS != 3000 || !snap.Players[0].Alive {
		t.Fatalf("invisible player %+v", snap.Players[0])
	}
	if len(rec.last) != 1 || rec.last[0].X != 10.5 {
		t.Fatalf("catcher brain should only see the visible runner, got %v", rec.last)
	}

	m.tick = m.runners[0].invisibleUntil - 1
	res = f.svc.Step(context.Background(), m)
	if !res.Ended || res.End == nil {
		t.Fatalf("single visible runner on top of a catcher should be caught: %+v", res)
	}
	var caught []protocol.Event
	for _, f := range res.Frames {
		var ev protocol.Event
		if json.Unmarshal(f, &ev) == nil && ev.Kind == protocol.EventCaught {
			caught = append(caught, ev)
		}
	}
	if len(caught) != 1 || caught[0].PlayerID != "p1" {
		t.Fatalf("expected one caught event for p1, got %v", res.Frames)
	}
	snap = lastSnapshot(t, res)
	if snap.Players[0].SpeedBoostMS != 0 || snap.Players[0].InvisibleMS != 0 || snap.Players[0].Alive {
		t.Fatalf("dead runner should carry no effects: %+v", snap.Players[0])
	}
	if m.runners[0].speedUntil != 0 || m.runners[0].invisibleUntil != 0 {
		t.Fatal("effect deadlines must reset on death")
	}
}

func TestPickupCollectedOnceByFirstRunner(t *testing.T) {
	m := controlledPowerUpMatch()
	m.runners = append(m.runners, &runner{id: "p2", name: "Bob", pos: gridmath.Vec{X: 0.5, Y: 0.5}, want: protocol.DirNone, alive: true})
	placePowerUp(m, "power1", protocol.PowerUpSpeed, 0.5, 0.5)

	res := m.Step(1)
	events := powerUpEvents(t, res)
	if len(events) != 1 || events[0].PlayerID != "p1" {
		t.Fatalf("only the first runner collects: %+v", events)
	}
	snap := lastSnapshot(t, res)
	if snap.Players[0].SpeedBoostMS != 5000 || snap.Players[1].SpeedBoostMS != 0 {
		t.Fatalf("effects %+v", snap.Players)
	}

	m2 := controlledPowerUpMatch()
	m2.runners = append(m2.runners, &runner{id: "p2", name: "Bob", pos: gridmath.Vec{X: 0.5, Y: 0.5}, want: protocol.DirNone, alive: true})
	m2.runners[0].alive = false
	placePowerUp(m2, "power1", protocol.PowerUpSpeed, 0.5, 0.5)
	res = m2.Step(1)
	events = powerUpEvents(t, res)
	if len(events) != 1 || events[0].PlayerID != "p2" {
		t.Fatalf("dead runner must not collect, p2 should: %+v", events)
	}
}

func TestSameKindRefreshesAndKindsCoexist(t *testing.T) {
	m := controlledPowerUpMatch()
	placePowerUp(m, "power1", protocol.PowerUpSpeed, 0.5, 0.5)
	placePowerUp(m, "power2", protocol.PowerUpSpeed, 1.5, 0.5)
	placePowerUp(m, "power3", protocol.PowerUpInvisibility, 2.5, 0.5)

	res := m.Step(1)
	snap := lastSnapshot(t, res)
	if snap.Players[0].SpeedBoostMS != 5000 {
		t.Fatalf("first collect %+v", snap.Players[0])
	}
	m.tick = m.runners[0].speedUntil - 40
	m.runners[0].pos = gridmath.Vec{X: 1.5, Y: 0.5}
	res = m.Step(1)
	snap = lastSnapshot(t, res)
	if snap.Players[0].SpeedBoostMS != 5000 {
		t.Fatalf("same-kind recollect should reset to 5000, got %d", snap.Players[0].SpeedBoostMS)
	}
	m.runners[0].pos = gridmath.Vec{X: 2.5, Y: 0.5}
	res = m.Step(1)
	snap = lastSnapshot(t, res)
	if snap.Players[0].InvisibleMS != 3000 || snap.Players[0].SpeedBoostMS != 4950 {
		t.Fatalf("both effects should coexist: %+v", snap.Players[0])
	}
}

func TestPowerUpRespawnsAfterCooldown(t *testing.T) {
	m := controlledPowerUpMatch()
	m.grid = gridmath.Grid{M: openMaze(20, 20)}
	m.runners[0].pos = gridmath.Vec{X: 0.5, Y: 0.5}
	m.powerUpRNG = rand.New(rand.NewSource(7))
	placePowerUp(m, "power1", protocol.PowerUpSpeed, 0.5, 0.5)

	res := m.Step(1)
	if len(powerUpEvents(t, res)) != 1 {
		t.Fatal("setup collect failed")
	}
	if m.powerUps[0].active || m.powerUps[0].respawnTick != 161 {
		t.Fatalf("respawnTick %d want 161", m.powerUps[0].respawnTick)
	}
	m.tick = 159
	res = m.Step(1)
	if m.powerUps[0].active {
		t.Fatal("pickup respawned before its cooldown")
	}
	if got := lastSnapshot(t, res).PowerUps; len(got) != 0 {
		t.Fatalf("snapshot lists inactive pickup: %v", got)
	}
	m.Step(1)
	p := m.powerUps[0]
	if !p.active {
		t.Fatal("pickup did not respawn at cooldown")
	}
	legalPowerUpSpots(t, m, p.PowerUp)
	if p.X == 0.5 && p.Y == 0.5 {
		t.Fatal("pickup respawned on the runner's cell")
	}

	m2 := controlledPowerUpMatch()
	m2.powerUpRNG = rand.New(rand.NewSource(7))
	m2.powerUps = []pickup{{PowerUp: protocol.PowerUp{ID: "power1", Kind: protocol.PowerUpSpeed}, respawnTick: 1}}
	m2.ended = true
	res = m2.Step(1)
	if !res.Ended || len(res.Frames) != 0 || m2.Tick() != 0 || m2.powerUps[0].active || m2.powerUps[0].respawnTick != 1 {
		t.Fatalf("ended match changed state: result=%+v pickup=%+v tick=%d", res, m2.powerUps[0], m2.Tick())
	}
}

func TestConstrainedMazeNoLegalSpawn(t *testing.T) {
	m := &Match{
		id:      "m-tiny",
		grid:    gridmath.Grid{M: openMaze(1, 1)},
		roundMS: 30000,
		runners: []*runner{
			{id: "p1", name: "Ann", pos: gridmath.Vec{X: 0.5, Y: 0.5}, want: protocol.DirNone, alive: true},
		},
		powerUpRNG: rand.New(rand.NewSource(7)),
	}
	m.initPowerUps()
	if got := m.activePowerUps(); len(got) != 0 {
		t.Fatalf("1x1 maze must yield no powerups, got %v", got)
	}
	for i := 0; i < 200; i++ {
		res := m.Step(int64(i))
		if res.Ended {
			break
		}
	}
	if got := m.State().Snapshot.PowerUps; len(got) != 0 {
		t.Fatalf("snapshot should encode no pickups, got %v", got)
	}
}

func TestPowerUpsInServiceStateAndPushes(t *testing.T) {
	_, m, resp := seededPowerUpFixture(t)
	if len(resp.Starts["p1"].PowerUps) == 0 {
		t.Fatal("match_start carries no power_ups")
	}
	st := m.State()
	if !slices.Equal(st.Start.PowerUps, st.Snapshot.PowerUps) {
		t.Fatalf("state start %v vs snapshot %v", st.Start.PowerUps, st.Snapshot.PowerUps)
	}

	f := newFixture(t, protocol.MazeGenerateResponse{
		Maze: openMaze(15, 15), Seed: 7,
		RunnerSpawns:  []protocol.Point{{X: 0.5, Y: 0.5}, {X: 14.5, Y: 14.5}},
		CatcherSpawns: []protocol.Point{{X: 7.5, Y: 7.5}},
	}, func(int) Mover { return &fixedMover{dir: protocol.DirNone} })
	h := f.svc.Handler()
	var create protocol.SimCreateMatchResponse
	postJSON(t, h, "/matches", twoRunnerRequest(), &create)
	if len(create.Starts["p1"].PowerUps) != 4 {
		t.Fatalf("create response power_ups %v", create.Starts["p1"].PowerUps)
	}

	m2, _ := f.svc.Match(create.MatchID)
	m2.powerUps = nil
	placePowerUp(m2, "power9", protocol.PowerUpSpeed, 0.5, 0.5)
	res := f.svc.Step(context.Background(), m2)
	if len(powerUpEvents(t, res)) != 1 {
		t.Fatalf("service step should emit collection, frames %v", res.Frames)
	}
	pushed := f.rec.frames(protocol.TypeEvent)
	if len(pushed) != 1 {
		t.Fatalf("collection event not pushed to gateway: %v", f.rec.pushes)
	}
	var ev protocol.Event
	_ = json.Unmarshal(pushed[0], &ev)
	if ev.Kind != protocol.EventPowerUpCollected || ev.PowerUpID != "power9" {
		t.Fatalf("pushed event %+v", ev)
	}

	placePowerUp(m2, "power10", protocol.PowerUpInvisibility, 4.5, 4.5)
	f.svc.Step(context.Background(), m2)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/matches/"+create.MatchID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET state: %d %s", w.Code, w.Body.String())
	}
	var current protocol.SimMatchStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.Start.You.Role != protocol.RoleSpectator || len(current.Snapshot.PowerUps) != 1 || current.Snapshot.PowerUps[0].ID != "power10" || !slices.Equal(current.Start.PowerUps, current.Snapshot.PowerUps) || current.Snapshot.Players[0].SpeedBoostMS != 4950 {
		t.Fatalf("late spectator state lost current pickups/effect: %+v", current)
	}
}
