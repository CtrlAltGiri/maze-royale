package simulation

import (
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
