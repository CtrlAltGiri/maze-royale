// Package matchmaking owns rooms: join codes, config validation, ready state, start
// criteria, countdown, role and color assignment. Port 8091.
package matchmaking

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// Environment variables that override the peer URLs when no flag is given.
const (
	EnvGatewayURL    = "MM_GATEWAY_URL"
	EnvSimulationURL = "MM_SIMULATION_URL"
)

// MaxPlayers is the hard room capacity (the largest allowed min_players).
const MaxPlayers = protocol.MaxMinPlayers

// CodeLength is the length of a room join code.
const CodeLength = 4

// Palette is the fixed set of readable player colors, assigned in order of
// availability so every player in a room has a distinct one.
var Palette = [8]string{
	"#4fc3f7", // light blue
	"#ff7043", // deep orange
	"#66bb6a", // green
	"#ffca28", // amber
	"#ab47bc", // purple
	"#ec407a", // pink
	"#26c6da", // cyan
	"#d4e157", // lime
}

// Options configures the service. Zero values fall back to the contract
// defaults so NewHandler() works with no configuration.
type Options struct {
	GatewayURL    string
	SimulationURL string
	// Countdown is the total lobby countdown; default protocol.CountdownMS.
	Countdown time.Duration
	// CountdownTick is how often countdown frames are pushed; default 1s.
	CountdownTick time.Duration
	// Logf receives diagnostics; default log.Printf.
	Logf func(format string, args ...any)
}

// DefaultOptions returns the options built from the environment and the
// protocol defaults.
func DefaultOptions() Options {
	o := Options{GatewayURL: protocol.GatewayURL, SimulationURL: protocol.SimulationURL}
	if v := os.Getenv(EnvGatewayURL); v != "" {
		o.GatewayURL = v
	}
	if v := os.Getenv(EnvSimulationURL); v != "" {
		o.SimulationURL = v
	}
	return o
}

func (o Options) withDefaults() Options {
	if o.GatewayURL == "" {
		o.GatewayURL = protocol.GatewayURL
	}
	if o.SimulationURL == "" {
		o.SimulationURL = protocol.SimulationURL
	}
	if o.Countdown <= 0 {
		o.Countdown = protocol.CountdownMS * time.Millisecond
	}
	if o.CountdownTick <= 0 {
		o.CountdownTick = time.Second
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	return o
}

type roomPhase int

const (
	phaseLobby roomPhase = iota
	phaseCountdown
	phaseInMatch
)

type player struct {
	protocol.RoomPlayer
}

type room struct {
	code    string
	config  protocol.RoomConfig
	players []*player // join order
	phase   roomPhase
	// countdownEnd is the wall clock instant the countdown finishes.
	countdownEnd time.Time
	// countdownGen increments whenever a countdown starts or is cancelled so a
	// stale countdown goroutine can notice it was superseded.
	countdownGen int
	matchID      string
	bots         int // bots ever added, for Bot-N naming
}

// Service holds every room and the peer clients.
type Service struct {
	opts Options
	gw   *httpx.Client
	sim  *httpx.Client

	mu    sync.Mutex
	rooms map[string]*room
	// wg tracks countdown goroutines so tests can wait for them.
	wg sync.WaitGroup
}

// NewService creates a matchmaking service with the given options.
func NewService(opts Options) *Service {
	opts = opts.withDefaults()
	return &Service{
		opts:  opts,
		gw:    httpx.NewClient(opts.GatewayURL),
		sim:   httpx.NewClient(opts.SimulationURL),
		rooms: map[string]*room{},
	}
}

// NewHandler returns the service's HTTP handler configured from the
// environment (MM_GATEWAY_URL, MM_SIMULATION_URL) with protocol defaults.
func NewHandler() http.Handler {
	return NewService(DefaultOptions()).Handler()
}

// NewHandlerWithOptions returns a handler for a service built from opts.
func NewHandlerWithOptions(opts Options) http.Handler {
	return NewService(opts).Handler()
}

// Handler returns the HTTP routes of the service.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("matchmaking"))
	mux.HandleFunc("POST /rooms", s.handleCreate)
	mux.HandleFunc("GET /rooms/{code}", s.handleGet)
	mux.HandleFunc("POST /rooms/{code}/join", s.handleJoin)
	mux.HandleFunc("POST /rooms/{code}/leave", s.handleLeave)
	mux.HandleFunc("POST /rooms/{code}/ready", s.handleReady)
	mux.HandleFunc("POST /rooms/{code}/start", s.handleStart)
	mux.HandleFunc("POST /rooms/{code}/end", s.handleEnd)
	return mux
}

