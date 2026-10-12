package simulation

import (
	"encoding/json"
	"fmt"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// bullet is one live projectile. Bullets travel along corridors like any other
// entity, despawn on walls, and down the first catcher they touch.
type bullet struct {
	id  string
	pos gridmath.Vec
	dir protocol.Direction
}

func (m *Match) nextBulletID() string {
	return fmt.Sprintf("b%d", m.tick*1000+int64(len(m.bullets)+1))
}

// moveBullets advances every live bullet one tick. A bullet that hits a wall
// despawns; a bullet within BulletRadius of a non-downed catcher knocks it down
// and despawns. One bullet hits at most one catcher.
func (m *Match) moveBullets() []json.RawMessage {
	var frames []json.RawMessage
	var alive []*bullet
	for _, b := range m.bullets {
		pos, travelled := m.grid.Step(b.pos, b.dir, protocol.BulletSpeed, tickDT)
		if travelled == protocol.DirNone {
			continue
		}
		b.pos = pos
		hit := false
		for _, c := range m.catchers {
			if m.tick < c.downUntil {
				continue
			}
			if gridmath.Dist(b.pos, c.pos) >= protocol.BulletRadius {
				continue
			}
			c.downUntil = m.tick + int64(protocol.CatcherDownMS/protocol.TickMillis)
			c.dir = protocol.DirNone
			frames = append(frames, encode(protocol.Event{
				T: protocol.TypeEvent, Kind: protocol.EventCatcherDown,
				CatcherID: c.id, Tick: m.tick, MS: int64(protocol.CatcherDownMS),
			}))
			hit = true
			break
		}
		if !hit {
			alive = append(alive, b)
		}
	}
	m.bullets = alive
	return frames
}
