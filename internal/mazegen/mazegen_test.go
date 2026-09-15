package mazegen

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

var sizes = []int{15, 21, 31}

func gen(t *testing.T, n int, seed int64, runners, catchers int) protocol.MazeGenerateResponse {
	t.Helper()
	res, err := Generate(protocol.MazeGenerateRequest{W: n, H: n, Seed: seed, Runners: runners, Catchers: catchers})
	if err != nil {
		t.Fatalf("generate %dx%d seed %d: %v", n, n, seed, err)
	}
	return res
}

func TestDeterminism(t *testing.T) {
	for _, n := range sizes {
		a := gen(t, n, 42, 8, 4)
		b := gen(t, n, 42, 8, 4)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%dx%d: same seed produced different output", n, n)
		}
		c := gen(t, n, 43, 8, 4)
		if reflect.DeepEqual(a.Maze.Walls, c.Maze.Walls) {
			t.Fatalf("%dx%d: different seeds produced identical walls", n, n)
		}
		if a.Seed != 42 {
			t.Fatalf("seed echoed as %d", a.Seed)
		}
	}
}

func TestRandomSeed(t *testing.T) {
	a := gen(t, 15, 0, 2, 1)
	b := gen(t, 15, 0, 2, 1)
	if a.Seed == 0 || b.Seed == 0 {
		t.Fatal("seed 0 must be replaced with a real seed")
	}
	if a.Seed == b.Seed {
		t.Fatal("two random seeds collided")
	}
	replay := gen(t, 15, a.Seed, 2, 1)
	if !reflect.DeepEqual(a, replay) {
		t.Fatal("replaying the returned seed did not reproduce the maze")
	}
}

func TestConnectivityAndSymmetry(t *testing.T) {
	for _, n := range sizes {
		for seed := int64(1); seed <= 25; seed++ {
			res := gen(t, n, seed, 8, 4)
			m := res.Maze
			if m.W != n || m.H != n || len(m.Walls) != n*n {
				t.Fatalf("bad dimensions %dx%d len %d", m.W, m.H, len(m.Walls))
			}
			if err := CheckSymmetry(m); err != nil {
				t.Fatalf("%dx%d seed %d: %v", n, n, seed, err)
			}
			if err := CheckConnected(m); err != nil {
				t.Fatalf("%dx%d seed %d: %v", n, n, seed, err)
			}
			g := gridmath.Grid{M: m}
			for i := range m.Walls {
				c := gridmath.Cell{X: i % n, Y: i / n}
				for _, d := range gridmath.Cardinals {
					nb := gridmath.Neighbor(c, d)
					if !g.InBounds(nb) {
						continue
					}
					if g.CanMove(c, d) != g.CanMove(nb, gridmath.Opposite(d)) {
						t.Fatalf("wall between %v and %v is one sided", c, nb)
					}
				}
			}
		}
	}
}

func TestLoopsAndIntersections(t *testing.T) {
	for _, n := range sizes {
		res := gen(t, n, 7, 2, 2)
		g := gridmath.Grid{M: res.Maze}
		open, intersections := 0, 0
		for i := range res.Maze.Walls {
			c := gridmath.Cell{X: i % n, Y: i / n}
			dirs := len(g.OpenDirs(c))
			open += dirs
			if dirs >= 3 {
				intersections++
			}
		}
		edges := open / 2
		if edges <= n*n-1 {
			t.Fatalf("%dx%d: maze is a tree (%d edges), expected loops", n, n, edges)
		}
		if intersections == 0 {
			t.Fatalf("%dx%d: no intersections", n, n)
		}
	}
}

func TestSpawnSeparation(t *testing.T) {
	for _, n := range sizes {
		for seed := int64(1); seed <= 25; seed++ {
			res := gen(t, n, seed, 8, 4)
			if len(res.RunnerSpawns) != 8 || len(res.CatcherSpawns) != 4 {
				t.Fatalf("spawn counts %d/%d", len(res.RunnerSpawns), len(res.CatcherSpawns))
			}
			seen := map[gridmath.Cell]bool{}
			for _, p := range res.RunnerSpawns {
				rc := gridmath.CellOf(gridmath.Vec{X: p.X, Y: p.Y})
				if !(gridmath.Grid{M: res.Maze}).InBounds(rc) {
					t.Fatalf("runner spawn %v out of bounds", p)
				}
				if seen[rc] {
					t.Fatalf("duplicate runner spawn %v", rc)
				}
				seen[rc] = true
				for _, q := range res.CatcherSpawns {
					cc := gridmath.CellOf(gridmath.Vec{X: q.X, Y: q.Y})
					if d := gridmath.Manhattan(rc, cc); d < MinSpawnSeparation {
						t.Fatalf("%dx%d seed %d: runner %v is %d from catcher %v", n, n, seed, rc, d, cc)
					}
				}
			}
			for _, p := range append(res.RunnerSpawns, res.CatcherSpawns...) {
				if p.X != float64(int(p.X))+0.5 || p.Y != float64(int(p.Y))+0.5 {
					t.Fatalf("spawn %v is not a cell center", p)
				}
			}
		}
	}
}

func TestValidate(t *testing.T) {
	bad := []protocol.MazeGenerateRequest{
		{W: 14, H: 14, Runners: 1, Catchers: 1},
		{W: 15, H: 21, Runners: 1, Catchers: 1},
		{W: 3, H: 3, Runners: 1, Catchers: 1},
		{W: 15, H: 15, Runners: -1, Catchers: 1},
		{W: 15, H: 15, Runners: 1, Catchers: MaxCatchers + 1},
	}
	for _, r := range bad {
		if _, err := Generate(r); err == nil {
			t.Fatalf("expected error for %+v", r)
		}
	}
}

func TestHTTPGenerate(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()

	body, _ := json.Marshal(protocol.MazeGenerateRequest{W: 21, H: 21, Seed: 99, Runners: 3, Catchers: 2})
	resp, err := http.Post(srv.URL+"/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var raw struct {
		Maze struct {
			W     int   `json:"w"`
			H     int   `json:"h"`
			Walls []int `json:"walls"`
		} `json:"maze"`
		Seed          int64            `json:"seed"`
		RunnerSpawns  []protocol.Point `json:"runner_spawns"`
		CatcherSpawns []protocol.Point `json:"catcher_spawns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw.Seed != 99 || raw.Maze.W != 21 || len(raw.Maze.Walls) != 441 || len(raw.RunnerSpawns) != 3 || len(raw.CatcherSpawns) != 2 {
		t.Fatalf("unexpected response %+v", raw)
	}
	for _, v := range raw.Maze.Walls {
		if v < 0 || v > 15 {
			t.Fatalf("wall value %d out of range", v)
		}
	}
	want := gen(t, 21, 99, 3, 2)
	var got protocol.MazeGenerateResponse
	b2, _ := json.Marshal(raw)
	if err := json.Unmarshal(b2, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("HTTP response differs from Generate")
	}

	resp, err = http.Post(srv.URL+"/generate", "application/json", bytes.NewReader([]byte(`{"w":14,"h":14}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var he protocol.HTTPError
	if err := json.NewDecoder(resp.Body).Decode(&he); err != nil || he.Code != protocol.ErrBadRequest {
		t.Fatalf("bad error body %+v %v", he, err)
	}

	resp, err = http.Post(srv.URL+"/generate", "application/json", bytes.NewReader([]byte(`not json`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad json, got %d", resp.StatusCode)
	}
}
