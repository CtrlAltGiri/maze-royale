// Package gridmath holds cell arithmetic shared by the maze generator, the
// simulation and tests: direction vectors, wall bit lookups, distance and
// wall aware movement of a point through a maze.
package gridmath

import (
	"math"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// Vec is a 2D vector in cell units.
type Vec struct{ X, Y float64 }

// Cell is an integer grid coordinate.
type Cell struct{ X, Y int }

// Center returns the center of the cell in cell units.
func (c Cell) Center() Vec { return Vec{float64(c.X) + 0.5, float64(c.Y) + 0.5} }

// CellOf returns the cell containing p.
func CellOf(p Vec) Cell { return Cell{int(math.Floor(p.X)), int(math.Floor(p.Y))} }

// Dist is the euclidean distance between two points.
func Dist(a, b Vec) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

// Manhattan is the grid distance between two cells.
func Manhattan(a, b Cell) int { return abs(a.X-b.X) + abs(a.Y-b.Y) }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Delta returns the unit step for a direction. DirNone returns zero.
func Delta(d protocol.Direction) (dx, dy int) {
	switch d {
	case protocol.DirUp:
		return 0, -1
	case protocol.DirDown:
		return 0, 1
	case protocol.DirLeft:
		return -1, 0
	case protocol.DirRight:
		return 1, 0
	}
	return 0, 0
}

// WallBit returns the wall bit that blocks movement in direction d.
func WallBit(d protocol.Direction) byte {
	switch d {
	case protocol.DirUp:
		return protocol.WallNorth
	case protocol.DirRight:
		return protocol.WallEast
	case protocol.DirDown:
		return protocol.WallSouth
	case protocol.DirLeft:
		return protocol.WallWest
	}
	return 0
}

// Opposite returns the reverse direction.
func Opposite(d protocol.Direction) protocol.Direction {
	switch d {
	case protocol.DirUp:
		return protocol.DirDown
	case protocol.DirDown:
		return protocol.DirUp
	case protocol.DirLeft:
		return protocol.DirRight
	case protocol.DirRight:
		return protocol.DirLeft
	}
	return protocol.DirNone
}

// Cardinals lists the four movement directions in a fixed order.
var Cardinals = []protocol.Direction{protocol.DirUp, protocol.DirRight, protocol.DirDown, protocol.DirLeft}

// Grid wraps a protocol.Maze with lookup helpers.
type Grid struct{ M protocol.Maze }

// InBounds reports whether the cell is inside the maze.
func (g Grid) InBounds(c Cell) bool {
	return c.X >= 0 && c.Y >= 0 && c.X < g.M.W && c.Y < g.M.H
}

// Walls returns the wall byte for a cell; out of bounds cells are fully walled.
func (g Grid) Walls(c Cell) byte {
	if !g.InBounds(c) {
		return protocol.WallNorth | protocol.WallEast | protocol.WallSouth | protocol.WallWest
	}
	return g.M.Walls[c.Y*g.M.W+c.X]
}

// CanMove reports whether an entity in cell c may step in direction d.
func (g Grid) CanMove(c Cell, d protocol.Direction) bool {
	if d == protocol.DirNone {
		return false
	}
	if g.Walls(c)&WallBit(d) != 0 {
		return false
	}
	dx, dy := Delta(d)
	return g.InBounds(Cell{c.X + dx, c.Y + dy})
}

// OpenDirs returns the directions an entity in c may step toward.
func (g Grid) OpenDirs(c Cell) []protocol.Direction {
	var out []protocol.Direction
	for _, d := range Cardinals {
		if g.CanMove(c, d) {
			out = append(out, d)
		}
	}
	return out
}

// Neighbor returns the cell one step in d from c.
func Neighbor(c Cell, d protocol.Direction) Cell {
	dx, dy := Delta(d)
	return Cell{c.X + dx, c.Y + dy}
}

// Step advances p by speed*dt in direction d, respecting walls. Entities are
// treated as points that travel along cell centers: perpendicular offset is
// snapped toward the center of the lane so movement stays on the grid. When
// the next cell in d is walled off, movement stops at the cell center.
//
// It returns the new position and the direction actually being travelled
// (DirNone when blocked).
func (g Grid) Step(p Vec, d protocol.Direction, speed, dt float64) (Vec, protocol.Direction) {
	if d == protocol.DirNone || !d.Valid() || speed <= 0 || dt <= 0 {
		return p, protocol.DirNone
	}
	remaining := speed * dt
	const eps = 1e-9
	for remaining > eps {
		c := CellOf(p)
		center := c.Center()
		dx, dy := Delta(d)
		// Snap onto the lane axis so we don't drift diagonally.
		if dx != 0 {
			p.Y = center.Y
		} else {
			p.X = center.X
		}
		// Distance along d from p to the center of the current cell.
		along := (center.X-p.X)*float64(dx) + (center.Y-p.Y)*float64(dy)
		if along > eps {
			// Still approaching the center: always allowed.
			mv := math.Min(along, remaining)
			p.X += float64(dx) * mv
			p.Y += float64(dy) * mv
			remaining -= mv
			continue
		}
		// At or past the center: need to check the wall toward d.
		if !g.CanMove(c, d) {
			return center, protocol.DirNone
		}
		// Distance to the next cell's center.
		next := Neighbor(c, d).Center()
		toNext := (next.X-p.X)*float64(dx) + (next.Y-p.Y)*float64(dy)
		mv := math.Min(toNext, remaining)
		p.X += float64(dx) * mv
		p.Y += float64(dy) * mv
		remaining -= mv
	}
	return p, d
}

// AtCenter reports whether p is within tol of its cell center.
func AtCenter(p Vec, tol float64) bool {
	c := CellOf(p).Center()
	return math.Abs(p.X-c.X) <= tol && math.Abs(p.Y-c.Y) <= tol
}

// Caught reports whether a catcher at c and runner at r are within CatchRadius.
func Caught(c, r Vec) bool { return Dist(c, r) < protocol.CatchRadius }

// Lerp interpolates between a and b by t in [0,1].
func Lerp(a, b Vec, t float64) Vec {
	return Vec{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t}
}
