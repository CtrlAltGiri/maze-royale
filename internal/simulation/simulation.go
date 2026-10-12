// Package simulation runs the authoritative 20Hz tick loop per match: movement,
// collision, catchers, elimination, win condition. Port 8092.
package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gridmath"
	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// Config wires the service to its peers. Zero values fall back to the
// SIM_MAZE_URL / SIM_GATEWAY_URL / SIM_STATS_URL environment variables and
// then to the localhost defaults in protocol.
type Config struct {
	MazeURL    string
	GatewayURL string
	StatsURL   string

	// Push delivers frames to the gateway; nil means an ordered async HTTP
	// queue against GatewayURL. Tests inject a recorder.
	Push func(ctx context.Context, p protocol.GatewayPush) error
	// Report posts the result to stats; nil means HTTP against StatsURL.
	Report func(ctx context.Context, r protocol.StatsResultRequest) error
	// Now stamps snapshots; nil means time.Now.
	Now func() time.Time
	// ManualTick disables the wall clock ticker so tests drive Step themselves.
	ManualTick bool
	// NewMover builds the catcher brain for a match; nil means Chaser; pass
	// NewRandomWalk to get the wandering v1 brain.
	NewMover func(seed int64, index int) Mover
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Service holds every live match.
type Service struct {
	cfg     Config
	maze    *httpx.Client
	gateway *httpx.Client
	stats   *httpx.Client

	mu      sync.Mutex
	matches map[string]*Match

	pushQ chan protocol.GatewayPush
}

// New builds a Service from cfg.
func New(cfg Config) *Service {
	if cfg.MazeURL == "" {
		cfg.MazeURL = envOr("SIM_MAZE_URL", protocol.MazeURL)
	}
	if cfg.GatewayURL == "" {
		cfg.GatewayURL = envOr("SIM_GATEWAY_URL", protocol.GatewayURL)
	}
	if cfg.StatsURL == "" {
		cfg.StatsURL = envOr("SIM_STATS_URL", protocol.StatsURL)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewMover == nil {
		cfg.NewMover = func(seed int64, index int) Mover { return NewChaser(seed + int64(index)) }
	}
	s := &Service{
		cfg:     cfg,
		maze:    httpx.NewClient(cfg.MazeURL),
		gateway: httpx.NewClient(cfg.GatewayURL),
		stats:   httpx.NewClient(cfg.StatsURL),
		matches: map[string]*Match{},
	}
	if s.cfg.Push == nil {
		s.pushQ = make(chan protocol.GatewayPush, 1024)
		go s.pushLoop()
		s.cfg.Push = s.enqueuePush
	}
	if s.cfg.Report == nil {
		s.cfg.Report = func(ctx context.Context, r protocol.StatsResultRequest) error {
			return s.stats.Post(ctx, "/results", r, nil)
		}
	}
	return s
}

// NewHandler returns the service's HTTP handler with default peer wiring.
func NewHandler() http.Handler { return New(Config{}).Handler() }

// Handler returns the HTTP routes from docs/CONTRACT.md.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("simulation"))
	mux.HandleFunc("POST /matches", s.handleCreate)
	mux.HandleFunc("GET /matches/{id}", s.handleGet)
	mux.HandleFunc("POST /matches/{id}/input", s.handleInput)
	mux.HandleFunc("POST /matches/{id}/shoot", s.handleShoot)
	return mux
}

// Match returns a live match by id.
func (s *Service) Match(id string) (*Match, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.matches[id]
	return m, ok
}

// Step runs one tick of a match, pushing its frames and reporting the result
// when it ends. Tests call this instead of waiting on the wall clock.
func (s *Service) Step(ctx context.Context, m *Match) StepResult {
	res := m.Step(s.cfg.Now().UnixMilli())
	if len(res.Frames) > 0 {
		if err := s.cfg.Push(ctx, protocol.GatewayPush{MatchID: m.id, RoomCode: m.roomCode, Frames: res.Frames}); err != nil {
			log.Printf("[simulation] push match=%s: %v", m.id, err)
		}
	}
	if res.End != nil {
		req := protocol.StatsResultRequest{
			MatchID: m.id, EndedAt: s.cfg.Now().UnixMilli(),
			WinnerIDs: res.End.WinnerIDs, Standings: res.End.Standings,
		}
		if err := s.cfg.Report(ctx, req); err != nil {
			log.Printf("[simulation] stats match=%s: %v", m.id, err)
		}
	}
	return res
}

func (s *Service) run(m *Match) {
	t := time.NewTicker(time.Duration(protocol.TickMillis) * time.Millisecond)
	defer t.Stop()
	for range t.C {
		if res := s.Step(context.Background(), m); res.Ended {
			return
		}
	}
}

func (s *Service) enqueuePush(_ context.Context, p protocol.GatewayPush) error {
	select {
	case s.pushQ <- p:
		return nil
	default:
		return fmt.Errorf("push queue full, dropping %d frames", len(p.Frames))
	}
}

func (s *Service) pushLoop() {
	for p := range s.pushQ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := s.gateway.Post(ctx, "/internal/push", p, nil); err != nil {
			log.Printf("[simulation] gateway push match=%s: %v", p.MatchID, err)
		}
		cancel()
	}
}

