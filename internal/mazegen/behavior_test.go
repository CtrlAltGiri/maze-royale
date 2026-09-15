package mazegen

import (
	"testing"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// allSizes are the three contract maze sizes resolved through protocol so the
// mazegen tests break if the mapping ever drifts.
func allSizes(t *testing.T) []int {
	t.Helper()
	var out []int
	for _, name := range []string{protocol.MazeSmall, protocol.MazeMedium, protocol.MazeLarge} {
		n, ok := protocol.MazeDimension(name)
		if !ok {
			t.Fatalf("unknown maze size %q", name)
		}
		out = append(out, n)
	}
	return out
}

func cellOf(p protocol.Point) gridmath.Cell { return gridmath.CellOf(gridmath.Vec{X: p.X, Y: p.Y}) }

// bfs returns the shortest path length between two cells or -1.
func bfs(m protocol.Maze, from, to gridmath.Cell) int {
	return distances(m, from)[to.Y*m.W+to.X]
}

func TestEveryCellReachableFromEverySpawnAcrossSeedsAndSizes(t *testing.T) {
	for _, n := range allSizes(t) {
		for seed := int64(100); seed < 140; seed++ {
			res := gen(t, n, seed, 8, 4)
			m := res.Maze
			// Connectivity from (0,0) is checked by CheckConnected; also
			// verify from a spawn so spawns are not on an isolated island.
			for _, p := range append(res.RunnerSpawns, res.CatcherSpawns...) {
				dist := distances(m, cellOf(p))
				for i, d := range dist {
					if d < 0 {
						t.Fatalf("%dx%d seed %d: cell %d unreachable from spawn %v", n, n, seed, i, p)
					}
				}
			}
			// Every runner can actually reach every catcher (and vice versa)
			// through the corridors; the path is never shorter than the
			// manhattan separation guarantee.
			for _, r := range res.RunnerSpawns {
				for _, c := range res.CatcherSpawns {
					d := bfs(m, cellOf(r), cellOf(c))
					if d < MinSpawnSeparation {
						t.Fatalf("%dx%d seed %d: path %d between runner %v and catcher %v", n, n, seed, d, r, c)
					}
				}
			}
		}
	}
}

func TestSpawnSeparationHoldsForMaxCountsAndOddRequests(t *testing.T) {
	cases := []struct{ runners, catchers int }{
		{1, 1}, {2, 1}, {8, 1}, {8, 4}, {protocol.MaxMinPlayers, 4}, {protocol.MaxMinPlayers, 17}, {16, 4}, {1, 4},
	}
	for _, n := range allSizes(t) {
		for _, tc := range cases {
			for seed := int64(1); seed <= 10; seed++ {
				res := gen(t, n, seed, tc.runners, tc.catchers)
				if len(res.RunnerSpawns) != tc.runners || len(res.CatcherSpawns) != tc.catchers {
					t.Fatalf("%dx%d r=%d c=%d: got %d/%d spawns", n, n, tc.runners, tc.catchers, len(res.RunnerSpawns), len(res.CatcherSpawns))
				}
				g := gridmath.Grid{M: res.Maze}
				seenR := map[gridmath.Cell]bool{}
				for _, p := range res.RunnerSpawns {
					c := cellOf(p)
					if !g.InBounds(c) || seenR[c] {
						t.Fatalf("%dx%d seed %d: runner spawn %v out of bounds or duplicated", n, n, seed, p)
					}
					seenR[c] = true
					for _, q := range res.CatcherSpawns {
						if d := gridmath.Manhattan(c, cellOf(q)); d < MinSpawnSeparation {
							t.Fatalf("%dx%d seed %d r=%d c=%d: runner %v only %d from catcher %v", n, n, seed, tc.runners, tc.catchers, c, d, cellOf(q))
						}
					}
				}
				for _, q := range res.CatcherSpawns {
					if !g.InBounds(cellOf(q)) {
						t.Fatalf("%dx%d seed %d: catcher spawn %v out of bounds", n, n, seed, q)
					}
				}
			}
		}
	}
}

func TestFirstCatcherSpawnsAtCenterAndOthersNearby(t *testing.T) {
	for _, n := range allSizes(t) {
		for seed := int64(1); seed <= 10; seed++ {
			res := gen(t, n, seed, 4, 4)
			center := gridmath.Cell{X: n / 2, Y: n / 2}
			if got := cellOf(res.CatcherSpawns[0]); got != center {
				t.Fatalf("%dx%d seed %d: first catcher at %v, want center %v", n, n, seed, got, center)
			}
			for _, q := range res.CatcherSpawns[1:] {
				if d := gridmath.Manhattan(cellOf(q), center); d > 2 {
					t.Fatalf("%dx%d seed %d: catcher %v is %d from center; expected the smallest central radius", n, n, seed, cellOf(q), d)
				}
			}
		}
	}
}

func TestRunnersSpreadOutFromEachOther(t *testing.T) {
	for _, n := range allSizes(t) {
		for seed := int64(1); seed <= 10; seed++ {
			res := gen(t, n, seed, 4, 1)
			for i, a := range res.RunnerSpawns {
				for j, b := range res.RunnerSpawns {
					if i < j && gridmath.Manhattan(cellOf(a), cellOf(b)) < 2 {
						t.Fatalf("%dx%d seed %d: runners %v and %v are adjacent", n, n, seed, a, b)
					}
				}
			}
		}
	}
}

func TestSeedIndependentOfSpawnCounts(t *testing.T) {
	// The wall layout depends only on seed and size, so the same seed with a
	// different roster size yields the same maze (rooms can be replayed).
	for _, n := range allSizes(t) {
		a := gen(t, n, 11, 2, 1)
		b := gen(t, n, 11, 8, 4)
		if string(a.Maze.Walls) != string(b.Maze.Walls) {
			t.Fatalf("%dx%d: walls changed with spawn counts", n, n)
		}
	}
}

func TestZeroSpawnsAllowed(t *testing.T) {
	res := gen(t, 15, 5, 0, 0)
	if len(res.RunnerSpawns) != 0 || len(res.CatcherSpawns) != 0 {
		t.Fatalf("spawns %+v %+v", res.RunnerSpawns, res.CatcherSpawns)
	}
	if err := CheckConnected(res.Maze); err != nil {
		t.Fatal(err)
	}
}

func TestBoundaryWallsAndNoDiagonalLeaks(t *testing.T) {
	for _, n := range allSizes(t) {
		res := gen(t, n, 3, 2, 1)
		g := gridmath.Grid{M: res.Maze}
		for i := 0; i < n; i++ {
			if g.CanMove(gridmath.Cell{X: i, Y: 0}, protocol.DirUp) || g.CanMove(gridmath.Cell{X: i, Y: n - 1}, protocol.DirDown) ||
				g.CanMove(gridmath.Cell{X: 0, Y: i}, protocol.DirLeft) || g.CanMove(gridmath.Cell{X: n - 1, Y: i}, protocol.DirRight) {
				t.Fatalf("%dx%d: boundary open at index %d", n, n, i)
			}
		}
		for _, b := range res.Maze.Walls {
			if b&^allWalls != 0 {
				t.Fatalf("wall byte %08b has bits outside the four cardinal walls", b)
			}
		}
	}
}

func TestCheckConnectedRejectsBrokenMazes(t *testing.T) {
	res := gen(t, 15, 9, 2, 1)
	// Seal off cell (0,0) on all sides (symmetrically) -> unreachable.
	sealed := protocol.Maze{W: 15, H: 15, Walls: append(protocol.Walls(nil), res.Maze.Walls...)}
	g := gridmath.Grid{M: sealed}
	origin := gridmath.Cell{}
	for _, d := range gridmath.Cardinals {
		nb := gridmath.Neighbor(origin, d)
		if !g.InBounds(nb) {
			continue
		}
		sealed.Walls[0] |= gridmath.WallBit(d)
		sealed.Walls[nb.Y*15+nb.X] |= gridmath.WallBit(gridmath.Opposite(d))
	}
	if err := CheckConnected(sealed); err == nil {
		t.Fatal("sealed cell should be reported unreachable")
	}
	// One-sided wall -> asymmetric.
	asym := protocol.Maze{W: 15, H: 15, Walls: append(protocol.Walls(nil), res.Maze.Walls...)}
	ga := gridmath.Grid{M: asym}
	c := gridmath.Cell{X: 7, Y: 7}
	dirs := ga.OpenDirs(c)
	if len(dirs) == 0 {
		t.Fatal("center cell should have an opening")
	}
	asym.Walls[7*15+7] |= gridmath.WallBit(dirs[0])
	if err := CheckSymmetry(asym); err == nil {
		t.Fatal("one-sided wall should fail symmetry")
	}
	if err := CheckConnected(asym); err == nil {
		t.Fatal("CheckConnected must include the symmetry check")
	}
	// Missing boundary wall.
	open := protocol.Maze{W: 15, H: 15, Walls: append(protocol.Walls(nil), res.Maze.Walls...)}
	open.Walls[0] &^= protocol.WallNorth
	if err := CheckSymmetry(open); err == nil {
		t.Fatal("open boundary should fail")
	}
	// Wrong length.
	if err := CheckConnected(protocol.Maze{W: 15, H: 15, Walls: make(protocol.Walls, 10)}); err == nil {
		t.Fatal("bad walls length should fail")
	}
}

func TestValidateBoundaries(t *testing.T) {
	ok := []protocol.MazeGenerateRequest{
		{W: MinSide, H: MinSide},
		{W: MaxSide, H: MaxSide, Runners: MaxRunners, Catchers: MaxSide * MaxSide},
	}
	for _, r := range ok {
		if err := Validate(r); err != nil {
			t.Fatalf("%+v: %v", r, err)
		}
	}
	bad := []protocol.MazeGenerateRequest{
		{W: MinSide - 2, H: MinSide - 2},
		{W: MaxSide + 2, H: MaxSide + 2},
		{W: MinSide + 1, H: MinSide + 1},
		{W: 0, H: 0},
		{W: 15, H: 15, Runners: MaxRunners + 1},
		{W: 15, H: 15, Catchers: -1},
		{W: 15, H: 15, Catchers: 15*15 + 1},
	}
	for _, r := range bad {
		if err := Validate(r); err == nil {
			t.Fatalf("%+v should be rejected", r)
		}
	}
	// The contract sizes all validate.
	for _, n := range allSizes(t) {
		if err := Validate(protocol.MazeGenerateRequest{W: n, H: n, Runners: protocol.MaxMinPlayers, Catchers: 4}); err != nil {
			t.Fatalf("contract size %d: %v", n, err)
		}
	}
}

func TestSmallestMazeStillSeatsFullRoom(t *testing.T) {
	// MinSide (7) with a full roster of 8 runners and 4 catchers is a tight
	// fit for the separation rule; the generator must report an error rather
	// than violate it, and the 15x15 small contract size must always succeed.
	for seed := int64(1); seed <= 20; seed++ {
		res, err := Generate(protocol.MazeGenerateRequest{W: MinSide, H: MinSide, Seed: seed, Runners: protocol.MaxMinPlayers, Catchers: 4})
		if err != nil {
			continue
		}
		for _, r := range res.RunnerSpawns {
			for _, c := range res.CatcherSpawns {
				if gridmath.Manhattan(cellOf(r), cellOf(c)) < MinSpawnSeparation {
					t.Fatalf("7x7 seed %d violated separation", seed)
				}
			}
		}
	}
	for seed := int64(1); seed <= 50; seed++ {
		gen(t, 15, seed, protocol.MaxMinPlayers, 4)
	}
}
