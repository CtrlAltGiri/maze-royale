package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// fakeMM is a minimal matchmaking peer: one room, roster of whoever joined.
type fakeMM struct {
	mu      sync.Mutex
	players []protocol.RoomPlayer
	code    string
	inMatch bool
}

func (f *fakeMM) response() protocol.MMRoomResponse {
	ids := make([]string, 0, len(f.players))
	for _, p := range f.players {
		ids = append(ids, p.PlayerID)
	}
	return protocol.MMRoomResponse{
		Room:       protocol.RoomState{T: protocol.TypeRoomState, Code: f.code, Config: protocol.DefaultRoomConfig(), Players: f.players},
		Recipients: ids,
	}
}

func (f *fakeMM) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rooms", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.MMCreateRoomRequest
		_ = httpx.ReadJSON(r, &req)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.code = "ABCD"
		f.players = []protocol.RoomPlayer{{PlayerID: req.PlayerID, Name: req.Name, Color: "#fff", IsCreator: true}}
		httpx.WriteJSON(w, 200, f.response())
	})
	mux.HandleFunc("POST /rooms/{code}/join", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.MMJoinRoomRequest
		_ = httpx.ReadJSON(r, &req)
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.PathValue("code") != f.code {
			httpx.WriteError(w, 404, protocol.ErrNotFound, "no such room")
			return
		}
		if f.inMatch {
			httpx.WriteError(w, 409, protocol.ErrInMatch, "room is in a match")
			return
		}
		f.players = append(f.players, protocol.RoomPlayer{PlayerID: req.PlayerID, Name: req.Name, Color: "#000"})
		httpx.WriteJSON(w, 200, f.response())
	})
	mux.HandleFunc("POST /rooms/{code}/leave", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.MMPlayerRequest
		_ = httpx.ReadJSON(r, &req)
		f.mu.Lock()
		defer f.mu.Unlock()
		kept := f.players[:0]
		for _, p := range f.players {
			if p.PlayerID != req.PlayerID {
				kept = append(kept, p)
			}
		}
		f.players = kept
		httpx.WriteJSON(w, 200, f.response())
	})
	mux.HandleFunc("POST /rooms/{code}/ready", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.MMReadyRequest
		_ = httpx.ReadJSON(r, &req)
		f.mu.Lock()
		defer f.mu.Unlock()
		for i := range f.players {
			if f.players[i].PlayerID == req.PlayerID {
				f.players[i].Ready = req.Ready
			}
		}
		httpx.WriteJSON(w, 200, f.response())
	})
	mux.HandleFunc("POST /rooms/{code}/start", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.MMPlayerRequest
		_ = httpx.ReadJSON(r, &req)
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.players) == 0 || f.players[0].PlayerID != req.PlayerID {
			httpx.WriteError(w, 403, protocol.ErrForbidden, "creator only")
			return
		}
		httpx.WriteJSON(w, 200, f.response())
	})
	return mux
}

// fakeSim records inputs and serves one match state.
type fakeSim struct {
	mu     sync.Mutex
	inputs []struct {
		match string
		req   protocol.SimInputRequest
	}
	gotInput chan struct{}
	ended    bool
}

func (f *fakeSim) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /matches/{id}/input", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.SimInputRequest
		_ = httpx.ReadJSON(r, &req)
		f.mu.Lock()
		f.inputs = append(f.inputs, struct {
			match string
			req   protocol.SimInputRequest
		}{r.PathValue("id"), req})
		f.mu.Unlock()
		f.gotInput <- struct{}{}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /matches/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ended := f.ended
		f.mu.Unlock()
		httpx.WriteJSON(w, 200, protocol.SimMatchStateResponse{
			Ended: ended,
			Start: protocol.MatchStart{T: protocol.TypeMatchStart, MatchID: r.PathValue("id"),
				Maze: protocol.Maze{W: 1, H: 1, Walls: protocol.Walls{15}},
				You:  protocol.You{Role: protocol.RoleSpectator}, RoundMS: 1000},
			Snapshot: protocol.Snapshot{T: protocol.TypeSnapshot, Tick: 42, AliveCount: 1},
		})
	})
	return mux
}

type env struct {
	t       *testing.T
	gw      *httptest.Server
	mm      *fakeMM
	sim     *fakeSim
	webDir  string
	timeout time.Duration
}

