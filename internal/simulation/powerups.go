package simulation

import (
	"encoding/json"
	"fmt"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

type pickup struct {
	protocol.PowerUp
	respawnTick int64
	active      bool
	noRespawn   bool
}

func (m *Match) initPowerUps() {
	for i, kind := range []string{protocol.PowerUpSpeed, protocol.PowerUpInvisibility, protocol.PowerUpSpeed, protocol.PowerUpInvisibility} {
		m.powerUps = append(m.powerUps, pickup{PowerUp: protocol.PowerUp{ID: fmt.Sprintf("power%d", i+1), Kind: kind}})
	}
	// The gun sits at the maze center from match start. It bypasses random
	// placement: catchers spawn in the central region, so the 2-cell clearance
	// rule in respawnPowerUps would never allow it there.
	m.powerUps = append(m.powerUps, pickup{
		PowerUp:   protocol.PowerUp{ID: "power5", Kind: protocol.PowerUpGun, X: m.center.X, Y: m.center.Y},
		active:    true,
		noRespawn: true,
	})
	m.respawnPowerUps()
}

func (m *Match) respawnPowerUps() {
	if m.powerUpRNG == nil {
		return
	}
	for i := range m.powerUps {
		p := &m.powerUps[i]
		if p.active || p.noRespawn || m.tick < p.respawnTick {
			continue
		}
		for _, n := range m.powerUpRNG.Perm(m.grid.M.W * m.grid.M.H) {
			cell := gridmath.Cell{X: n % m.grid.M.W, Y: n / m.grid.M.W}
			pos := cell.Center()
			if len(m.grid.OpenDirs(cell)) == 0 {
				continue
			}
			blocked := false
			for _, r := range m.runners {
				if r.alive && gridmath.Dist(pos, r.pos) < 2 {
					blocked = true
					break
				}
			}
			for _, c := range m.catchers {
				if gridmath.Dist(pos, c.pos) < 2 {
					blocked = true
					break
				}
			}
			for _, other := range m.powerUps {
				if other.active && other.X == pos.X && other.Y == pos.Y {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
			p.X, p.Y, p.active = pos.X, pos.Y, true
			break
		}
		if !p.active {
			p.respawnTick = m.tick + int64(protocol.TickRate)
		}
	}
}

func (m *Match) activePowerUps() []protocol.PowerUp {
	out := make([]protocol.PowerUp, 0, len(m.powerUps))
	for _, p := range m.powerUps {
		if p.active {
			out = append(out, p.PowerUp)
		}
	}
	return out
}

func (m *Match) collectPowerUps(r *runner) []json.RawMessage {
	var frames []json.RawMessage
	for i := range m.powerUps {
		p := &m.powerUps[i]
		if !p.active || gridmath.CellOf(r.pos) != gridmath.CellOf(gridmath.Vec{X: p.X, Y: p.Y}) || gridmath.Dist(r.pos, gridmath.Vec{X: p.X, Y: p.Y}) > protocol.PowerUpRadius {
			continue
		}
		duration := int64(protocol.PowerUpSpeedMS)
		switch p.Kind {
		case protocol.PowerUpInvisibility:
			duration = protocol.PowerUpInvisibilityMS
			r.invisibleUntil = m.tick + duration/int64(protocol.TickMillis)
		case protocol.PowerUpGun:
			duration = 0
			r.hasGun = true
		default:
			r.speedUntil = m.tick + duration/int64(protocol.TickMillis)
		}
		p.active = false
		p.respawnTick = m.tick + int64(protocol.PowerUpRespawnMS/protocol.TickMillis)
		frames = append(frames, encode(protocol.Event{T: protocol.TypeEvent, Kind: protocol.EventPowerUpCollected, PlayerID: r.id, Tick: m.tick, MS: duration, PowerUpID: p.ID, PowerUpKind: p.Kind}))
	}
	return frames
}

func (m *Match) effectMS(until int64) int64 {
	if until <= m.tick {
		return 0
	}
	return (until - m.tick) * int64(protocol.TickMillis)
}
