package gridmath

import (
	"math"
	"testing"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// corridor builds a 3x1 maze with a 1 cell corridor open east/west, walled everywhere else.
func corridor() Grid {
	N, E, S, W := protocol.WallNorth, protocol.WallEast, protocol.WallSouth, protocol.WallWest
	return Grid{M: protocol.Maze{W: 3, H: 1, Walls: protocol.Walls{
		N | S | W, // cell 0: open east
		N | S,     // cell 1: open both
		N | S | E, // cell 2: open west
	}}}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestDeltaAndOpposite(t *testing.T) {
	for _, d := range Cardinals {
		dx, dy := Delta(d)
		ox, oy := Delta(Opposite(d))
		if dx != -ox || dy != -oy {
			t.Errorf("%s opposite mismatch", d)
		}
		if abs(dx)+abs(dy) != 1 {
			t.Errorf("%s delta not unit", d)
		}
	}
	if dx, dy := Delta(protocol.DirNone); dx != 0 || dy != 0 {
		t.Error("none should be zero")
	}
}

func TestWallLookups(t *testing.T) {
	g := corridor()
	if !g.CanMove(Cell{0, 0}, protocol.DirRight) {
		t.Error("cell 0 should open east")
	}
	if g.CanMove(Cell{0, 0}, protocol.DirLeft) {
		t.Error("cell 0 should be closed west")
	}
	if g.CanMove(Cell{0, 0}, protocol.DirUp) {
		t.Error("cell 0 should be closed north")
	}
	if g.Walls(Cell{-1, 0}) != 15 {
		t.Error("out of bounds must be fully walled")
	}
	if got := g.OpenDirs(Cell{1, 0}); len(got) != 2 {
		t.Errorf("cell 1 open dirs = %v", got)
	}
}

func TestStepMovesAlongCorridor(t *testing.T) {
	g := corridor()
	p, d := g.Step(Vec{0.5, 0.5}, protocol.DirRight, 4.0, 0.25) // 1 cell
	if !near(p.X, 1.5) || !near(p.Y, 0.5) || d != protocol.DirRight {
		t.Fatalf("got %+v %s", p, d)
	}
}

func TestStepStopsAtWall(t *testing.T) {
	g := corridor()
	// From cell 2 center, moving right is blocked: stay at center, dir none.
	p, d := g.Step(Vec{2.5, 0.5}, protocol.DirRight, 4.0, 0.5)
	if !near(p.X, 2.5) || d != protocol.DirNone {
		t.Fatalf("expected blocked at center, got %+v %s", p, d)
	}
	// From mid cell 2, moving right: reach center then stop.
	p, d = g.Step(Vec{2.2, 0.5}, protocol.DirRight, 4.0, 0.5)
	if !near(p.X, 2.5) || d != protocol.DirNone {
		t.Fatalf("expected stop at center, got %+v %s", p, d)
	}
	// Long move across the whole corridor clamps at the far center.
	p, _ = g.Step(Vec{0.5, 0.5}, protocol.DirRight, 4.0, 10)
	if !near(p.X, 2.5) {
		t.Fatalf("expected clamp at 2.5, got %+v", p)
	}
}

func TestStepSnapsToLane(t *testing.T) {
	g := corridor()
	p, _ := g.Step(Vec{0.5, 0.6}, protocol.DirRight, 4.0, 0.1)
	if !near(p.Y, 0.5) {
		t.Fatalf("expected snap to lane center, got %+v", p)
	}
}

func TestStepNoneIsNoop(t *testing.T) {
	g := corridor()
	p, d := g.Step(Vec{1.5, 0.5}, protocol.DirNone, 4.0, 1)
	if !near(p.X, 1.5) || d != protocol.DirNone {
		t.Fatal("none should not move")
	}
}

func TestCaught(t *testing.T) {
	if !Caught(Vec{1, 1}, Vec{1.4, 1}) {
		t.Error("0.4 should be a catch")
	}
	if Caught(Vec{1, 1}, Vec{1.5, 1}) {
		t.Error("0.5 should not be a catch")
	}
}

func TestCellHelpers(t *testing.T) {
	if CellOf(Vec{2.9, 0.1}) != (Cell{2, 0}) {
		t.Error("CellOf wrong")
	}
	if c := (Cell{3, 4}).Center(); !near(c.X, 3.5) || !near(c.Y, 4.5) {
		t.Error("Center wrong")
	}
	if Manhattan(Cell{0, 0}, Cell{3, 4}) != 7 {
		t.Error("Manhattan wrong")
	}
	if !AtCenter(Vec{1.5, 1.5}, 1e-9) || AtCenter(Vec{1.6, 1.5}, 0.01) {
		t.Error("AtCenter wrong")
	}
	if l := Lerp(Vec{0, 0}, Vec{2, 4}, 0.5); !near(l.X, 1) || !near(l.Y, 2) {
		t.Error("Lerp wrong")
	}
}
