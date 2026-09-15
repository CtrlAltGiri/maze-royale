package simulation

import (
	"encoding/json"
	"math"
	"sort"
	"sync"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

const (
	tickDT    = float64(protocol.TickMillis) / 1000.0
	centerTol = 1e-6
)

type runner struct {
	id, name, color string
	isBot           bool
	pos             gridmath.Vec
	want            protocol.Direction // latest intent
	moving          protocol.Direction // direction actually travelled last tick
	seq             int64
	alive           bool
	survivedMS      int64
}

type catcher struct {
	id    string
	pos   gridmath.Vec
	dir   protocol.Direction
	speed float64
	mover Mover
}

// Match is one authoritative game. All methods are safe for concurrent use.
type Match struct {
	mu sync.Mutex

	id       string
	roomCode string
	grid     gridmath.Grid
	start    protocol.MatchStart // You left empty; filled per recipient
	roundMS  int64
	runners  []*runner
	catchers []*catcher
	tick     int64
	ended    bool
	winners  []string
	last     protocol.Snapshot
}

// StepResult is what one tick produced.
type StepResult struct {
	Frames []json.RawMessage
	Ended  bool
	End    *protocol.MatchEnd
}

// ID returns the match id.
func (m *Match) ID() string { return m.id }

// Ended reports whether the match is over.
func (m *Match) Ended() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ended
}

// Tick returns the number of ticks processed so far.
func (m *Match) Tick() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tick
}

// Input records a player's latest intent; a stale seq is ignored.
func (m *Match) Input(playerID string, seq int64, dir protocol.Direction) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.runners {
		if r.id != playerID {
			continue
		}
		if seq < r.seq {
			return true
		}
		r.seq = seq
		r.want = dir
		return true
	}
	return false
}

// StartFor returns the match_start frame addressed to playerID with the given role.
func (m *Match) StartFor(playerID, role string) protocol.MatchStart {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startFor(playerID, role)
}

func (m *Match) startFor(playerID, role string) protocol.MatchStart {
	s := m.start
	s.You = protocol.You{PlayerID: playerID, Role: role}
	for _, r := range m.runners {
		if r.id == playerID {
			s.You.Color = r.color
		}
	}
	return s
}

// State returns the spectator view used by GET /matches/{id}.
func (m *Match) State() protocol.SimMatchStateResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	return protocol.SimMatchStateResponse{
		Start:    m.startFor("", protocol.RoleSpectator),
		Snapshot: m.last,
		Ended:    m.ended,
	}
}

// Step advances the match by one tick. nowMS is the server clock stamped on
// the snapshot; the game clock itself is tick based so tests never wait.
func (m *Match) Step(nowMS int64) StepResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ended {
		return StepResult{Ended: true}
	}
	m.tick++
	elapsed := m.tick * int64(protocol.TickMillis)

	var runnerPos []gridmath.Vec
	for _, r := range m.runners {
		if !r.alive {
			continue
		}
		r.pos, r.moving = m.grid.Step(r.pos, r.want, protocol.RunnerSpeed, tickDT)
		runnerPos = append(runnerPos, r.pos)
	}
	for _, c := range m.catchers {
		m.moveCatcher(c, runnerPos)
	}

	var frames []json.RawMessage
	for _, c := range m.catchers {
		for _, r := range m.runners {
			if r.alive && gridmath.Caught(c.pos, r.pos) {
				r.alive = false
				r.survivedMS = elapsed
				frames = append(frames, encode(protocol.Event{
					T: protocol.TypeEvent, Kind: protocol.EventCaught,
					PlayerID: r.id, CatcherID: c.id, Tick: m.tick,
				}))
			}
		}
	}

	alive := 0
	for _, r := range m.runners {
		if r.alive {
			alive++
		}
	}
	timeLeft := m.roundMS - elapsed
	if timeLeft < 0 {
		timeLeft = 0
	}
	m.last = m.snapshot(nowMS, timeLeft, alive)
	frames = append([]json.RawMessage{encode(m.last)}, frames...)

	res := StepResult{Frames: frames}
	if alive == 0 || (alive == 1 && len(m.runners) > 1) || elapsed >= m.roundMS {
		end := m.finish(elapsed)
		res.Ended, res.End = true, &end
		res.Frames = append(res.Frames, encode(end))
	}
	return res
}

