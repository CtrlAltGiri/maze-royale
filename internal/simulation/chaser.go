package simulation

import (
	"math"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// Chaser heads for the nearest live runner by maze path length. It runs a
// breadth-first search over grid.CanMove from the catcher's cell and takes
// the first step of the shortest path to the first runner cell reached. With
// no visible or reachable runner it patrols with an embedded RandomWalk.
type Chaser struct {
	fallback *RandomWalk
	// BFS scratch, sized W*H and reused across calls; visited is a generation
	// stamp so nothing is cleared between decisions.
	gen    uint32
	stamp  []uint32
	target []uint32
	first  []protocol.Direction // first step from the origin that reaches the cell
	queue  []int32
}

// NewChaser returns a Chaser whose patrol fallback is seeded deterministically.
func NewChaser(seed int64) *Chaser {
	return &Chaser{fallback: NewRandomWalk(seed)}
}

// Next implements Mover.
func (c *Chaser) Next(grid gridmath.Grid, cell gridmath.Cell, cur protocol.Direction, runners, others []gridmath.Vec) protocol.Direction {
	if len(runners) == 0 {
		return c.fallback.Next(grid, cell, cur, runners, others)
	}
	w, h := grid.M.W, grid.M.H
	if n := w * h; len(c.stamp) != n {
		c.stamp = make([]uint32, n)
		c.target = make([]uint32, n)
		c.first = make([]protocol.Direction, n)
		c.queue = make([]int32, 0, n)
		c.gen = 0
	}
	c.gen++
	if c.gen == 0 {
		for i := range c.stamp {
			c.stamp[i] = 0
			c.target[i] = 0
		}
		c.gen = 1
	}
	gen := c.gen
	center := cell.Center()
	sameCell := -1
	for i, r := range runners {
		t := gridmath.CellOf(r)
		if !grid.InBounds(t) {
			continue
		}
		c.target[t.Y*w+t.X] = gen
		if t == cell && sameCell < 0 {
			sameCell = i
		}
	}
	if sameCell >= 0 {
		dx, dy := runners[sameCell].X-center.X, runners[sameCell].Y-center.Y
		var d protocol.Direction
		if math.Abs(dx) >= math.Abs(dy) {
			if dx >= 0 {
				d = protocol.DirRight
			} else {
				d = protocol.DirLeft
			}
		} else if dy >= 0 {
			d = protocol.DirDown
		} else {
			d = protocol.DirUp
		}
		if grid.CanMove(cell, d) {
			return d
		}
		return c.fallback.Next(grid, cell, cur, runners, others)
	}
	queue := c.queue[:0]
	origin := cell.Y*w + cell.X
	c.stamp[origin] = gen
	queue = append(queue, int32(origin))
	for head := 0; head < len(queue); head++ {
		ci := int(queue[head])
		cc := gridmath.Cell{X: ci % w, Y: ci / w}
		for _, d := range gridmath.Cardinals {
			if !grid.CanMove(cc, d) {
				continue
			}
			n := gridmath.Neighbor(cc, d)
			ni := n.Y*w + n.X
			if c.stamp[ni] == gen {
				continue
			}
			c.stamp[ni] = gen
			step := c.first[ci]
			if ci == origin {
				step = d
			}
			c.first[ni] = step
			if c.target[ni] == gen {
				c.queue = queue
				return step
			}
			queue = append(queue, int32(ni))
		}
	}
	c.queue = queue
	return c.fallback.Next(grid, cell, cur, runners, others)
}
