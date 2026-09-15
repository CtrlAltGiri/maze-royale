package simulation

import (
	"math/rand"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// Mover decides where a catcher goes. v1 ships a randomized walk; an AI
// catcher implements this same interface and is swapped in at match creation.
type Mover interface {
	// Next is called once per tick when the catcher is at a cell center and
	// must choose its direction. cur is the direction it was travelling
	// (DirNone at spawn). Implementations must return a direction that
	// grid.CanMove(cell, dir) accepts, or DirNone to stand still.
	Next(grid gridmath.Grid, cell gridmath.Cell, cur protocol.Direction, runners []gridmath.Vec) protocol.Direction
}

// RandomWalk keeps travelling along corridors, picks a random branch at
// intersections and only reverses at a dead end.
type RandomWalk struct {
	rng *rand.Rand
}

// NewRandomWalk returns a RandomWalk seeded deterministically.
func NewRandomWalk(seed int64) *RandomWalk {
	return &RandomWalk{rng: rand.New(rand.NewSource(seed))}
}

// Next implements Mover.
func (r *RandomWalk) Next(grid gridmath.Grid, cell gridmath.Cell, cur protocol.Direction, _ []gridmath.Vec) protocol.Direction {
	open := grid.OpenDirs(cell)
	if len(open) == 0 {
		return protocol.DirNone
	}
	back := gridmath.Opposite(cur)
	forward := make([]protocol.Direction, 0, len(open))
	for _, d := range open {
		if d != back {
			forward = append(forward, d)
		}
	}
	if len(forward) == 0 {
		// Dead end: the only way out is back.
		return back
	}
	if len(forward) == 1 {
		return forward[0]
	}
	return forward[r.rng.Intn(len(forward))]
}