// CreateMatch fetches a maze, places everyone and registers the match. The
// tick loop starts immediately unless ManualTick is set.
func (s *Service) CreateMatch(ctx context.Context, req protocol.SimCreateMatchRequest) (*Match, protocol.SimCreateMatchResponse, error) {
	if err := req.Config.Validate(); err != nil {
		return nil, protocol.SimCreateMatchResponse{}, &httpx.Error{Status: http.StatusBadRequest, Code: protocol.ErrBadRequest, Message: err.Error()}
	}
	if len(req.Players) == 0 {
		return nil, protocol.SimCreateMatchResponse{}, &httpx.Error{Status: http.StatusBadRequest, Code: protocol.ErrBadRequest, Message: "players required"}
	}
	dim, _ := protocol.MazeDimension(req.Config.MazeSize)
	var seed int64
	if req.Config.Seed != nil {
		seed = *req.Config.Seed
	}
	var gen protocol.MazeGenerateResponse
	err := s.maze.Post(ctx, "/generate", protocol.MazeGenerateRequest{
		W: dim, H: dim, Seed: seed, Runners: len(req.Players), Catchers: req.Config.Catchers,
	}, &gen)
	if err != nil {
		return nil, protocol.SimCreateMatchResponse{}, &httpx.Error{Status: http.StatusBadGateway, Code: protocol.ErrInternal, Message: "maze: " + err.Error()}
	}
	if len(gen.Maze.Walls) != gen.Maze.W*gen.Maze.H || gen.Maze.W == 0 {
		return nil, protocol.SimCreateMatchResponse{}, &httpx.Error{Status: http.StatusBadGateway, Code: protocol.ErrInternal, Message: "maze: malformed walls"}
	}
	if len(gen.RunnerSpawns) == 0 || len(gen.CatcherSpawns) == 0 {
		return nil, protocol.SimCreateMatchResponse{}, &httpx.Error{Status: http.StatusBadGateway, Code: protocol.ErrInternal, Message: "maze: no spawns"}
	}

	m := &Match{
		id:       httpx.NewRequestID(),
		roomCode: req.RoomCode,
		grid:     gridmath.Grid{M: gen.Maze},
		center:   gridmath.Cell{X: gen.Maze.W / 2, Y: gen.Maze.H / 2}.Center(),
		roundMS:  int64(req.Config.RoundSeconds) * 1000,
	}
	for i, p := range req.Players {
		sp := gen.RunnerSpawns[i%len(gen.RunnerSpawns)]
		m.runners = append(m.runners, &runner{
			id: p.PlayerID, name: p.Name, color: p.Color, isBot: p.IsBot,
			pos: gridmath.Vec{X: sp.X, Y: sp.Y}, want: protocol.DirNone, moving: protocol.DirNone, facing: protocol.DirNone, alive: true,
		})
	}
	speed := protocol.RunnerSpeed * req.Config.CatcherSpeed
	for i := 0; i < req.Config.Catchers; i++ {
		sp := gen.CatcherSpawns[i%len(gen.CatcherSpawns)]
		m.catchers = append(m.catchers, &catcher{
			id: fmt.Sprintf("c%d", i+1), pos: gridmath.Vec{X: sp.X, Y: sp.Y}, dir: protocol.DirNone,
			speed: speed, mover: s.cfg.NewMover(gen.Seed, i),
		})
	}
	m.powerUpRNG = rand.New(rand.NewSource(gen.Seed))
	m.initPowerUps()
	m.start = protocol.MatchStart{T: protocol.TypeMatchStart, MatchID: m.id, Maze: gen.Maze, RoundMS: m.roundMS}
	for _, r := range m.runners {
		m.start.Players = append(m.start.Players, protocol.MatchPlayer{
			PlayerID: r.id, Name: r.name, Role: protocol.RoleRunner, Color: r.color, X: r.pos.X, Y: r.pos.Y, IsBot: r.isBot,
		})
	}
	for _, c := range m.catchers {
		m.start.Catchers = append(m.start.Catchers, protocol.MatchCatcher{ID: c.id, X: c.pos.X, Y: c.pos.Y})
	}
	m.last = m.snapshot(s.cfg.Now().UnixMilli(), m.roundMS, len(m.runners))

	resp := protocol.SimCreateMatchResponse{MatchID: m.id, Starts: map[string]protocol.MatchStart{}}
	for _, r := range m.runners {
		resp.Starts[r.id] = m.startFor(r.id, protocol.RoleRunner)
	}

	s.mu.Lock()
	s.matches[m.id] = m
	s.mu.Unlock()
	if !s.cfg.ManualTick {
		go s.run(m)
	}
	return m, resp, nil
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req protocol.SimCreateMatchRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, err.Error())
		return
	}
	_, resp, err := s.CreateMatch(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	m, ok := s.Match(r.PathValue("id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, protocol.ErrNotFound, "no such match")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m.State())
}

func (s *Service) handleInput(w http.ResponseWriter, r *http.Request) {
	m, ok := s.Match(r.PathValue("id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, protocol.ErrNotFound, "no such match")
		return
	}
	var in protocol.SimInputRequest
	if err := httpx.ReadJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, err.Error())
		return
	}
	if !in.Dir.Valid() {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, "bad dir")
		return
	}
	if !m.Input(in.PlayerID, in.Seq, in.Dir) {
		httpx.WriteError(w, http.StatusNotFound, protocol.ErrNotFound, "player not in match")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(`{"ok":true}`))
}

func (s *Service) handleShoot(w http.ResponseWriter, r *http.Request) {
	m, ok := s.Match(r.PathValue("id"))
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, protocol.ErrNotFound, "no such match")
		return
	}
	var in protocol.SimShootRequest
	if err := httpx.ReadJSON(r, &in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, err.Error())
		return
	}
	if !m.Shoot(in.PlayerID) {
		httpx.WriteError(w, http.StatusNotFound, protocol.ErrNotFound, "player not in match or has no gun")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(`{"ok":true}`))
}

func writeErr(w http.ResponseWriter, err error) {
	if e, ok := err.(*httpx.Error); ok {
		httpx.WriteError(w, e.Status, e.Code, e.Message)
		return
	}
	httpx.WriteError(w, http.StatusInternalServerError, protocol.ErrInternal, err.Error())
}
