// Package mazegen generates seeded, validated mazes (recursive backtracker) and
// serves them over HTTP on port 8083.
package mazegen

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
	"net/http"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

const (
	// MinSpawnSeparation is the minimum manhattan distance between any runner
	// spawn and any catcher spawn.
	MinSpawnSeparation = 6
	// MinSide is the smallest supported maze side.
	MinSide = 7
	// MaxSide is the largest supported maze side.
	MaxSide = 101
	// MaxRunners and MaxCatchers bound spawn counts per request.
	MaxRunners  = 64
	MaxCatchers = 16
	// LoopFraction is the share of cells that get one extra passage carved
	// after the perfect maze is built, so catchers meet intersections and loops.
	LoopFraction = 0.12

	allWalls = protocol.WallNorth | protocol.WallEast | protocol.WallSouth | protocol.WallWest
)

// NewHandler returns the service's HTTP handler.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("maze"))
	mux.HandleFunc("POST /generate", handleGenerate)
	return mux
}

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req protocol.MazeGenerateRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, err.Error())
		return
	}
	res, err := Generate(req)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// Validate checks a request against the supported ranges.
func Validate(req protocol.MazeGenerateRequest) error {
	if req.W != req.H {
		return fmt.Errorf("maze must be square: w=%d h=%d", req.W, req.H)
	}
	if req.W < MinSide || req.W > MaxSide {
		return fmt.Errorf("maze side must be between %d and %d: got %d", MinSide, MaxSide, req.W)
	}
	if req.W%2 == 0 {
		return fmt.Errorf("maze side must be odd: got %d", req.W)
	}
	if req.Runners < 0 || req.Runners > MaxRunners {
		return fmt.Errorf("runners must be between 0 and %d: got %d", MaxRunners, req.Runners)
	}
	if req.Catchers < 0 || req.Catchers > MaxCatchers {
		return fmt.Errorf("catchers must be between 0 and %d: got %d", MaxCatchers, req.Catchers)
	}
	return nil
}

// Generate builds a validated maze and spawns for req. A zero seed picks a
// random one; the seed actually used is returned so the layout can be replayed.
func Generate(req protocol.MazeGenerateRequest) (protocol.MazeGenerateResponse, error) {
	if err := Validate(req); err != nil {
		return protocol.MazeGenerateResponse{}, err
	}
	seed := req.Seed
	if seed == 0 {
		seed = randomSeed()
	}
	rng := mrand.New(mrand.NewSource(seed))

	maze := carve(req.W, req.H, rng)
	addLoops(maze, rng)
	if err := CheckConnected(maze); err != nil {
		return protocol.MazeGenerateResponse{}, err
	}
	catchers, runners, err := placeSpawns(maze, req.Runners, req.Catchers, rng)
	if err != nil {
		return protocol.MazeGenerateResponse{}, err
	}
	return protocol.MazeGenerateResponse{
		Maze:          maze,
		Seed:          seed,
		RunnerSpawns:  toPoints(runners),
		CatcherSpawns: toPoints(catchers),
	}, nil
}

func randomSeed() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 1
	}
	s := int64(binary.LittleEndian.Uint64(b[:]) >> 1)
	if s == 0 {
		s = 1
	}
	return s
}

// carve runs an iterative recursive backtracker over every cell, starting
// from the center, producing a perfect maze (a spanning tree of the grid).
func carve(w, h int, rng *mrand.Rand) protocol.Maze {
	m := protocol.Maze{W: w, H: h, Walls: make(protocol.Walls, w*h)}
	for i := range m.Walls {
		m.Walls[i] = allWalls
	}
	visited := make([]bool, w*h)
	g := gridmath.Grid{M: m}
	start := gridmath.Cell{X: w / 2, Y: h / 2}
	stack := []gridmath.Cell{start}
	visited[start.Y*w+start.X] = true
	dirs := make([]protocol.Direction, 0, 4)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		dirs = dirs[:0]
		for _, d := range gridmath.Cardinals {
			n := gridmath.Neighbor(c, d)
			if g.InBounds(n) && !visited[n.Y*w+n.X] {
				dirs = append(dirs, d)
			}
		}
		if len(dirs) == 0 {
			stack = stack[:len(stack)-1]
			continue
		}
		d := dirs[rng.Intn(len(dirs))]
		n := gridmath.Neighbor(c, d)
		openWall(m, c, d)
		visited[n.Y*w+n.X] = true
		stack = append(stack, n)
	}
	return m
}