func newEnv(t *testing.T) *env {
	t.Helper()
	mm := &fakeMM{}
	sim := &fakeSim{gotInput: make(chan struct{}, 16)}
	mmSrv := httptest.NewServer(mm.handler())
	simSrv := httptest.NewServer(sim.handler())
	statsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, protocol.LeaderboardResponse{Total: 1, Entries: []protocol.LeaderboardEntry{{Name: "Ana <b>", Wins: 3, Rounds: 5, TotalSurvive: 61500}}})
	}))
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<h1>maze</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithConfig(Config{MatchmakingURL: mmSrv.URL, SimulationURL: simSrv.URL, StatsURL: statsSrv.URL, WebDir: webDir})
	gw := httptest.NewServer(httpx.RequestID("gateway", h))
	t.Cleanup(func() { gw.Close(); mmSrv.Close(); simSrv.Close(); statsSrv.Close() })
	return &env{t: t, gw: gw, mm: mm, sim: sim, webDir: webDir, timeout: 3 * time.Second}
}

type client struct {
	t  *testing.T
	ws *websocket.Conn
	id string
}

func (e *env) dial() *client {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.gw.URL, "http")+"/ws", nil)
	if err != nil {
		e.t.Fatalf("dial: %v", err)
	}
	c := &client{t: e.t, ws: ws}
	e.t.Cleanup(func() { _ = ws.CloseNow() })
	return c
}

func (c *client) send(msg any) {
	c.t.Helper()
	data, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *client) recv() any {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.ws.Read(ctx)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	msg, err := protocol.Decode(data)
	if err != nil {
		c.t.Fatalf("decode %s: %v", data, err)
	}
	return msg
}

func (c *client) hello(name string) {
	c.t.Helper()
	c.send(protocol.Hello{T: protocol.TypeHello, Name: name})
	w, ok := c.recv().(*protocol.Welcome)
	if !ok || w.PlayerID == "" {
		c.t.Fatalf("expected welcome, got %#v", w)
	}
	c.id = w.PlayerID
}

