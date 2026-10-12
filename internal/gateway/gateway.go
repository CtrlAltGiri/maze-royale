// Package gateway serves the static client, terminates player websockets at /ws,
// routes intents to matchmaking and simulation, and fans snapshots out per
// room. Port 8090.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// AcceptOptions is the websocket accept configuration for /ws. Localhost only,
// so origin checking is disabled.
var AcceptOptions = &websocket.AcceptOptions{InsecureSkipVerify: true}

// Config holds the peer base URLs and the static file directory. Zero values
// fall back to the contract defaults (overridable through the environment
// variables MATCHMAKING_URL, SIMULATION_URL, STATS_URL and WEB_DIR).
type Config struct {
	MatchmakingURL string
	SimulationURL  string
	StatsURL       string
	WebDir         string
}

// ConfigFromEnv returns the default config with environment overrides applied.
func ConfigFromEnv() Config {
	return Config{
		MatchmakingURL: envOr("MATCHMAKING_URL", protocol.MatchmakingURL),
		SimulationURL:  envOr("SIMULATION_URL", protocol.SimulationURL),
		StatsURL:       envOr("STATS_URL", protocol.StatsURL),
		WebDir:         envOr("WEB_DIR", "web"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c Config) withDefaults() Config {
	d := ConfigFromEnv()
	if c.MatchmakingURL == "" {
		c.MatchmakingURL = d.MatchmakingURL
	}
	if c.SimulationURL == "" {
		c.SimulationURL = d.SimulationURL
	}
	if c.StatsURL == "" {
		c.StatsURL = d.StatsURL
	}
	if c.WebDir == "" {
		c.WebDir = d.WebDir
	}
	return c
}

// NewHandler returns the service's HTTP handler using ConfigFromEnv.
func NewHandler() http.Handler {
	return NewHandlerWithConfig(ConfigFromEnv())
}

// NewHandlerWithConfig returns the service's HTTP handler for cfg.
func NewHandlerWithConfig(cfg Config) http.Handler {
	return NewServer(cfg).Handler()
}

const writeTimeout = 5 * time.Second

// disconnectGrace is how long a room seat is held for a player whose socket
// dropped before the gateway removes them from the room.
const disconnectGrace = 10 * time.Second

// Server is the gateway state: live connections, room membership and the
// room -> match mapping learned from /internal/push.
type Server struct {
	cfg  Config
	mm   *httpx.Client
	sim  *httpx.Client
	stat *httpx.Client

	mu    sync.Mutex
	conns map[string]*conn  // player_id -> live connection
	rooms map[string]*room  // room_code -> room
	where map[string]string // player_id -> room_code (kept across disconnects)
}

type room struct {
	members map[string]bool
	matchID string
	// spectators are members who joined while matchID was set; they receive
	// frames but their input is never forwarded to the simulation.
	spectators map[string]bool
}

func newRoom() *room {
	return &room{members: map[string]bool{}, spectators: map[string]bool{}}
}

type conn struct {
	ws       *websocket.Conn
	playerID string
	name     string
	wmu      sync.Mutex
}

// NewServer builds a Server for cfg.
func NewServer(cfg Config) *Server {
	cfg = cfg.withDefaults()
	return &Server{
		cfg:   cfg,
		mm:    httpx.NewClient(cfg.MatchmakingURL),
		sim:   httpx.NewClient(cfg.SimulationURL),
		stat:  httpx.NewClient(cfg.StatsURL),
		conns: map[string]*conn{},
		rooms: map[string]*room{},
		where: map[string]string{},
	}
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("gateway"))
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("POST /internal/push", s.handlePush)
	mux.HandleFunc("GET /leaderboard", s.handleLeaderboard)
	mux.Handle("/", http.FileServer(http.Dir(s.cfg.WebDir)))
	return mux
}

// ---- websocket ----

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, AcceptOptions)
	if err != nil {
		log.Printf("[gateway] ws accept: %v", err)
		return
	}
	c := &conn{ws: ws}
	defer s.dropConn(c)

	ctx := r.Context()
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			c.sendError(ctx, protocol.ErrBadRequest, "frames must be JSON text")
			continue
		}
		s.dispatch(ctx, c, data)
	}
}

