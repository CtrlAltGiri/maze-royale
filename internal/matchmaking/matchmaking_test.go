package matchmaking

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// fakePeers records gateway pushes and answers simulation POST /matches.
type fakePeers struct {
	mu      sync.Mutex
	pushes  []protocol.GatewayPush
	matches []protocol.SimCreateMatchRequest
	simFail bool
}

func (f *fakePeers) gateway() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/push" {
			http.NotFound(w, r)
			return
		}
		var gp protocol.GatewayPush
		if err := json.NewDecoder(r.Body).Decode(&gp); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f.mu.Lock()
		f.pushes = append(f.pushes, gp)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
}

func (f *fakePeers) simulation() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/matches" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req protocol.SimCreateMatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f.mu.Lock()
		f.matches = append(f.matches, req)
		fail := f.simFail
		f.mu.Unlock()
		if fail {
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(protocol.HTTPError{Code: protocol.ErrInternal, Message: "boom"})
			return
		}
		res := protocol.SimCreateMatchResponse{MatchID: "m-" + req.RoomCode, Starts: map[string]protocol.MatchStart{}}
		for _, p := range req.Players {
			res.Starts[p.PlayerID] = protocol.MatchStart{
				T: protocol.TypeMatchStart, MatchID: res.MatchID,
				You:     protocol.You{PlayerID: p.PlayerID, Role: protocol.RoleRunner, Color: p.Color},
				RoundMS: int64(req.Config.RoundSeconds) * 1000,
			}
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
}

func (f *fakePeers) snapshot() ([]protocol.GatewayPush, []protocol.SimCreateMatchRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.GatewayPush(nil), f.pushes...), append([]protocol.SimCreateMatchRequest(nil), f.matches...)
}

type env struct {
	t     *testing.T
	svc   *Service
	srv   *httptest.Server
	peers *fakePeers
}

func newEnv(t *testing.T) *env {
	t.Helper()
	peers := &fakePeers{}
	gw, sim := peers.gateway(), peers.simulation()
	svc := NewService(Options{
		GatewayURL:    gw.URL,
		SimulationURL: sim.URL,
		Countdown:     120 * time.Millisecond,
		CountdownTick: 30 * time.Millisecond,
		Logf:          t.Logf,
	})
	srv := httptest.NewServer(svc.Handler())
	// Cleanups run LIFO: wait for countdown goroutines before closing peers.
	t.Cleanup(srv.Close)
	t.Cleanup(gw.Close)
	t.Cleanup(sim.Close)
	t.Cleanup(svc.Wait)
	return &env{t: t, svc: svc, srv: srv, peers: peers}
}

func (e *env) do(method, path string, body any) (int, protocol.MMRoomResponse, protocol.HTTPError) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	var raw json.RawMessage
	_ = json.NewDecoder(res.Body).Decode(&raw)
	var ok protocol.MMRoomResponse
	var he protocol.HTTPError
	if res.StatusCode < 300 {
		_ = json.Unmarshal(raw, &ok)
	} else {
		_ = json.Unmarshal(raw, &he)
	}
	return res.StatusCode, ok, he
}

func (e *env) mustOK(method, path string, body any) protocol.MMRoomResponse {
	e.t.Helper()
	st, resp, he := e.do(method, path, body)
	if st >= 300 {
		e.t.Fatalf("%s %s -> %d %s: %s", method, path, st, he.Code, he.Message)
	}
	return resp
}

func (e *env) mustFail(method, path string, body any, status int, code string) {
	e.t.Helper()
	st, _, he := e.do(method, path, body)
	if st != status || he.Code != code {
		e.t.Fatalf("%s %s -> %d %q, want %d %q (%s)", method, path, st, he.Code, status, code, he.Message)
	}
}

func cfg(min int, bots bool) protocol.RoomConfig {
	c := protocol.DefaultRoomConfig()
	c.MinPlayers = min
	c.FillBots = bots
	return c
}