// push posts p and returns how many connections received the frames.
func (e *env) push(p protocol.GatewayPush) int {
	e.t.Helper()
	body, _ := json.Marshal(p)
	res, err := http.Post(e.gw.URL+"/internal/push", "application/json", bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		e.t.Fatalf("push status %d", res.StatusCode)
	}
	var out struct {
		Delivered int `json:"delivered"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out.Delivered
}

func raw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHelloWelcomePingPong(t *testing.T) {
	e := newEnv(t)
	c := e.dial()
	c.send(protocol.Ping{T: protocol.TypePing, TS: 1})
	if p, ok := c.recv().(*protocol.Pong); !ok || p.TS != 1 {
		t.Fatalf("expected pong before hello, got %#v", p)
	}
	c.send(protocol.SetReady{T: protocol.TypeSetReady, Ready: true})
	if er, ok := c.recv().(*protocol.Error); !ok || er.Code != protocol.ErrBadRequest {
		t.Fatalf("expected bad_request before hello, got %#v", er)
	}
	c.hello("ana")
	c.send(protocol.Ping{T: protocol.TypePing, TS: 12345})
	if p, ok := c.recv().(*protocol.Pong); !ok || p.TS != 12345 {
		t.Fatalf("expected pong 12345, got %#v", p)
	}
	c.send(protocol.LeaveRoom{T: protocol.TypeLeaveRoom})
	if er, ok := c.recv().(*protocol.Error); !ok || er.Code != protocol.ErrNotInRoom {
		t.Fatalf("expected not_in_room, got %#v", er)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.ws.Write(ctx, websocket.MessageText, []byte("{not json"))
	if er, ok := c.recv().(*protocol.Error); !ok || er.Code != protocol.ErrBadRequest {
		t.Fatalf("expected bad_request, got %#v", er)
	}
}

func TestLobbyFanoutAndErrors(t *testing.T) {
	e := newEnv(t)
	a, b := e.dial(), e.dial()
	a.hello("ana")
	b.hello("bo")

	a.send(protocol.CreateRoom{T: protocol.TypeCreateRoom, Config: protocol.DefaultRoomConfig()})
	rs, ok := a.recv().(*protocol.RoomState)
	if !ok || rs.Code != "ABCD" || len(rs.Players) != 1 || rs.Players[0].PlayerID != a.id {
		t.Fatalf("bad room_state after create: %#v", rs)
	}

	b.send(protocol.JoinRoom{T: protocol.TypeJoinRoom, Code: "NOPE"})
	if er, ok := b.recv().(*protocol.Error); !ok || er.Code != protocol.ErrNotFound {
		t.Fatalf("expected not_found, got %#v", er)
	}

	b.send(protocol.JoinRoom{T: protocol.TypeJoinRoom, Code: "ABCD"})
	for _, c := range []*client{a, b} {
		rs, ok := c.recv().(*protocol.RoomState)
		if !ok || len(rs.Players) != 2 {
			t.Fatalf("expected 2-player room_state on both tabs, got %#v", rs)
		}
	}

	b.send(protocol.SetReady{T: protocol.TypeSetReady, Ready: true})
	for _, c := range []*client{a, b} {
		rs := c.recv().(*protocol.RoomState)
		if !rs.Players[1].Ready {
			t.Fatalf("expected bo ready: %#v", rs)
		}
	}

	b.send(protocol.StartRoom{T: protocol.TypeStartRoom})
	if er, ok := b.recv().(*protocol.Error); !ok || er.Code != protocol.ErrForbidden {
		t.Fatalf("expected forbidden, got %#v", er)
	}
	a.send(protocol.StartRoom{T: protocol.TypeStartRoom})
	if _, ok := a.recv().(*protocol.RoomState); !ok {
		t.Fatal("a expected room_state from start (and nothing from bo's forbidden start)")
	}
	b.recv()

	b.send(protocol.LeaveRoom{T: protocol.TypeLeaveRoom})
	rs = a.recv().(*protocol.RoomState)
	if len(rs.Players) != 1 {
		t.Fatalf("expected 1 player after leave: %#v", rs)
	}

	// b is no longer a member, so room-wide pushes must skip it.
	if n := e.push(protocol.GatewayPush{RoomCode: "ABCD", Frames: []json.RawMessage{raw(t, protocol.Event{T: protocol.TypeEvent, Kind: protocol.EventCountdown, MS: 5000})}}); n != 1 {
		t.Fatalf("expected push delivered to 1 connection, got %d", n)
	}
	if ev, ok := a.recv().(*protocol.Event); !ok || ev.Kind != protocol.EventCountdown {
		t.Fatalf("expected countdown event, got %#v", ev)
	}
	b.send(protocol.Ping{T: protocol.TypePing, TS: 1})
	if _, ok := b.recv().(*protocol.Pong); !ok {
		t.Fatal("b expected pong, not a stale room frame")
	}
}

func TestPushMatchStartInputAndReconnect(t *testing.T) {
	e := newEnv(t)
	a, b := e.dial(), e.dial()
	a.hello("ana")
	b.hello("bo")
	a.send(protocol.CreateRoom{T: protocol.TypeCreateRoom, Config: protocol.DefaultRoomConfig()})
	a.recv()
	b.send(protocol.JoinRoom{T: protocol.TypeJoinRoom, Code: "ABCD"})
	a.recv()
	b.recv()

	// input before a match is known -> error
	a.send(protocol.Input{T: protocol.TypeInput, Seq: 1, Dir: protocol.DirUp})
	if er, ok := a.recv().(*protocol.Error); !ok || er.Code != protocol.ErrBadRequest {
		t.Fatalf("expected bad_request, got %#v", er)
	}

	// per-player match_start pushes
	startFor := func(id string) json.RawMessage {
		return raw(t, protocol.MatchStart{T: protocol.TypeMatchStart, MatchID: "m1", You: protocol.You{PlayerID: id, Role: protocol.RoleRunner}})
	}
	e.push(protocol.GatewayPush{MatchID: "m1", RoomCode: "ABCD", PlayerIDs: []string{a.id}, Frames: []json.RawMessage{startFor(a.id)}})
	e.push(protocol.GatewayPush{MatchID: "m1", RoomCode: "ABCD", PlayerIDs: []string{b.id}, Frames: []json.RawMessage{startFor(b.id)}})
	if ms := a.recv().(*protocol.MatchStart); ms.You.PlayerID != a.id {
		t.Fatalf("a got wrong match_start: %#v", ms)
	}
	if ms := b.recv().(*protocol.MatchStart); ms.You.PlayerID != b.id {
		t.Fatalf("b got wrong match_start: %#v", ms)
	}

	// room-wide snapshot
	e.push(protocol.GatewayPush{MatchID: "m1", RoomCode: "ABCD", Frames: []json.RawMessage{raw(t, protocol.Snapshot{T: protocol.TypeSnapshot, Tick: 7})}})
	for _, c := range []*client{a, b} {
		if sn, ok := c.recv().(*protocol.Snapshot); !ok || sn.Tick != 7 {
			t.Fatalf("expected snapshot tick 7, got %#v", sn)
		}
	}

	// input routes to /matches/m1/input
	a.send(protocol.Input{T: protocol.TypeInput, Seq: 2, Dir: protocol.DirRight})
	select {
	case <-e.sim.gotInput:
	case <-time.After(2 * time.Second):
		t.Fatal("simulation never received input")
	}
	e.sim.mu.Lock()
	in := e.sim.inputs[0]
	e.sim.mu.Unlock()
	if in.match != "m1" || in.req.PlayerID != a.id || in.req.Seq != 2 || in.req.Dir != protocol.DirRight {
		t.Fatalf("bad forwarded input: %#v", in)
	}
	a.send(protocol.Input{T: protocol.TypeInput, Seq: 3, Dir: "diagonal"})
	if er, ok := a.recv().(*protocol.Error); !ok || er.Code != protocol.ErrBadRequest {
		t.Fatalf("expected bad_request for bad dir, got %#v", er)
	}

	// b drops; pushes still reach a, membership is kept.
	_ = b.ws.Close(websocket.StatusNormalClosure, "reload")
	time.Sleep(50 * time.Millisecond)
	if n := e.push(protocol.GatewayPush{MatchID: "m1", RoomCode: "ABCD", Frames: []json.RawMessage{raw(t, protocol.Snapshot{T: protocol.TypeSnapshot, Tick: 8})}}); n != 1 {
		t.Fatalf("expected 1 live connection, got %d", n)
	}
	if sn := a.recv().(*protocol.Snapshot); sn.Tick != 8 {
		t.Fatalf("expected tick 8, got %#v", sn)
	}

	// reconnect: hello + join_room into the in-match room -> spectator start + snapshot
	e.mm.mu.Lock()
	e.mm.inMatch = true
	e.mm.mu.Unlock()
	b2 := e.dial()
	b2.hello("bo")
	b2.send(protocol.JoinRoom{T: protocol.TypeJoinRoom, Code: "ABCD"})
	ms, ok := b2.recv().(*protocol.MatchStart)
	if !ok || ms.MatchID != "m1" || ms.You.Role != protocol.RoleSpectator || ms.You.PlayerID != b2.id {
		t.Fatalf("expected spectator match_start, got %#v", ms)
	}
	if sn, ok := b2.recv().(*protocol.Snapshot); !ok || sn.Tick != 42 {
		t.Fatalf("expected latest snapshot, got %#v", sn)
	}
	// spectator now receives room-wide pushes
	e.push(protocol.GatewayPush{MatchID: "m1", RoomCode: "ABCD", Frames: []json.RawMessage{raw(t, protocol.Snapshot{T: protocol.TypeSnapshot, Tick: 9})}})
	a.recv()
	if sn := b2.recv().(*protocol.Snapshot); sn.Tick != 9 {
		t.Fatalf("spectator expected tick 9, got %#v", sn)
	}

	// spectator input is refused at the gateway and never reaches the simulation
	b2.send(protocol.Input{T: protocol.TypeInput, Seq: 1, Dir: protocol.DirLeft})
	if er, ok := b2.recv().(*protocol.Error); !ok || er.Code != protocol.ErrForbidden {
		t.Fatalf("expected forbidden for spectator input, got %#v", er)
	}
	a.send(protocol.Input{T: protocol.TypeInput, Seq: 5, Dir: protocol.DirLeft})
	<-e.sim.gotInput
	e.sim.mu.Lock()
	for _, in := range e.sim.inputs {
		if in.req.PlayerID == b2.id {
			t.Fatalf("spectator input forwarded to simulation: %#v", in)
		}
	}
	e.sim.mu.Unlock()

	// a fresh tab (never seen before) joining mid-match is also a spectator
	c := e.dial()
	c.hello("cy")
	c.send(protocol.JoinRoom{T: protocol.TypeJoinRoom, Code: "ABCD"})
	if ms, ok := c.recv().(*protocol.MatchStart); !ok || ms.You.Role != protocol.RoleSpectator || ms.You.PlayerID != c.id {
		t.Fatalf("expected spectator match_start for fresh tab, got %#v", ms)
	}
	if _, ok := c.recv().(*protocol.Snapshot); !ok {
		t.Fatal("fresh tab expected snapshot")
	}
	// the runner's stream is undisturbed: a targeted push still reaches only a
	if n := e.push(protocol.GatewayPush{MatchID: "m1", RoomCode: "ABCD", PlayerIDs: []string{a.id}, Frames: []json.RawMessage{raw(t, protocol.Event{T: protocol.TypeEvent, Kind: protocol.EventCaught, PlayerID: a.id})}}); n != 1 {
		t.Fatalf("targeted push delivered to %d", n)
	}
	if ev, ok := a.recv().(*protocol.Event); !ok || ev.Kind != protocol.EventCaught {
		t.Fatalf("expected caught event, got %#v", ev)
	}

	// simulation already reports the match ended -> not_found, mapping dropped
	e.sim.mu.Lock()
	e.sim.ended = true
	e.sim.mu.Unlock()
	d := e.dial()
	d.hello("di")
	d.send(protocol.JoinRoom{T: protocol.TypeJoinRoom, Code: "ABCD"})
	if er, ok := d.recv().(*protocol.Error); !ok || er.Code != protocol.ErrNotFound {
		t.Fatalf("expected not_found for ended match, got %#v", er)
	}
	d.send(protocol.JoinRoom{T: protocol.TypeJoinRoom, Code: "ABCD"})
	if er, ok := d.recv().(*protocol.Error); !ok || er.Code != protocol.ErrInMatch {
		t.Fatalf("expected matchmaking in_match once the mapping is dropped, got %#v", er)
	}
	e.sim.mu.Lock()
	e.sim.ended = false
	e.sim.mu.Unlock()
	// a later push re-learns the mapping
	e.push(protocol.GatewayPush{MatchID: "m1", RoomCode: "ABCD", Frames: []json.RawMessage{raw(t, protocol.Snapshot{T: protocol.TypeSnapshot, Tick: 10})}})
	a.recv()
	b2.recv()
	c.recv()

	// match_end clears the mapping; input is rejected again
	e.push(protocol.GatewayPush{MatchID: "m1", RoomCode: "ABCD", Frames: []json.RawMessage{raw(t, protocol.MatchEnd{T: protocol.TypeMatchEnd, MatchID: "m1"})}})
	a.recv()
	b2.recv()
	a.send(protocol.Input{T: protocol.TypeInput, Seq: 4, Dir: protocol.DirUp})
	if er, ok := a.recv().(*protocol.Error); !ok || er.Code != protocol.ErrBadRequest {
		t.Fatalf("expected bad_request after match_end, got %#v", er)
	}
}

func TestPushRejectsBadBody(t *testing.T) {
	e := newEnv(t)
	res, err := http.Post(e.gw.URL+"/internal/push", "application/json", strings.NewReader(`{"frames":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}
}

func TestLeaderboardAndStatic(t *testing.T) {
	e := newEnv(t)
	res, err := http.Get(e.gw.URL + "/leaderboard?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	body := buf.String()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("leaderboard: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, want := range []string{"<table", "Ana &lt;b&gt;", "<td>3</td>", "<td>5</td>", "61.5"} {
		if !strings.Contains(body, want) {
			t.Fatalf("leaderboard html missing %q:\n%s", want, body)
		}
	}

	res2, err := http.Get(e.gw.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	buf.Reset()
	_, _ = buf.ReadFrom(res2.Body)
	if res2.StatusCode != 200 || !strings.Contains(buf.String(), "<h1>maze</h1>") {
		t.Fatalf("static: %d %s", res2.StatusCode, buf.String())
	}

	res3, err := http.Get(e.gw.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res3.Body.Close()
	var h protocol.HealthResponse
	if err := json.NewDecoder(res3.Body).Decode(&h); err != nil || h.Service != "gateway" || !h.OK {
		t.Fatalf("healthz: %v %#v", err, h)
	}
}

func TestLeaderboardStatsDown(t *testing.T) {
	h := NewHandlerWithConfig(Config{StatsURL: "http://127.0.0.1:1", WebDir: t.TempDir()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/leaderboard", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}