func (s *Server) dispatch(ctx context.Context, c *conn, raw []byte) {
	msg, err := protocol.Decode(raw)
	if err != nil {
		c.sendError(ctx, protocol.ErrBadRequest, err.Error())
		return
	}
	if p, ok := msg.(*protocol.Ping); ok {
		c.send(ctx, protocol.Pong{T: protocol.TypePong, TS: p.TS})
		return
	}
	if _, ok := msg.(*protocol.Hello); !ok && c.playerID == "" {
		c.sendError(ctx, protocol.ErrBadRequest, "send hello first")
		return
	}
	switch m := msg.(type) {
	case *protocol.Hello:
		s.onHello(ctx, c, m)
	case *protocol.CreateRoom:
		var res protocol.MMRoomResponse
		err := s.mm.Post(ctx, "/rooms", protocol.MMCreateRoomRequest{PlayerID: c.playerID, Name: c.name, Config: m.Config}, &res)
		s.finishRoomMutation(ctx, c, &res, err)
	case *protocol.JoinRoom:
		s.onJoin(ctx, c, m.Code)
	case *protocol.LeaveRoom:
		code, ok := s.roomOf(c.playerID)
		if !ok {
			c.sendError(ctx, protocol.ErrNotInRoom, "not in a room")
			return
		}
		var res protocol.MMRoomResponse
		err := s.mm.Post(ctx, "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: c.playerID}, &res)
		s.forget(c.playerID)
		s.finishRoomMutation(ctx, c, &res, err)
	case *protocol.SetReady:
		code, ok := s.roomOf(c.playerID)
		if !ok {
			c.sendError(ctx, protocol.ErrNotInRoom, "not in a room")
			return
		}
		var res protocol.MMRoomResponse
		err := s.mm.Post(ctx, "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: c.playerID, Ready: m.Ready}, &res)
		s.finishRoomMutation(ctx, c, &res, err)
	case *protocol.StartRoom:
		code, ok := s.roomOf(c.playerID)
		if !ok {
			c.sendError(ctx, protocol.ErrNotInRoom, "not in a room")
			return
		}
		var res protocol.MMRoomResponse
		err := s.mm.Post(ctx, "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: c.playerID}, &res)
		s.finishRoomMutation(ctx, c, &res, err)
	case *protocol.Input:
		s.onInput(ctx, c, m)
	case *protocol.Shoot:
		s.onShoot(ctx, c, m)
	default:
		c.sendError(ctx, protocol.ErrUnsupported, fmt.Sprintf("unexpected client frame %T", msg))
	}
}

func (s *Server) onHello(ctx context.Context, c *conn, m *protocol.Hello) {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = "player"
	}
	s.mu.Lock()
	if c.playerID == "" {
		c.playerID = httpx.NewRequestID()
		s.conns[c.playerID] = c
	}
	c.name = name
	s.mu.Unlock()
	c.send(ctx, protocol.Welcome{T: protocol.TypeWelcome, PlayerID: c.playerID})
}

func (s *Server) onJoin(ctx context.Context, c *conn, code string) {
	code = strings.TrimSpace(code)
	if code == "" {
		c.sendError(ctx, protocol.ErrBadRequest, "code is required")
		return
	}
	if matchID := s.matchOf(code); matchID != "" {
		s.joinAsSpectator(ctx, c, code, matchID)
		return
	}
	var res protocol.MMRoomResponse
	err := s.mm.Post(ctx, "/rooms/"+code+"/join", protocol.MMJoinRoomRequest{PlayerID: c.playerID, Name: c.name}, &res)
	s.finishRoomMutation(ctx, c, &res, err)
}

// joinAsSpectator handles a (re)connect into a room whose match is running:
// the client gets a spectator match_start plus the latest snapshot. The
// simulation is only read, never told about the spectator, so the tick loop
// and the runners' state are untouched.
//
// Membership is recorded and the two frames written while holding the
// connection's write lock, so a live snapshot fanned out concurrently can
// never land in front of the (older) snapshot fetched here.
func (s *Server) joinAsSpectator(ctx context.Context, c *conn, code, matchID string) {
	var st protocol.SimMatchStateResponse
	if err := s.sim.Get(ctx, "/matches/"+matchID, &st); err != nil {
		c.sendPeerError(ctx, err)
		return
	}
	if st.Ended {
		s.mu.Lock()
		if r := s.rooms[code]; r != nil && r.matchID == matchID {
			r.matchID = ""
		}
		s.mu.Unlock()
		c.sendError(ctx, protocol.ErrNotFound, "match already ended, join again")
		return
	}

	start := st.Start
	start.T = protocol.TypeMatchStart
	if start.MatchID == "" {
		start.MatchID = matchID
	}
	start.You = protocol.You{PlayerID: c.playerID, Role: protocol.RoleSpectator}
	snap := st.Snapshot
	snap.T = protocol.TypeSnapshot
	startData, err := protocol.Encode(start)
	if err != nil {
		log.Printf("[gateway] encode: %v", err)
		return
	}
	snapData, err := protocol.Encode(snap)
	if err != nil {
		log.Printf("[gateway] encode: %v", err)
		return
	}

	c.wmu.Lock()
	defer c.wmu.Unlock()
	s.mu.Lock()
	if r := s.rooms[code]; r == nil || r.matchID != matchID {
		s.mu.Unlock()
		c.writeLocked(ctx, encodeOrLog(protocol.NewError(protocol.ErrNotFound, "match already ended, join again")))
		return
	}
	s.place(c.playerID, code)
	s.rooms[code].spectators[c.playerID] = true
	s.mu.Unlock()
	c.writeLocked(ctx, startData)
	c.writeLocked(ctx, snapData)
}

func (s *Server) onInput(ctx context.Context, c *conn, m *protocol.Input) {
	if !m.Dir.Valid() {
		c.sendError(ctx, protocol.ErrBadRequest, "bad dir")
		return
	}
	code, ok := s.roomOf(c.playerID)
	if !ok {
		c.sendError(ctx, protocol.ErrNotInRoom, "not in a room")
		return
	}
	matchID, spectator := s.matchAndRole(code, c.playerID)
	if matchID == "" {
		c.sendError(ctx, protocol.ErrBadRequest, "room is not in a match")
		return
	}
	if spectator {
		c.sendError(ctx, protocol.ErrForbidden, "spectators cannot send input")
		return
	}
	req := protocol.SimInputRequest{PlayerID: c.playerID, Seq: m.Seq, Dir: m.Dir}
	rid := httpx.RequestIDFrom(ctx)
	if rid == "" {
		rid = httpx.NewRequestID()
	}
	go func() {
		bctx, cancel := context.WithTimeout(httpx.WithRequestID(context.Background(), rid), writeTimeout)
		defer cancel()
		if err := s.sim.Post(bctx, "/matches/"+matchID+"/input", req, nil); err != nil {
			log.Printf("[gateway] rid=%s input %s: %v", rid, c.playerID, err)
		}
	}()
}

func (s *Server) onShoot(ctx context.Context, c *conn, _ *protocol.Shoot) {
	code, ok := s.roomOf(c.playerID)
	if !ok {
		c.sendError(ctx, protocol.ErrNotInRoom, "not in a room")
		return
	}
	matchID, spectator := s.matchAndRole(code, c.playerID)
	if matchID == "" {
		c.sendError(ctx, protocol.ErrBadRequest, "room is not in a match")
		return
	}
	if spectator {
		c.sendError(ctx, protocol.ErrForbidden, "spectators cannot send input")
		return
	}
	req := protocol.SimShootRequest{PlayerID: c.playerID}
	rid := httpx.RequestIDFrom(ctx)
	if rid == "" {
		rid = httpx.NewRequestID()
	}
	go func() {
		bctx, cancel := context.WithTimeout(httpx.WithRequestID(context.Background(), rid), writeTimeout)
		defer cancel()
		if err := s.sim.Post(bctx, "/matches/"+matchID+"/shoot", req, nil); err != nil {
			log.Printf("[gateway] rid=%s shoot %s: %v", rid, c.playerID, err)
		}
	}()
}

// finishRoomMutation sends the peer error (if any) to c, otherwise records
// membership from the returned room and fans room_state to the recipients.
func (s *Server) finishRoomMutation(ctx context.Context, c *conn, res *protocol.MMRoomResponse, err error) {
	if err != nil {
		c.sendPeerError(ctx, err)
		return
	}
	res.Room.T = protocol.TypeRoomState
	s.recordRoom(res.Room)
	s.fanout(ctx, res.Room.Code, res.Recipients, res.Room)
}

// recordRoom resyncs the gateway's membership view with the roster returned by
// matchmaking. Players no longer on the roster are unlinked from the room.
func (s *Server) recordRoom(rs protocol.RoomState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[rs.Code]
	if r == nil {
		r = newRoom()
		s.rooms[rs.Code] = r
	}
	seen := map[string]bool{}
	for _, p := range rs.Players {
		seen[p.PlayerID] = true
		s.place(p.PlayerID, rs.Code)
	}
	for id := range r.members {
		if !seen[id] && r.matchID == "" {
			delete(r.members, id)
			delete(r.spectators, id)
			if s.where[id] == rs.Code {
				delete(s.where, id)
			}
		}
	}
	if len(r.members) == 0 {
		delete(s.rooms, rs.Code)
	}
}

// place links player to code (caller holds s.mu).
func (s *Server) place(playerID, code string) {
	if old, ok := s.where[playerID]; ok && old != code {
		if r := s.rooms[old]; r != nil {
			delete(r.members, playerID)
			delete(r.spectators, playerID)
			if len(r.members) == 0 {
				delete(s.rooms, old)
			}
		}
	}
	r := s.rooms[code]
	if r == nil {
		r = newRoom()
		s.rooms[code] = r
	}
	r.members[playerID] = true
	s.where[playerID] = code
}

func (s *Server) forget(playerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code, ok := s.where[playerID]
	if !ok {
		return
	}
	delete(s.where, playerID)
	if r := s.rooms[code]; r != nil {
		delete(r.members, playerID)
		delete(r.spectators, playerID)
		if len(r.members) == 0 {
			delete(s.rooms, code)
		}
	}
}

func (s *Server) roomOf(playerID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code, ok := s.where[playerID]
	return code, ok
}

func (s *Server) matchOf(code string) string {
	id, _ := s.matchAndRole(code, "")
	return id
}

// matchAndRole returns the room's current match id and whether playerID is
// spectating it.
func (s *Server) matchAndRole(code, playerID string) (matchID string, spectator bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.rooms[code]; r != nil {
		return r.matchID, r.spectators[playerID]
	}
	return "", false
}

func (s *Server) dropConn(c *conn) {
	s.mu.Lock()
	dropped := c.playerID != "" && s.conns[c.playerID] == c
	if dropped {
		delete(s.conns, c.playerID)
	}
	_, inRoom := s.where[c.playerID]
	s.mu.Unlock()
	_ = c.ws.CloseNow()
	if dropped && inRoom {
		time.AfterFunc(disconnectGrace, func() { s.evictIfGone(c.playerID) })
	}
}

// evictIfGone removes playerID from its room unless a live connection for the
// same player id has appeared in the meantime.
func (s *Server) evictIfGone(playerID string) {
	s.mu.Lock()
	_, live := s.conns[playerID]
	code, inRoom := s.where[playerID]
	spectator := inRoom && s.rooms[code] != nil && s.rooms[code].spectators[playerID]
	s.mu.Unlock()
	if live || !inRoom {
		return
	}
	if spectator {
		// Spectators are never on the matchmaking roster.
		s.forget(playerID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	var res protocol.MMRoomResponse
	err := s.mm.Post(ctx, "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: playerID}, &res)
	s.forget(playerID)
	if err != nil {
		log.Printf("[gateway] room %s: evict %s: %v", code, playerID, err)
		return
	}
	res.Room.T = protocol.TypeRoomState
	s.recordRoom(res.Room)
	s.fanout(ctx, res.Room.Code, res.Recipients, res.Room)
}

// targets returns the live connections for the given player ids, or for every
// member of the room when ids is empty.
func (s *Server) targets(code string, ids []string) []*conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*conn
	if len(ids) == 0 {
		r := s.rooms[code]
		if r == nil {
			return nil
		}
		for id := range r.members {
			if c := s.conns[id]; c != nil {
				out = append(out, c)
			}
		}
		return out
	}
	for _, id := range ids {
		if c := s.conns[id]; c != nil {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) fanout(ctx context.Context, code string, ids []string, msg any) {
	data, err := protocol.Encode(msg)
	if err != nil {
		log.Printf("[gateway] encode: %v", err)
		return
	}
	for _, c := range s.targets(code, ids) {
		c.write(ctx, data)
	}
}

// ---- POST /internal/push ----

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	var push protocol.GatewayPush
	if err := httpx.ReadJSON(r, &push); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, err.Error())
		return
	}
	if push.RoomCode == "" {
		httpx.WriteError(w, http.StatusBadRequest, protocol.ErrBadRequest, "room_code is required")
		return
	}
	ended := false
	for _, f := range push.Frames {
		var env protocol.Envelope
		if json.Unmarshal(f, &env) == nil && env.T == protocol.TypeMatchEnd {
			ended = true
		}
	}
	s.mu.Lock()
	rm := s.rooms[push.RoomCode]
	if rm == nil {
		rm = newRoom()
		s.rooms[push.RoomCode] = rm
	}
	if push.MatchID != "" && rm.matchID == "" {
		rm.matchID = push.MatchID
		rm.spectators = map[string]bool{}
	}
	if ended {
		rm.matchID = ""
		rm.spectators = map[string]bool{}
	}
	s.mu.Unlock()

	conns := s.targets(push.RoomCode, push.PlayerIDs)
	for _, c := range conns {
		for _, f := range push.Frames {
			c.write(r.Context(), f)
		}
	}
	if ended {
		go s.endRoomMatch(push.RoomCode, push.MatchID)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"delivered": len(conns)})
}

// endRoomMatch tells matchmaking the room's match is over and fans the
// resulting lobby room_state so clients can ready up again.
func (s *Server) endRoomMatch(code, matchID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res protocol.MMRoomResponse
	if err := s.mm.Post(ctx, "/rooms/"+code+"/end", protocol.MMMatchEndRequest{MatchID: matchID}, &res); err != nil {
		log.Printf("[gateway] room %s: matchmaking end: %v", code, err)
		return
	}
	res.Room.T = protocol.TypeRoomState
	s.recordRoom(res.Room)
	s.fanout(ctx, res.Room.Code, res.Recipients, res.Room)
}

// ---- GET /leaderboard ----

var leaderboardTmpl = template.Must(template.New("lb").Funcs(template.FuncMap{
	"add": func(a ...int) int {
		n := 0
		for _, v := range a {
			n += v
		}
		return n
	},
	"secs": func(ms int64) string { return fmt.Sprintf("%.1f", float64(ms)/1000) },
}).Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Maze Royale leaderboard</title>
<style>body{font-family:sans-serif;margin:2em}table{border-collapse:collapse}td,th{border:1px solid #999;padding:.3em .8em;text-align:left}</style>
</head><body>
<h1>Leaderboard</h1>
<table><thead><tr><th>#</th><th>Name</th><th>Wins</th><th>Rounds</th><th>Total survived (s)</th></tr></thead><tbody>
{{range $i, $e := .Entries}}<tr><td>{{add $i $.Offset 1}}</td><td>{{$e.Name}}</td><td>{{$e.Wins}}</td><td>{{$e.Rounds}}</td><td>{{secs $e.TotalSurvive}}</td></tr>
{{else}}<tr><td colspan="5">No results yet.</td></tr>{{end}}
</tbody></table>
<p>{{.Total}} player(s) total. <a href="/">Back to game</a></p>
</body></html>
`))

type leaderboardView struct {
	protocol.LeaderboardResponse
	Offset int
}

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	path := "/leaderboard"
	if q.Has("limit") || q.Has("offset") {
		path += "?" + q.Encode()
	}
	var lb protocol.LeaderboardResponse
	if err := s.stat.Get(r.Context(), path, &lb); err != nil {
		status := httpx.StatusOf(err)
		if status == 0 {
			status = http.StatusBadGateway
		}
		httpx.WriteError(w, status, protocol.ErrInternal, err.Error())
		return
	}
	offset := 0
	fmt.Sscanf(q.Get("offset"), "%d", &offset)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := leaderboardTmpl.Execute(w, leaderboardView{LeaderboardResponse: lb, Offset: offset}); err != nil {
		log.Printf("[gateway] leaderboard render: %v", err)
	}
}

// ---- conn helpers ----

func (c *conn) send(ctx context.Context, msg any) {
	data, err := protocol.Encode(msg)
	if err != nil {
		log.Printf("[gateway] encode: %v", err)
		return
	}
	c.write(ctx, data)
}

func (c *conn) sendError(ctx context.Context, code, message string) {
	c.send(ctx, protocol.NewError(code, message))
}

// sendPeerError converts a peer HTTP failure into an error frame.
func (c *conn) sendPeerError(ctx context.Context, err error) {
	var he *httpx.Error
	if errors.As(err, &he) {
		c.sendError(ctx, he.Code, he.Message)
		return
	}
	c.sendError(ctx, protocol.ErrInternal, err.Error())
}

func encodeOrLog(msg any) []byte {
	data, err := protocol.Encode(msg)
	if err != nil {
		log.Printf("[gateway] encode: %v", err)
	}
	return data
}

func (c *conn) write(ctx context.Context, data []byte) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.writeLocked(ctx, data)
}

// writeLocked writes one frame; the caller holds c.wmu.
func (c *conn) writeLocked(ctx context.Context, data []byte) {
	if data == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if err := c.ws.Write(wctx, websocket.MessageText, data); err != nil {
		_ = c.ws.CloseNow()
	}
}