func (e *env) create(pid string, c protocol.RoomConfig) protocol.MMRoomResponse {
	e.t.Helper()
	return e.mustOK("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: pid, Name: "N-" + pid, Config: c})
}

func (e *env) join(code, pid string) protocol.MMRoomResponse {
	e.t.Helper()
	return e.mustOK("POST", "/rooms/"+code+"/join", protocol.MMJoinRoomRequest{PlayerID: pid, Name: "N-" + pid})
}

func decodeFrames(t *testing.T, gp protocol.GatewayPush) []any {
	t.Helper()
	out := make([]any, 0, len(gp.Frames))
	for _, f := range gp.Frames {
		m, err := protocol.Decode(f)
		if err != nil {
			t.Fatalf("decode frame %s: %v", f, err)
		}
		out = append(out, m)
	}
	return out
}

func TestCreateRoom(t *testing.T) {
	e := newEnv(t)
	resp := e.create("p1", cfg(2, false))
	if len(resp.Room.Code) != CodeLength {
		t.Fatalf("code %q should have %d letters", resp.Room.Code, CodeLength)
	}
	for _, r := range resp.Room.Code {
		if r < 'A' || r > 'Z' {
			t.Fatalf("code %q must be uppercase letters", resp.Room.Code)
		}
	}
	if resp.Room.T != protocol.TypeRoomState || resp.Room.CountdownMS != 0 {
		t.Fatalf("bad room state: %+v", resp.Room)
	}
	if len(resp.Room.Players) != 1 || !resp.Room.Players[0].IsCreator || resp.Room.Players[0].Color != Palette[0] || resp.Room.Players[0].Name != "N-p1" {
		t.Fatalf("bad creator: %+v", resp.Room.Players)
	}
	if len(resp.Recipients) != 1 || resp.Recipients[0] != "p1" {
		t.Fatalf("recipients %v", resp.Recipients)
	}
	if resp.Room.Config != cfg(2, false) {
		t.Fatalf("config echoed wrong: %+v", resp.Room.Config)
	}

	got := e.mustOK("GET", "/rooms/"+resp.Room.Code, nil)
	if got.Room.Code != resp.Room.Code {
		t.Fatalf("GET mismatch")
	}
	// Lookup is case insensitive.
	e.mustOK("GET", "/rooms/"+string(bytes.ToLower([]byte(resp.Room.Code))), nil)
	e.mustFail("GET", "/rooms/ZZZZZ", nil, http.StatusNotFound, protocol.ErrNotFound)
}

func TestCreateRoomValidation(t *testing.T) {
	e := newEnv(t)
	bad := cfg(2, false)
	bad.Catchers = 9
	e.mustFail("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: "p1", Config: bad}, http.StatusBadRequest, protocol.ErrBadRequest)
	bad = cfg(2, false)
	bad.MazeSize = "huge"
	e.mustFail("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: "p1", Config: bad}, http.StatusBadRequest, protocol.ErrBadRequest)
	e.mustFail("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: "", Config: cfg(2, false)}, http.StatusBadRequest, protocol.ErrBadRequest)

	// Empty config falls back to the defaults.
	resp := e.mustOK("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: "p1"})
	if resp.Room.Config != protocol.DefaultRoomConfig() {
		t.Fatalf("expected default config, got %+v", resp.Room.Config)
	}
}

func TestJoinLeaveColorsAndCapacity(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(8, false)).Room.Code
	for i := 2; i <= 8; i++ {
		e.join(code, "p"+string(rune('0'+i)))
	}
	resp := e.mustOK("GET", "/rooms/"+code, nil)
	if len(resp.Room.Players) != 8 || len(resp.Recipients) != 8 {
		t.Fatalf("expected 8 players, got %d/%d", len(resp.Room.Players), len(resp.Recipients))
	}
	seen := map[string]bool{}
	for _, p := range resp.Room.Players {
		if seen[p.Color] {
			t.Fatalf("duplicate color %s", p.Color)
		}
		seen[p.Color] = true
	}
	e.mustFail("POST", "/rooms/"+code+"/join", protocol.MMJoinRoomRequest{PlayerID: "p9"}, http.StatusConflict, protocol.ErrRoomFull)
	// Re-join is idempotent.
	if r := e.join(code, "p3"); len(r.Room.Players) != 8 {
		t.Fatalf("rejoin changed roster")
	}

	// Creator leaves: next human inherits, color is freed and reused.
	left := e.mustOK("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "p1"})
	if len(left.Room.Players) != 7 || !left.Room.Players[0].IsCreator || left.Room.Players[0].PlayerID != "p2" {
		t.Fatalf("creator not transferred: %+v", left.Room.Players)
	}
	if !contains(left.Recipients, "p1") {
		t.Fatalf("leaver should be a recipient: %v", left.Recipients)
	}
	if r := e.join(code, "p9"); r.Room.Players[7].Color != Palette[0] {
		t.Fatalf("freed color not reused: %s", r.Room.Players[7].Color)
	}
	e.mustFail("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "ghost"}, http.StatusNotFound, protocol.ErrNotInRoom)
	e.mustFail("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "ghost", Ready: true}, http.StatusNotFound, protocol.ErrNotInRoom)
}

func TestLastPlayerLeavingDeletesRoom(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	resp := e.mustOK("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "p1"})
	if len(resp.Room.Players) != 0 || !contains(resp.Recipients, "p1") {
		t.Fatalf("unexpected: %+v", resp)
	}
	e.mustFail("GET", "/rooms/"+code, nil, http.StatusNotFound, protocol.ErrNotFound)
}

func TestReadyAutoStartsAndRunsCountdown(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	r := e.mustOK("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "p1", Ready: true})
	if r.Room.CountdownMS != 0 {
		t.Fatalf("countdown should not start with one ready player")
	}
	r = e.mustOK("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "p2", Ready: true})
	if r.Room.CountdownMS <= 0 || r.Room.CountdownMS > 120 {
		t.Fatalf("countdown_ms = %d, want (0,120]", r.Room.CountdownMS)
	}
	e.svc.Wait()

	pushes, matches := e.peers.snapshot()
	if len(matches) != 1 || matches[0].RoomCode != code || len(matches[0].Players) != 2 {
		t.Fatalf("simulation not called correctly: %+v", matches)
	}
	var sawCountdownEvent, sawRoomState bool
	starts := map[string]bool{}
	for _, gp := range pushes {
		if gp.RoomCode != code {
			t.Fatalf("push without room code: %+v", gp)
		}
		for _, f := range decodeFrames(t, gp) {
			switch m := f.(type) {
			case *protocol.Event:
				if m.Kind == protocol.EventCountdown && m.MS > 0 {
					sawCountdownEvent = true
				}
			case *protocol.RoomState:
				if m.CountdownMS > 0 && len(gp.PlayerIDs) == 0 {
					sawRoomState = true
				}
			case *protocol.MatchStart:
				if gp.MatchID != "m-"+code || len(gp.PlayerIDs) != 1 || m.You.PlayerID != gp.PlayerIDs[0] {
					t.Fatalf("match_start push malformed: %+v you=%+v", gp, m.You)
				}
				starts[gp.PlayerIDs[0]] = true
			}
		}
	}
	if !sawCountdownEvent || !sawRoomState {
		t.Fatalf("missing countdown frames: event=%v room_state=%v", sawCountdownEvent, sawRoomState)
	}
	if !starts["p1"] || !starts["p2"] {
		t.Fatalf("match_start not delivered per player: %v", starts)
	}

	// Room is now in a match: joins and ready toggles are rejected.
	e.mustFail("POST", "/rooms/"+code+"/join", protocol.MMJoinRoomRequest{PlayerID: "late"}, http.StatusConflict, protocol.ErrInMatch)
	e.mustFail("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "p1"}, http.StatusConflict, protocol.ErrInMatch)
	e.mustFail("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"}, http.StatusConflict, protocol.ErrInMatch)
}

func TestCreatorStart(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(4, false)).Room.Code
	// Alone: cannot force start.
	e.mustFail("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"}, http.StatusBadRequest, protocol.ErrBadRequest)
	e.join(code, "p2")
	// Non creator is forbidden.
	e.mustFail("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p2"}, http.StatusForbidden, protocol.ErrForbidden)
	// Both ready but below min_players: no auto start.
	e.mustOK("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "p1", Ready: true})
	if r := e.mustOK("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "p2", Ready: true}); r.Room.CountdownMS != 0 {
		t.Fatalf("should not auto start below min_players")
	}
	r := e.mustOK("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"})
	if r.Room.CountdownMS <= 0 {
		t.Fatalf("creator start should begin countdown")
	}
	// Second start during countdown is a no-op.
	e.mustOK("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"})
	e.svc.Wait()
	_, matches := e.peers.snapshot()
	if len(matches) != 1 || len(matches[0].Players) != 2 {
		t.Fatalf("expected one match with 2 players: %+v", matches)
	}
}

func TestBotFill(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(4, true)).Room.Code
	r := e.mustOK("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"})
	if len(r.Room.Players) != 4 {
		t.Fatalf("expected 4 players after bot fill, got %d", len(r.Room.Players))
	}
	bots := 0
	colors := map[string]bool{}
	for i, p := range r.Room.Players {
		colors[p.Color] = true
		if i == 0 {
			continue
		}
		if !p.IsBot || !p.Ready || p.Name != "Bot-"+string(rune('0'+i)) {
			t.Fatalf("bad bot %d: %+v", i, p)
		}
		bots++
	}
	if bots != 3 || len(colors) != 4 {
		t.Fatalf("bots=%d distinct colors=%d", bots, len(colors))
	}
	if len(r.Recipients) != 1 {
		t.Fatalf("bots must not be recipients: %v", r.Recipients)
	}
	e.svc.Wait()
	pushes, matches := e.peers.snapshot()
	if len(matches) != 1 || len(matches[0].Players) != 4 {
		t.Fatalf("simulation should get 4 players: %+v", matches)
	}
	botJoined := 0
	matchStarts := 0
	for _, gp := range pushes {
		for _, f := range decodeFrames(t, gp) {
			switch m := f.(type) {
			case *protocol.Event:
				if m.Kind == protocol.EventBotJoined && m.PlayerID != "" && m.Name != "" {
					botJoined++
				}
			case *protocol.MatchStart:
				matchStarts++
			}
		}
	}
	if botJoined != 3 || matchStarts != 1 {
		t.Fatalf("bot_joined=%d match_start=%d", botJoined, matchStarts)
	}
}

func TestLeaveDuringCountdownCancels(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	e.mustOK("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "p1", Ready: true})
	e.mustOK("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "p2", Ready: true})
	r := e.mustOK("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "p2"})
	if r.Room.CountdownMS != 0 {
		t.Fatalf("countdown should be cancelled")
	}
	e.svc.Wait()
	_, matches := e.peers.snapshot()
	if len(matches) != 0 {
		t.Fatalf("simulation must not be called after cancel")
	}
	// Room is back in the lobby and can start again.
	e.join(code, "p3")
	if r := e.mustOK("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"}); r.Room.CountdownMS <= 0 {
		t.Fatalf("restart failed")
	}
	e.svc.Wait()
	if _, matches := e.peers.snapshot(); len(matches) != 1 {
		t.Fatalf("expected one match after restart")
	}
}

func TestSimulationFailureReturnsToLobby(t *testing.T) {
	e := newEnv(t)
	e.peers.simFail = true
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	e.mustOK("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"})
	e.svc.Wait()
	r := e.mustOK("GET", "/rooms/"+code, nil)
	if r.Room.CountdownMS != 0 {
		t.Fatalf("countdown should be over")
	}
	for _, p := range r.Room.Players {
		if p.Ready {
			t.Fatalf("players should be un-readied after failure")
		}
	}
	pushes, _ := e.peers.snapshot()
	sawErr := false
	for _, gp := range pushes {
		for _, f := range decodeFrames(t, gp) {
			if _, ok := f.(*protocol.Error); ok {
				sawErr = true
			}
		}
	}
	if !sawErr {
		t.Fatalf("expected an error frame pushed to the room")
	}
	// Room is usable again.
	e.mustOK("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"})
}

func TestNewHandlerServesRoutes(t *testing.T) {
	srv := httptest.NewServer(NewHandler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var h protocol.HealthResponse
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil || h.Service != "matchmaking" || !h.OK {
		t.Fatalf("bad health: %+v %v", h, err)
	}
	res2, err := http.Post(srv.URL+"/rooms", "application/json", bytes.NewReader([]byte(`{"player_id":"x"}`)))
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusCreated {
		t.Fatalf("POST /rooms -> %d", res2.StatusCode)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
