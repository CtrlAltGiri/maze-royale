package simulation

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"testing"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// gunMatch is a 15x15 open maze with two runners parked far from the center.
func gunMatch(catchers ...*catcher) *Match {
	return &Match{
		id:      "m-gun",
		grid:    gridmath.Grid{M: openMaze(15, 15)},
		center:  gridmath.Cell{X: 7, Y: 7}.Center(),
		roundMS: 300000,
		runners: []*runner{
			{id: "p1", name: "Ann", pos: gridmath.Vec{X: 0.5, Y: 0.5}, want: protocol.DirNone, alive: true},
			{id: "p2", name: "Bob", pos: gridmath.Vec{X: 14.5, Y: 14.5}, want: protocol.DirNone, alive: true},
		},
		catchers: catchers,
	}
}

func matchEvents(t *testing.T, res StepResult, kind string) []protocol.Event {
	t.Helper()
	var out []protocol.Event
	for _, f := range res.Frames {
		var ev protocol.Event
		if err := json.Unmarshal(f, &ev); err == nil && ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestGunAtCenterInMatchStartAndSnapshot(t *testing.T) {
	// The catcher spawn is the maze center itself: the gun must still be
	// placed there, bypassing the 2-cell clearance rule.
	_, m, resp := seededPowerUpFixture(t)

	var start *protocol.PowerUp
	for i, p := range resp.Starts["p1"].PowerUps {
		if p.Kind == protocol.PowerUpGun {
			start = &resp.Starts["p1"].PowerUps[i]
		}
	}
	if start == nil || start.X != 7.5 || start.Y != 7.5 {
		t.Fatalf("match_start missing gun at center: %+v", resp.Starts["p1"].PowerUps)
	}

	snap := m.State().Snapshot
	var live *protocol.PowerUp
	for i, p := range snap.PowerUps {
		if p.Kind == protocol.PowerUpGun {
			live = &snap.PowerUps[i]
		}
	}
	if live == nil || live.X != 7.5 || live.Y != 7.5 {
		t.Fatalf("snapshot missing gun at center: %+v", snap.PowerUps)
	}
	if st := m.State(); st.Start.You.Role != protocol.RoleSpectator {
		t.Fatalf("spectator state %+v", st.Start.You)
	}
}

func TestGunCollectionGrantsHasGunAndNeverRespawns(t *testing.T) {
	m := gunMatch()
	m.powerUpRNG = rand.New(rand.NewSource(7))
	m.powerUps = append(m.powerUps, pickup{
		PowerUp:   protocol.PowerUp{ID: "power5", Kind: protocol.PowerUpGun, X: 0.5, Y: 0.5},
		active:    true,
		noRespawn: true,
	})

	res := m.Step(1)
	events := powerUpEvents(t, res)
	if len(events) != 1 || events[0].PowerUpKind != protocol.PowerUpGun || events[0].PlayerID != "p1" || events[0].PowerUpID != "power5" {
		t.Fatalf("gun collection events %+v", events)
	}
	snap := lastSnapshot(t, res)
	if !snap.Players[0].HasGun || snap.Players[1].HasGun {
		t.Fatalf("has_gun %+v", snap.Players)
	}
	if m.powerUps[0].active {
		t.Fatal("gun pickup should be consumed")
	}
	for i := 0; i < 200; i++ {
		m.Step(int64(i + 1))
	}
	if m.powerUps[0].active {
		t.Fatal("gun respawned; it is one per match")
	}
	if got := m.State().Snapshot.PowerUps; len(got) != 0 {
		t.Fatalf("gun reappeared in snapshot: %v", got)
	}
	if !m.runners[0].hasGun {
		t.Fatal("runner should still hold the gun")
	}
}

func TestShootRequiresLiveRunnerWithGun(t *testing.T) {
	m := gunMatch()
	if m.Shoot("p1") {
		t.Fatal("shoot without the gun must be rejected")
	}
	m.runners[0].hasGun = true
	if !m.Shoot("p1") {
		t.Fatal("gun holder should be able to shoot")
	}
	m.runners[0].wantShot = false
	m.runners[0].alive = false
	if m.Shoot("p1") {
		t.Fatal("dead runner cannot shoot")
	}
	if m.Shoot("nobody") {
		t.Fatal("unknown player cannot shoot")
	}
}

func TestShootSpawnsBulletAndConsumesGun(t *testing.T) {
	m := gunMatch()
	r := m.runners[0]
	r.hasGun = true
	m.Input("p1", 1, protocol.DirRight)
	if !m.Shoot("p1") {
		t.Fatal("shoot rejected")
	}
	res := m.Step(1)
	fired := matchEvents(t, res, protocol.EventGunFired)
	if len(fired) != 1 || fired[0].PlayerID != "p1" || fired[0].Tick != 1 {
		t.Fatalf("gun_fired %+v", res.Frames)
	}
	snap := lastSnapshot(t, res)
	if snap.Players[0].HasGun {
		t.Fatal("firing must consume the gun")
	}
	if len(snap.Bullets) != 1 {
		t.Fatalf("want one bullet: %+v", snap.Bullets)
	}
	b := snap.Bullets[0]
	// Runner moved to 0.7; the bullet spawned there and travelled 0.6 this tick.
	if b.Dir != protocol.DirRight || math.Abs(b.X-1.3) > 1e-9 || b.Y != 0.5 {
		t.Fatalf("bullet %+v", b)
	}
}

func TestStandingRunnerShootsFacingDirection(t *testing.T) {
	m := gunMatch()
	r := m.runners[0]
	r.hasGun = true
	r.facing = protocol.DirDown // last travelled down; now standing still
	if !m.Shoot("p1") {
		t.Fatal("shoot rejected")
	}
	res := m.Step(1)
	snap := lastSnapshot(t, res)
	if len(snap.Bullets) != 1 || snap.Bullets[0].Dir != protocol.DirDown || snap.Bullets[0].Y != 1.1 {
		t.Fatalf("standing runner should fire along facing: %+v", snap.Bullets)
	}

	// No movement, no facing, no intent: the shot is a no-op and the gun is kept.
	m2 := gunMatch()
	m2.runners[0].hasGun = true
	if !m2.Shoot("p1") {
		t.Fatal("shoot rejected")
	}
	res = m2.Step(1)
	snap = lastSnapshot(t, res)
	if len(snap.Bullets) != 0 || !snap.Players[0].HasGun {
		t.Fatalf("aimless shot must keep the gun: %+v", snap)
	}
	if evs := matchEvents(t, res, protocol.EventGunFired); len(evs) != 0 {
		t.Fatalf("no gun_fired expected: %+v", evs)
	}
}

func TestBulletDespawnsAtWall(t *testing.T) {
	m := gunMatch()
	// Point-blank on the east lane: cell (14,0) is walled east, so the bullet
	// dies on the last open cell center.
	m.bullets = []*bullet{{id: "b1", pos: gridmath.Vec{X: 13.5, Y: 0.5}, dir: protocol.DirRight}}
	var res StepResult
	for i := 0; i < 5 && len(m.bullets) > 0; i++ {
		res = m.Step(int64(i + 1))
	}
	if len(m.bullets) != 0 {
		t.Fatalf("bullet should have died at the wall: %+v", m.bullets)
	}
	if got := lastSnapshot(t, res).Bullets; len(got) != 0 {
		t.Fatalf("snapshot still lists the bullet: %+v", got)
	}
}

func TestBulletDownsCatcherThenRespawnsAtCenter(t *testing.T) {
	m := gunMatch(&catcher{
		id: "c1", pos: gridmath.Vec{X: 2.5, Y: 0.5}, dir: protocol.DirNone,
		speed: 4.0, mover: &fixedMover{dir: protocol.DirRight},
	})
	m.runners[0].hasGun = true
	m.Input("p1", 1, protocol.DirRight)
	m.Shoot("p1")

	// The bullet outruns the moving catcher and hits it a few ticks in.
	var res StepResult
	var downTick int64
	for i := int64(1); i <= 30 && downTick == 0; i++ {
		res = m.Step(i)
		if evs := matchEvents(t, res, protocol.EventCatcherDown); len(evs) == 1 {
			if evs[0].CatcherID != "c1" || evs[0].MS != protocol.CatcherDownMS || evs[0].Tick != i {
				t.Fatalf("catcher_down %+v", evs[0])
			}
			downTick = i
		}
	}
	if downTick == 0 {
		t.Fatal("bullet never hit the catcher")
	}
	snap := lastSnapshot(t, res)
	if snap.Catchers[0].DownMS != int64(protocol.CatcherDownMS) {
		t.Fatalf("down_ms %d want %d", snap.Catchers[0].DownMS, protocol.CatcherDownMS)
	}
	if len(snap.Bullets) != 0 {
		t.Fatal("the bullet is consumed by the hit")
	}
	if m.catchers[0].downUntil != downTick+60 {
		t.Fatalf("downUntil %d want %d", m.catchers[0].downUntil, downTick+60)
	}

	// While down the catcher neither moves (its mover wants right) nor catches,
	// even with a runner standing on top of it.
	downPos := m.catchers[0].pos
	m.runners[1].pos = gridmath.Vec{X: downPos.X + 0.2, Y: downPos.Y}
	res = m.Step(downTick + 1)
	snap = lastSnapshot(t, res)
	if m.catchers[0].pos != downPos {
		t.Fatalf("downed catcher moved to %v", m.catchers[0].pos)
	}
	if snap.Catchers[0].DownMS != int64(protocol.CatcherDownMS-protocol.TickMillis) {
		t.Fatalf("down_ms %d want %d", snap.Catchers[0].DownMS, protocol.CatcherDownMS-protocol.TickMillis)
	}
	if evs := matchEvents(t, res, protocol.EventCaught); len(evs) != 0 {
		t.Fatalf("downed catcher must not catch: %+v", evs)
	}
	if !snap.Players[1].Alive {
		t.Fatal("runner on top of a downed catcher must survive")
	}

	// Down for exactly 60 ticks: still down at downTick+59, respawned at +60.
	m.runners[1].pos = gridmath.Vec{X: 14.5, Y: 14.5}
	for i := downTick + 2; i < downTick+60; i++ {
		m.Step(i)
	}
	if m.catchers[0].downUntil == 0 || m.catchers[0].pos != downPos {
		t.Fatal("catcher respawned early")
	}
	res = m.Step(downTick + 60)
	respawns := matchEvents(t, res, protocol.EventCatcherRespawn)
	if len(respawns) != 1 || respawns[0].CatcherID != "c1" || respawns[0].Tick != downTick+60 {
		t.Fatalf("catcher_respawn %+v", res.Frames)
	}
	snap = lastSnapshot(t, res)
	if snap.Catchers[0].DownMS != 0 {
		t.Fatalf("down_ms %d after respawn", snap.Catchers[0].DownMS)
	}
	// Respawned at the maze center and allowed to act on the same tick (its
	// mover pushes it right 0.2 cells).
	if math.Abs(snap.Catchers[0].X-7.7) > 1e-9 || snap.Catchers[0].Y != 7.5 || snap.Catchers[0].Dir != protocol.DirRight {
		t.Fatalf("respawned catcher %+v want ~(7.7,7.5) moving right", snap.Catchers[0])
	}
}

type othersRecorder struct {
	last []gridmath.Vec
}

func (r *othersRecorder) Next(_ gridmath.Grid, _ gridmath.Cell, _ protocol.Direction, _, others []gridmath.Vec) protocol.Direction {
	r.last = append([]gridmath.Vec(nil), others...)
	return protocol.DirNone
}

func TestDownedCatcherStillCountsAsAnObstacle(t *testing.T) {
	rec := &othersRecorder{}
	m := gunMatch(
		&catcher{id: "c1", pos: gridmath.Vec{X: 2.5, Y: 0.5}, dir: protocol.DirNone, speed: 4.0, mover: &fixedMover{dir: protocol.DirNone}, downUntil: 100},
		&catcher{id: "c2", pos: gridmath.Vec{X: 5.5, Y: 5.5}, dir: protocol.DirNone, speed: 4.0, mover: rec},
	)
	m.tick = 50
	m.Step(51)
	if len(rec.last) != 1 || rec.last[0].X != 2.5 || rec.last[0].Y != 0.5 {
		t.Fatalf("catcher brains must still route around the downed body: %v", rec.last)
	}
}

func TestShotFiredSameTickAsCatchStillFlies(t *testing.T) {
	// p1 fires while the catcher standing off the bullet's lane catches it.
	// The bullet is already spawned this tick and keeps flying afterwards.
	m := gunMatch(&catcher{
		id: "c1", pos: gridmath.Vec{X: 0.5, Y: 0.9}, dir: protocol.DirNone,
		speed: 4.0, mover: &fixedMover{dir: protocol.DirNone},
	})
	m.runners = append(m.runners, &runner{id: "p3", name: "Cid", pos: gridmath.Vec{X: 14.5, Y: 0.5}, want: protocol.DirNone, alive: true})
	m.runners[0].hasGun = true
	m.Input("p1", 1, protocol.DirRight)
	m.Shoot("p1")

	res := m.Step(1)
	if res.Ended {
		t.Fatal("two survivors left: match must continue")
	}
	snap := lastSnapshot(t, res)
	if snap.Players[0].Alive {
		t.Fatal("runner should have been caught this tick")
	}
	if evs := matchEvents(t, res, protocol.EventCaught); len(evs) != 1 || evs[0].PlayerID != "p1" {
		t.Fatalf("caught %+v", res.Frames)
	}
	if evs := matchEvents(t, res, protocol.EventGunFired); len(evs) != 1 {
		t.Fatalf("gun_fired %+v", res.Frames)
	}
	if len(snap.Bullets) != 1 || snap.Bullets[0].Dir != protocol.DirRight {
		t.Fatalf("bullet already spawned, still flies: %+v", snap.Bullets)
	}
	prevX := snap.Bullets[0].X
	res = m.Step(2)
	snap = lastSnapshot(t, res)
	if len(snap.Bullets) != 1 || snap.Bullets[0].X <= prevX {
		t.Fatalf("bullet should keep flying after the shooter died: %+v", snap.Bullets)
	}
}

func TestShootEndpointRoutesToMatch(t *testing.T) {
	f, m, resp := seededPowerUpFixture(t)
	m.runners[0].hasGun = true
	h := f.svc.Handler()

	path := "/matches/" + resp.MatchID + "/shoot"
	if w := postJSON(t, h, path, protocol.SimShootRequest{PlayerID: "p1"}, nil); w.Code != http.StatusOK {
		t.Fatalf("shoot: %d %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, h, path, protocol.SimShootRequest{PlayerID: "p2"}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("no gun -> %d", w.Code)
	}
	if w := postJSON(t, h, "/matches/nope/shoot", protocol.SimShootRequest{PlayerID: "p1"}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing match -> %d", w.Code)
	}

	m.Input("p1", 1, protocol.DirRight)
	res := f.svc.Step(context.Background(), m)
	if evs := matchEvents(t, res, protocol.EventGunFired); len(evs) != 1 || evs[0].PlayerID != "p1" {
		t.Fatalf("endpoint shoot should resolve on the next tick: %+v", res.Frames)
	}
}
