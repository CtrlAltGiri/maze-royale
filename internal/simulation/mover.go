package simulation

import (
	"math"
	"math/rand"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// Mover decides where a catcher goes. RandomWalk wanders; Chaser (the
// default) pursues the nearest runner. Both are swapped in at match creation
// via Config.NewMover.
type Mover interface {
	// Next is called once per tick when the catcher is at a cell center and
	// must choose its direction. cur is the direction it was travelling
	// (DirNone at spawn). runners are the live runner positions and others the
	// positions of the other catchers. Implementations should return a direction
	// that grid.CanMove(cell, dir) accepts; DirNone or a walled direction lets
	// the match keep the catcher moving along the corridor instead.
	Next(grid gridmath.Grid, cell gridmath.Cell, cur protocol.Direction, runners, others []gridmath.Vec) protocol.Direction
}

// spreadLookahead is how many cells ahead of a branch the walk looks when
// scoring how far that branch leads from the other catchers.
const spreadLookahead = 4

// RandomWalk keeps travelling along corridors, picks a branch at
// intersections and only reverses at a dead end. Branch choice is weighted
// toward corridors that lead away from the other catchers so a pack spreads
// across the maze instead of clumping.
type RandomWalk struct {
	rng *rand.Rand
}

// NewRandomWalk returns a RandomWalk seeded deterministically.
func NewRandomWalk(seed int64) *RandomWalk {
	return &RandomWalk{rng: rand.New(rand.NewSource(seed))}
}

// Next implements Mover.
func (r *RandomWalk) Next(grid gridmath.Grid, cell gridmath.Cell, cur protocol.Direction, _ []gridmath.Vec, others []gridmath.Vec) protocol.Direction {
	forward := forwardDirs(grid, cell, cur)
	switch len(forward) {
	case 0:
		return protocol.DirNone
	case 1:
		return forward[0]
	}
	if len(others) == 0 {
		return forward[r.rng.Intn(len(forward))]
	}
	weights := make([]float64, len(forward))
	total := 0.0
	for i, d := range forward {
		ahead := lookahead(grid, cell, d, spreadLookahead).Center()
		nearest := math.Inf(1)
		for _, o := range others {
			nearest = math.Min(nearest, gridmath.Dist(ahead, o))
		}
		// Quadratic in distance: a branch twice as far from the pack is four
		// times as likely, while no branch is ever ruled out entirely.
		weights[i] = 0.5 + nearest*nearest
		total += weights[i]
	}
	pick := r.rng.Float64() * total
	for i, w := range weights {
		pick -= w
		if pick < 0 {
			return forward[i]
		}
	}
	return forward[len(forward)-1]
}

// forwardDirs is the set of legal moves out of cell excluding the reverse of
// cur. At a dead end it is just the reverse; in an isolated cell it is empty.
func forwardDirs(grid gridmath.Grid, cell gridmath.Cell, cur protocol.Direction) []protocol.Direction {
	open := grid.OpenDirs(cell)
	if len(open) == 0 {
		return nil
	}
	back := gridmath.Opposite(cur)
	forward := make([]protocol.Direction, 0, len(open))
	for _, d := range open {
		if d != back {
			forward = append(forward, d)
		}
	}
	if len(forward) == 0 {
		return []protocol.Direction{back}
	}
	return forward
}

// lookahead follows d from cell for up to n steps (stopping at a wall) and
// returns the cell reached.
func lookahead(grid gridmath.Grid, cell gridmath.Cell, d protocol.Direction, n int) gridmath.Cell {
	for i := 0; i < n && grid.CanMove(cell, d); i++ {
		cell = gridmath.Neighbor(cell, d)
	}
	return cell
}