// Wait blocks until every running countdown has finished. Intended for tests
// and graceful shutdown.
func (s *Service) Wait() { s.wg.Wait() }

// ---- errors ----

type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }

func errBad(msg string) error { return &apiError{http.StatusBadRequest, protocol.ErrBadRequest, msg} }

var (
	errRoomNotFound = &apiError{http.StatusNotFound, protocol.ErrNotFound, "room not found"}
	errNotInRoom    = &apiError{http.StatusNotFound, protocol.ErrNotInRoom, "player not in room"}
	errRoomFull     = &apiError{http.StatusConflict, protocol.ErrRoomFull, "room is full"}
	errInMatch      = &apiError{http.StatusConflict, protocol.ErrInMatch, "room is already in a match"}
	errForbidden    = &apiError{http.StatusForbidden, protocol.ErrForbidden, "only the creator can start the room"}
)

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		httpx.WriteError(w, ae.status, ae.code, ae.message)
		return
	}
	httpx.WriteError(w, http.StatusInternalServerError, protocol.ErrInternal, err.Error())
}

// ---- handlers ----

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req protocol.MMCreateRoomRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		writeErr(w, errBad(err.Error()))
		return
	}
	resp, err := s.CreateRoom(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, resp)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	resp, err := s.GetRoom(r.PathValue("code"))
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req protocol.MMJoinRoomRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		writeErr(w, errBad(err.Error()))
		return
	}
	resp, err := s.Join(r.PathValue("code"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleLeave(w http.ResponseWriter, r *http.Request) {
	var req protocol.MMPlayerRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		writeErr(w, errBad(err.Error()))
		return
	}
	resp, err := s.Leave(r.PathValue("code"), req.PlayerID)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleReady(w http.ResponseWriter, r *http.Request) {
	var req protocol.MMReadyRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		writeErr(w, errBad(err.Error()))
		return
	}
	resp, err := s.SetReady(r.PathValue("code"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleStart(w http.ResponseWriter, r *http.Request) {
	var req protocol.MMPlayerRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		writeErr(w, errBad(err.Error()))
		return
	}
	resp, err := s.Start(r.PathValue("code"), req.PlayerID)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleEnd(w http.ResponseWriter, r *http.Request) {
	var req protocol.MMMatchEndRequest
	if err := httpx.ReadJSON(r, &req); err != nil {
		writeErr(w, errBad(err.Error()))
		return
	}
	resp, err := s.EndMatch(r.PathValue("code"), req.MatchID)
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// ---- room operations ----

// EndMatch returns a room to the lobby once its match has ended: every human
// is un-readied so the room can be started again. A stale or unknown
// match_id is ignored (the room is returned unchanged).
func (s *Service) EndMatch(code, matchID string) (protocol.MMRoomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, err := s.roomLocked(code)
	if err != nil {
		return protocol.MMRoomResponse{}, err
	}
	if rm.phase == phaseInMatch && (matchID == "" || rm.matchID == "" || rm.matchID == matchID) {
		rm.phase = phaseLobby
		rm.matchID = ""
		rm.countdownEnd = time.Time{}
		for _, p := range rm.players {
			if !p.IsBot {
				p.Ready = false
			}
		}
	}
	return rm.response(s.now()), nil
}

// CreateRoom validates the config, mints a code and seats the creator.
func (s *Service) CreateRoom(req protocol.MMCreateRoomRequest) (protocol.MMRoomResponse, error) {
	if strings.TrimSpace(req.PlayerID) == "" {
		return protocol.MMRoomResponse{}, errBad("player_id is required")
	}
	cfg := req.Config
	if cfg == (protocol.RoomConfig{}) {
		cfg = protocol.DefaultRoomConfig()
	}
	if err := cfg.Validate(); err != nil {
		return protocol.MMRoomResponse{}, errBad(err.Error())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	code, err := s.newCodeLocked()
	if err != nil {
		return protocol.MMRoomResponse{}, err
	}
	rm := &room{code: code, config: cfg}
	rm.players = append(rm.players, &player{protocol.RoomPlayer{
		PlayerID:  req.PlayerID,
		Name:      displayName(req.Name, req.PlayerID),
		Color:     rm.nextColor(),
		IsCreator: true,
	}})
	s.rooms[code] = rm
	return rm.response(s.now()), nil
}

// GetRoom returns the current state of a room.
func (s *Service) GetRoom(code string) (protocol.MMRoomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, err := s.roomLocked(code)
	if err != nil {
		return protocol.MMRoomResponse{}, err
	}
	return rm.response(s.now()), nil
}

// Join seats a player in a room. Re-joining with the same player id is
// idempotent and returns the current state.
func (s *Service) Join(code string, req protocol.MMJoinRoomRequest) (protocol.MMRoomResponse, error) {
	if strings.TrimSpace(req.PlayerID) == "" {
		return protocol.MMRoomResponse{}, errBad("player_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, err := s.roomLocked(code)
	if err != nil {
		return protocol.MMRoomResponse{}, err
	}
	if rm.find(req.PlayerID) != nil {
		return rm.response(s.now()), nil
	}
	if rm.phase == phaseInMatch {
		return protocol.MMRoomResponse{}, errInMatch
	}
	if len(rm.players) >= MaxPlayers {
		return protocol.MMRoomResponse{}, errRoomFull
	}
	rm.players = append(rm.players, &player{protocol.RoomPlayer{
		PlayerID: req.PlayerID,
		Name:     displayName(req.Name, req.PlayerID),
		Color:    rm.nextColor(),
	}})
	return rm.response(s.now()), nil
}

// Leave removes a player. The oldest remaining human inherits creator
// privileges; an empty room is deleted. Recipients still include the leaver
// so the gateway can tell them the room they left.
func (s *Service) Leave(code, playerID string) (protocol.MMRoomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, err := s.roomLocked(code)
	if err != nil {
		return protocol.MMRoomResponse{}, err
	}
	idx := rm.index(playerID)
	if idx < 0 {
		return protocol.MMRoomResponse{}, errNotInRoom
	}
	leaver := rm.players[idx]
	rm.players = append(rm.players[:idx], rm.players[idx+1:]...)
	if leaver.IsCreator {
		for _, p := range rm.players {
			if !p.IsBot {
				p.IsCreator = true
				break
			}
		}
	}
	if rm.humanCount() == 0 {
		delete(s.rooms, code)
		rm.countdownGen++
		rm.phase = phaseLobby
		rm.players = nil
	} else if rm.phase == phaseCountdown && len(rm.players) < protocol.MinMinPlayers {
		s.cancelCountdownLocked(rm)
	}
	resp := rm.response(s.now())
	resp.Recipients = append(resp.Recipients, playerID)
	return resp, nil
}

// SetReady flips a player's ready flag and auto-starts the room when every
// seated player is ready and min_players is reached.
func (s *Service) SetReady(code string, req protocol.MMReadyRequest) (protocol.MMRoomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, err := s.roomLocked(code)
	if err != nil {
		return protocol.MMRoomResponse{}, err
	}
	p := rm.find(req.PlayerID)
	if p == nil {
		return protocol.MMRoomResponse{}, errNotInRoom
	}
	if rm.phase == phaseInMatch {
		return protocol.MMRoomResponse{}, errInMatch
	}
	p.Ready = req.Ready
	if rm.phase == phaseLobby && rm.allReady() {
		s.beginCountdownLocked(rm)
	}
	return rm.response(s.now()), nil
}

// Start lets the creator force the countdown with at least two players.
func (s *Service) Start(code, playerID string) (protocol.MMRoomResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, err := s.roomLocked(code)
	if err != nil {
		return protocol.MMRoomResponse{}, err
	}
	p := rm.find(playerID)
	if p == nil {
		return protocol.MMRoomResponse{}, errNotInRoom
	}
	if !p.IsCreator {
		return protocol.MMRoomResponse{}, errForbidden
	}
	switch rm.phase {
	case phaseInMatch:
		return protocol.MMRoomResponse{}, errInMatch
	case phaseCountdown:
		return rm.response(s.now()), nil
	}
	if !rm.config.FillBots && len(rm.players) < protocol.MinMinPlayers {
		return protocol.MMRoomResponse{}, errBad(fmt.Sprintf("need at least %d players to start", protocol.MinMinPlayers))
	}
	s.beginCountdownLocked(rm)
	return rm.response(s.now()), nil
}

// ---- start criteria, bots and countdown ----

func (r *room) allReady() bool {
	if len(r.players) < r.config.MinPlayers {
		return false
	}
	for _, p := range r.players {
		if !p.Ready {
			return false
		}
	}
	return true
}

// fillBotsLocked seats bots until min_players is reached and returns the
// bot_joined events to push.
func (r *room) fillBotsLocked() []protocol.Event {
	var events []protocol.Event
	for len(r.players) < r.config.MinPlayers && len(r.players) < MaxPlayers {
		r.bots++
		b := &player{protocol.RoomPlayer{
			PlayerID: fmt.Sprintf("bot-%s-%d", strings.ToLower(r.code), r.bots),
			Name:     fmt.Sprintf("Bot-%d", r.bots),
			Color:    r.nextColor(),
			Ready:    true,
			IsBot:    true,
		}}
		r.players = append(r.players, b)
		events = append(events, protocol.Event{T: protocol.TypeEvent, Kind: protocol.EventBotJoined, PlayerID: b.PlayerID, Name: b.Name})
	}
	return events
}

func (s *Service) beginCountdownLocked(rm *room) {
	var botEvents []protocol.Event
	if rm.config.FillBots {
		botEvents = rm.fillBotsLocked()
	}
	rm.phase = phaseCountdown
	rm.countdownGen++
	gen := rm.countdownGen
	rm.countdownEnd = s.now().Add(s.opts.Countdown)

	frames := make([]any, 0, len(botEvents)+2)
	for _, e := range botEvents {
		frames = append(frames, e)
	}
	frames = append(frames, protocol.Event{T: protocol.TypeEvent, Kind: protocol.EventCountdown, MS: s.opts.Countdown.Milliseconds()})
	frames = append(frames, rm.state(s.now()))
	code := rm.code

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.push(code, "", nil, frames...)
		s.runCountdown(rm, gen)
	}()
}

func (s *Service) cancelCountdownLocked(rm *room) {
	rm.phase = phaseLobby
	rm.countdownGen++
	rm.countdownEnd = time.Time{}
}

func (s *Service) runCountdown(rm *room, gen int) {
	ticker := time.NewTicker(s.opts.CountdownTick)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		if rm.countdownGen != gen || rm.phase != phaseCountdown {
			s.mu.Unlock()
			return
		}
		now := s.now()
		remaining := rm.countdownEnd.Sub(now)
		if remaining > 0 {
			frames := []any{
				protocol.Event{T: protocol.TypeEvent, Kind: protocol.EventCountdown, MS: remaining.Milliseconds()},
				rm.state(now),
			}
			s.mu.Unlock()
			s.push(rm.code, "", nil, frames...)
			continue
		}
		req := protocol.SimCreateMatchRequest{RoomCode: rm.code, Config: rm.config, Players: rm.roster()}
		rm.phase = phaseInMatch
		rm.countdownEnd = time.Time{}
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var res protocol.SimCreateMatchResponse
		err := s.sim.Post(ctx, "/matches", req, &res)
		cancel()
		if err != nil {
			s.opts.Logf("[matchmaking] room %s: simulation POST /matches failed: %v", rm.code, err)
			s.mu.Lock()
			if rm.countdownGen == gen {
				rm.phase = phaseLobby
				for _, p := range rm.players {
					if !p.IsBot {
						p.Ready = false
					}
				}
			}
			state := rm.state(s.now())
			s.mu.Unlock()
			s.push(rm.code, "", nil,
				protocol.NewError(protocol.ErrInternal, "could not start match: "+err.Error()),
				state)
			return
		}
		s.mu.Lock()
		rm.matchID = res.MatchID
		roster := rm.roster()
		s.mu.Unlock()

		// Every human gets its own match_start so the you block is correct.
		for _, p := range roster {
			if p.IsBot {
				continue
			}
			start, ok := res.Starts[p.PlayerID]
			if !ok {
				s.opts.Logf("[matchmaking] room %s: simulation returned no match_start for %s", rm.code, p.PlayerID)
				continue
			}
			if start.T == "" {
				start.T = protocol.TypeMatchStart
			}
			s.push(rm.code, res.MatchID, []string{p.PlayerID}, start)
		}
		return
	}
}

// push encodes frames and delivers them to the gateway; failures are logged,
// never fatal, because the lobby state stays authoritative here.
func (s *Service) push(roomCode, matchID string, playerIDs []string, frames ...any) {
	gp := protocol.GatewayPush{RoomCode: roomCode, MatchID: matchID, PlayerIDs: playerIDs, Frames: make([]json.RawMessage, 0, len(frames))}
	for _, f := range frames {
		raw, err := protocol.Encode(f)
		if err != nil {
			s.opts.Logf("[matchmaking] encode frame: %v", err)
			continue
		}
		gp.Frames = append(gp.Frames, raw)
	}
	if len(gp.Frames) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.gw.Post(ctx, "/internal/push", gp, nil); err != nil {
		s.opts.Logf("[matchmaking] room %s: gateway push failed: %v", roomCode, err)
	}
}

// ---- helpers ----

func (s *Service) now() time.Time { return time.Now() }

func (s *Service) roomLocked(code string) (*room, error) {
	rm, ok := s.rooms[normalizeCode(code)]
	if !ok {
		return nil, errRoomNotFound
	}
	return rm, nil
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ" // no I or O: they read as digits

func (s *Service) newCodeLocked() (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		var b strings.Builder
		for i := 0; i < CodeLength; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
			if err != nil {
				return "", fmt.Errorf("matchmaking: random code: %w", err)
			}
			b.WriteByte(codeAlphabet[n.Int64()])
		}
		code := b.String()
		if _, taken := s.rooms[code]; !taken {
			return code, nil
		}
	}
	return "", &apiError{http.StatusServiceUnavailable, protocol.ErrInternal, "no free room codes"}
}

func normalizeCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

func displayName(name, playerID string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		if len(playerID) > 6 {
			return "Player-" + playerID[:6]
		}
		return "Player-" + playerID
	}
	if len(name) > 24 {
		name = name[:24]
	}
	return name
}

func (r *room) find(playerID string) *player {
	if i := r.index(playerID); i >= 0 {
		return r.players[i]
	}
	return nil
}

func (r *room) index(playerID string) int {
	for i, p := range r.players {
		if p.PlayerID == playerID {
			return i
		}
	}
	return -1
}

func (r *room) humanCount() int {
	n := 0
	for _, p := range r.players {
		if !p.IsBot {
			n++
		}
	}
	return n
}

// nextColor returns the first palette color not used by a seated player.
func (r *room) nextColor() string {
	used := map[string]bool{}
	for _, p := range r.players {
		used[p.Color] = true
	}
	for _, c := range Palette {
		if !used[c] {
			return c
		}
	}
	return Palette[len(r.players)%len(Palette)]
}

func (r *room) roster() []protocol.RoomPlayer {
	out := make([]protocol.RoomPlayer, len(r.players))
	for i, p := range r.players {
		out[i] = p.RoomPlayer
	}
	return out
}

func (r *room) state(now time.Time) protocol.RoomState {
	var cd int64
	if r.phase == phaseCountdown {
		cd = r.countdownEnd.Sub(now).Milliseconds()
		if cd < 0 {
			cd = 0
		}
	}
	return protocol.RoomState{T: protocol.TypeRoomState, Code: r.code, Config: r.config, Players: r.roster(), CountdownMS: cd}
}

func (r *room) response(now time.Time) protocol.MMRoomResponse {
	recipients := make([]string, 0, len(r.players))
	for _, p := range r.players {
		if !p.IsBot {
			recipients = append(recipients, p.PlayerID)
		}
	}
	sort.Strings(recipients)
	return protocol.MMRoomResponse{Room: r.state(now), Recipients: recipients}
}