// openWall removes the wall between c and its neighbor in d on both sides.
func openWall(m protocol.Maze, c gridmath.Cell, d protocol.Direction) {
	n := gridmath.Neighbor(c, d)
	m.Walls[c.Y*m.W+c.X] &^= gridmath.WallBit(d)
	m.Walls[n.Y*m.W+n.X] &^= gridmath.WallBit(gridmath.Opposite(d))
}

// addLoops knocks out extra interior walls so the maze has cycles and
// intersections. Walls are chosen uniformly at random among those still
// standing between two in-bounds cells; the count scales with the area.
func addLoops(m protocol.Maze, rng *mrand.Rand) {
	g := gridmath.Grid{M: m}
	want := int(float64(m.W*m.H) * LoopFraction)
	type edge struct {
		c gridmath.Cell
		d protocol.Direction
	}
	var closed []edge
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			c := gridmath.Cell{X: x, Y: y}
			for _, d := range []protocol.Direction{protocol.DirRight, protocol.DirDown} {
				n := gridmath.Neighbor(c, d)
				if g.InBounds(n) && !g.CanMove(c, d) {
					closed = append(closed, edge{c, d})
				}
			}
		}
	}
	rng.Shuffle(len(closed), func(i, j int) { closed[i], closed[j] = closed[j], closed[i] })
	if want > len(closed) {
		want = len(closed)
	}
	for _, e := range closed[:want] {
		openWall(m, e.c, e.d)
	}
}

// CheckConnected verifies every cell is reachable from (0,0) and that walls
// are symmetric (a wall between two cells is set on both sides).
func CheckConnected(m protocol.Maze) error {
	if len(m.Walls) != m.W*m.H {
		return fmt.Errorf("walls length %d != %d*%d", len(m.Walls), m.W, m.H)
	}
	if err := CheckSymmetry(m); err != nil {
		return err
	}
	dist := distances(m, gridmath.Cell{})
	for i, d := range dist {
		if d < 0 {
			return fmt.Errorf("cell (%d,%d) unreachable", i%m.W, i/m.W)
		}
	}
	return nil
}

// CheckSymmetry verifies that a wall between two adjacent cells is present on
// both sides and that the outer boundary is walled.
func CheckSymmetry(m protocol.Maze) error {
	g := gridmath.Grid{M: m}
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			c := gridmath.Cell{X: x, Y: y}
			for _, d := range gridmath.Cardinals {
				n := gridmath.Neighbor(c, d)
				here := g.Walls(c)&gridmath.WallBit(d) != 0
				if !g.InBounds(n) {
					if !here {
						return fmt.Errorf("cell (%d,%d) missing boundary wall %s", x, y, d)
					}
					continue
				}
				there := g.Walls(n)&gridmath.WallBit(gridmath.Opposite(d)) != 0
				if here != there {
					return fmt.Errorf("asymmetric wall between (%d,%d) and (%d,%d)", x, y, n.X, n.Y)
				}
			}
		}
	}
	return nil
}

// distances returns BFS path lengths from start (-1 for unreachable cells).
func distances(m protocol.Maze, start gridmath.Cell) []int {
	g := gridmath.Grid{M: m}
	dist := make([]int, m.W*m.H)
	for i := range dist {
		dist[i] = -1
	}
	dist[start.Y*m.W+start.X] = 0
	queue := []gridmath.Cell{start}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		for _, d := range g.OpenDirs(c) {
			n := gridmath.Neighbor(c, d)
			if dist[n.Y*m.W+n.X] < 0 {
				dist[n.Y*m.W+n.X] = dist[c.Y*m.W+c.X] + 1
				queue = append(queue, n)
			}
		}
	}
	return dist
}