// moveCatcher travels along the corridor and consults the Mover every time
// the catcher sits on a cell center, so turns only happen at intersections.
func (m *Match) moveCatcher(c *catcher, runners []gridmath.Vec) {
	remaining := c.speed * tickDT
	for i := 0; remaining > 1e-9 && i < 8; i++ {
		if gridmath.AtCenter(c.pos, centerTol) {
			cell := gridmath.CellOf(c.pos)
			c.pos = cell.Center()
			c.dir = c.mover.Next(m.grid, cell, c.dir, runners)
			if c.dir == protocol.DirNone || !m.grid.CanMove(cell, c.dir) {
				c.dir = protocol.DirNone
				return
			}
		}
		dist := distToNextCenter(c.pos, c.dir)
		mv := math.Min(dist, remaining)
		var travelled protocol.Direction
		c.pos, travelled = m.grid.Step(c.pos, c.dir, c.speed, mv/c.speed)
		remaining -= mv
		if travelled == protocol.DirNone {
			c.dir = protocol.DirNone
			return
		}
	}
}

// distToNextCenter is the distance along d from p to the next cell center it
// will reach (the current cell's if still approaching it, else the neighbour's).
func distToNextCenter(p gridmath.Vec, d protocol.Direction) float64 {
	dx, dy := gridmath.Delta(d)
	cell := gridmath.CellOf(p)
	center := cell.Center()
	along := (center.X-p.X)*float64(dx) + (center.Y-p.Y)*float64(dy)
	if along > 1e-9 {
		return along
	}
	next := gridmath.Neighbor(cell, d).Center()
	return (next.X-p.X)*float64(dx) + (next.Y-p.Y)*float64(dy)
}

func (m *Match) snapshot(nowMS, timeLeft int64, alive int) protocol.Snapshot {
	s := protocol.Snapshot{
		T: protocol.TypeSnapshot, Tick: m.tick, TS: nowMS,
		Players:    make([]protocol.SnapshotPlayer, 0, len(m.runners)),
		Catchers:   make([]protocol.SnapshotCatcher, 0, len(m.catchers)),
		TimeLeftMS: timeLeft, AliveCount: alive,
	}
	for _, r := range m.runners {
		dir := r.moving
		if !r.alive {
			dir = protocol.DirNone
		}
		s.Players = append(s.Players, protocol.SnapshotPlayer{ID: r.id, X: r.pos.X, Y: r.pos.Y, Dir: dir, Alive: r.alive})
	}
	for _, c := range m.catchers {
		s.Catchers = append(s.Catchers, protocol.SnapshotCatcher{ID: c.id, X: c.pos.X, Y: c.pos.Y, Dir: c.dir})
	}
	return s
}

func (m *Match) finish(elapsed int64) protocol.MatchEnd {
	m.ended = true
	winners := []string{}
	for _, r := range m.runners {
		if r.alive {
			r.survivedMS = elapsed
			winners = append(winners, r.id)
		}
	}
	m.winners = winners
	order := make([]*runner, len(m.runners))
	copy(order, m.runners)
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].alive != order[j].alive {
			return order[i].alive
		}
		return order[i].survivedMS > order[j].survivedMS
	})
	standings := make([]protocol.Standing, 0, len(order))
	for i, r := range order {
		place := i + 1
		if i > 0 && order[i-1].alive == r.alive && order[i-1].survivedMS == r.survivedMS {
			place = standings[i-1].Place
		}
		standings = append(standings, protocol.Standing{PlayerID: r.id, Name: r.name, SurvivedMS: r.survivedMS, Place: place})
	}
	return protocol.MatchEnd{T: protocol.TypeMatchEnd, MatchID: m.id, WinnerIDs: winners, Standings: standings}
}

func encode(v any) json.RawMessage {
	b, err := protocol.Encode(v)
	if err != nil {
		return json.RawMessage(`{"t":"error","code":"internal","message":"encode"}`)
	}
	return b
}