// placeSpawns puts catchers in the central region and runners on distinct
// cells at least MinSpawnSeparation (manhattan) from every catcher, favouring
// cells far from the catchers by path length.
func placeSpawns(m protocol.Maze, runners, catchers int, rng *mrand.Rand) (cs, rs []gridmath.Cell, err error) {
	center := gridmath.Cell{X: m.W / 2, Y: m.H / 2}
	var central []gridmath.Cell
	radius := 1
	for len(central) < catchers && radius < m.W {
		central = central[:0]
		for y := center.Y - radius; y <= center.Y+radius; y++ {
			for x := center.X - radius; x <= center.X+radius; x++ {
				c := gridmath.Cell{X: x, Y: y}
				if x >= 0 && y >= 0 && x < m.W && y < m.H && gridmath.Manhattan(c, center) <= radius {
					central = append(central, c)
				}
			}
		}
		radius++
	}
	if len(central) < catchers {
		return nil, nil, fmt.Errorf("cannot place %d catchers in a %dx%d maze", catchers, m.W, m.H)
	}
	rng.Shuffle(len(central), func(i, j int) { central[i], central[j] = central[j], central[i] })
	cs = append(cs, central[:catchers]...)
	if catchers > 0 {
		cs[0] = center
	}

	// Candidate runner cells: far enough from every catcher, scored by the
	// shortest path distance to the nearest catcher so runners start with a
	// head start along the corridors, not just as the crow flies.
	pathDist := make([]int, m.W*m.H)
	for i := range pathDist {
		pathDist[i] = int(^uint(0) >> 1)
	}
	for _, c := range cs {
		for i, d := range distances(m, c) {
			if d < pathDist[i] {
				pathDist[i] = d
			}
		}
	}
	var cand []gridmath.Cell
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			c := gridmath.Cell{X: x, Y: y}
			ok := true
			for _, k := range cs {
				if gridmath.Manhattan(c, k) < MinSpawnSeparation {
					ok = false
					break
				}
			}
			if ok {
				cand = append(cand, c)
			}
		}
	}
	if len(cand) < runners {
		return nil, nil, fmt.Errorf("cannot place %d runners >= %d cells from %d catchers in a %dx%d maze", runners, MinSpawnSeparation, catchers, m.W, m.H)
	}
	rng.Shuffle(len(cand), func(i, j int) { cand[i], cand[j] = cand[j], cand[i] })
	// Keep the farthest half by path distance (stable sort on the shuffled
	// order keeps the choice seeded), then pick runners spread out from each
	// other greedily.
	sortByPathDistDesc(cand, pathDist, m.W)
	pool := cand
	if len(cand) > 2*runners && len(cand) > 4 {
		pool = cand[:max(len(cand)/2, runners)]
	}
	for len(rs) < runners {
		best, bestScore := -1, -1
		for i, c := range pool {
			score := int(^uint(0) >> 1)
			for _, r := range rs {
				if d := gridmath.Manhattan(c, r); d < score {
					score = d
				}
			}
			if len(rs) == 0 {
				score = pathDist[c.Y*m.W+c.X]
			}
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		rs = append(rs, pool[best])
		pool = append(pool[:best:best], pool[best+1:]...)
	}
	return cs, rs, nil
}

func sortByPathDistDesc(cells []gridmath.Cell, pathDist []int, w int) {
	// insertion sort keeps it stable and the inputs are small.
	for i := 1; i < len(cells); i++ {
		for j := i; j > 0 && pathDist[cells[j].Y*w+cells[j].X] > pathDist[cells[j-1].Y*w+cells[j-1].X]; j-- {
			cells[j], cells[j-1] = cells[j-1], cells[j]
		}
	}
}

func toPoints(cells []gridmath.Cell) []protocol.Point {
	out := make([]protocol.Point, 0, len(cells))
	for _, c := range cells {
		v := c.Center()
		out = append(out, protocol.Point{X: v.X, Y: v.Y})
	}
	return out
}
