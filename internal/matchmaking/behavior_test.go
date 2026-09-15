package matchmaking

import (
	"net/http"
	"testing"
	"time"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

func (e *env) ready(code, pid string, ready bool) protocol.MMRoomResponse {
	e.t.Helper()
	return e.mustOK("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: pid, Ready: ready})
}

func (e *env) start(code, pid string) protocol.MMRoomResponse {
	e.t.Helper()
	return e.mustOK("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: pid})
}

func TestAutoStartNeedsEverySeatedPlayerReady(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	e.join(code, "p3")
	e.ready(code, "p1", true)
	if r := e.ready(code, "p2", true); r.Room.CountdownMS != 0 {
		t.Fatalf("min_players reached but p3 not ready: countdown must not start (%d)", r.Room.CountdownMS)
	}
	// Toggling a player back off keeps the lobby idle even once p3 readies.
	e.ready(code, "p1", false)
	if r := e.ready(code, "p3", true); r.Room.CountdownMS != 0 {
		t.Fatalf("p1 un-readied: countdown must not start")
	}
	r := e.ready(code, "p1", true)
	if r.Room.CountdownMS <= 0 {
		t.Fatalf("all three ready: countdown should start")
	}
	for _, p := range r.Room.Players {
		if !p.Ready {
			t.Fatalf("player %s should be ready in the countdown state", p.PlayerID)
		}
	}
	e.svc.Wait()
	_, matches := e.peers.snapshot()
	if len(matches) != 1 || len(matches[0].Players) != 3 {
		t.Fatalf("expected one match with 3 players: %+v", matches)
	}
}

func TestReadyFalseNeverStartsAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	for i := 0; i < 3; i++ {
		if r := e.ready(code, "p1", false); r.Room.CountdownMS != 0 || r.Room.Players[0].Ready {
			t.Fatalf("ready=false must not start or flag ready: %+v", r.Room)
		}
	}
	if r := e.ready(code, "p2", false); r.Room.CountdownMS != 0 {
		t.Fatal("two not-ready players started a countdown")
	}
	e.svc.Wait()
	if _, matches := e.peers.snapshot(); len(matches) != 0 {
		t.Fatalf("simulation called: %+v", matches)
	}
}

func TestCountdownFramesDecreaseAndEndWithMatchStart(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	before := time.Now()
	e.ready(code, "p1", true)
	e.ready(code, "p2", true)
	e.svc.Wait()
	elapsed := time.Since(before)
	if elapsed < 120*time.Millisecond {
		t.Fatalf("match started after %v, before the %v countdown elapsed", elapsed, 120*time.Millisecond)
	}

	pushes, _ := e.peers.snapshot()
	var countdownMS []int64
	var stateMS []int64
	startIdx := -1
	for i, gp := range pushes {
		for _, f := range decodeFrames(t, gp) {
			switch m := f.(type) {
			case *protocol.Event:
				if m.Kind != protocol.EventCountdown {
					t.Fatalf("unexpected event kind %q during countdown", m.Kind)
				}
				if startIdx >= 0 {
					t.Fatal("countdown event after match_start")
				}
				countdownMS = append(countdownMS, m.MS)
			case *protocol.RoomState:
				if m.Code != code || len(m.Players) != 2 {
					t.Fatalf("room_state during countdown %+v", m)
				}
				stateMS = append(stateMS, m.CountdownMS)
			case *protocol.MatchStart:
				if startIdx < 0 {
					startIdx = i
				}
			default:
				t.Fatalf("unexpected frame %T", f)
			}
		}
	}
	if startIdx < 0 {
		t.Fatal("no match_start pushed")
	}
	if len(countdownMS) < 2 || countdownMS[0] != 120 {
		t.Fatalf("first countdown event must carry the full countdown: %v", countdownMS)
	}
	for i := 1; i < len(countdownMS); i++ {
		if countdownMS[i] <= 0 || countdownMS[i] >= countdownMS[i-1] {
			t.Fatalf("countdown ms must strictly decrease and stay positive: %v", countdownMS)
		}
	}
	if len(stateMS) != len(countdownMS) {
		t.Fatalf("every countdown event pairs with a room_state: %d vs %d", len(countdownMS), len(stateMS))
	}
	for i, ms := range stateMS {
		if ms <= 0 || ms > countdownMS[i] {
			t.Fatalf("room_state countdown_ms %v out of step with events %v", stateMS, countdownMS)
		}
	}
	// Every push before the match_start is a room broadcast (no player_ids).
	for _, gp := range pushes[:startIdx] {
		if len(gp.PlayerIDs) != 0 || gp.MatchID != "" {
			t.Fatalf("countdown push should be a room broadcast: %+v", gp)
		}
	}
}

func TestJoinDuringCountdownIsSeatedInTheMatch(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	e.ready(code, "p1", true)
	if r := e.ready(code, "p2", true); r.Room.CountdownMS <= 0 {
		t.Fatal("countdown should be running")
	}
	r := e.join(code, "p3")
	if r.Room.CountdownMS <= 0 || len(r.Room.Players) != 3 || r.Room.Players[2].Ready {
		t.Fatalf("late joiner: %+v", r.Room)
	}
	e.svc.Wait()
	pushes, matches := e.peers.snapshot()
	if len(matches) != 1 || len(matches[0].Players) != 3 || matches[0].Players[2].PlayerID != "p3" {
		t.Fatalf("late joiner must be in the roster: %+v", matches)
	}
	starts := map[string]bool{}
	for _, gp := range pushes {
		for _, f := range decodeFrames(t, gp) {
			if _, ok := f.(*protocol.MatchStart); ok {
				starts[gp.PlayerIDs[0]] = true
			}
		}
	}
	if len(starts) != 3 || !starts["p3"] {
		t.Fatalf("match_start per player %v", starts)
	}
}

func TestLeaveDuringCountdownWithEnoughPlayersKeepsCounting(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	e.join(code, "p3")
	e.ready(code, "p1", true)
	e.ready(code, "p2", true)
	e.ready(code, "p3", true)
	r := e.mustOK("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "p3"})
	if r.Room.CountdownMS <= 0 || len(r.Room.Players) != 2 {
		t.Fatalf("two players remain: countdown must continue: %+v", r.Room)
	}
	e.svc.Wait()
	_, matches := e.peers.snapshot()
	if len(matches) != 1 || len(matches[0].Players) != 2 {
		t.Fatalf("match should start with the two remaining players: %+v", matches)
	}
	for _, p := range matches[0].Players {
		if p.PlayerID == "p3" {
			t.Fatal("leaver was sent to simulation")
		}
	}
}

func TestCreatorTransferGrantsStartRights(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(4, false)).Room.Code
	e.join(code, "p2")
	e.join(code, "p3")
	e.mustFail("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p2"}, http.StatusForbidden, protocol.ErrForbidden)
	e.mustOK("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "p1"})
	e.mustFail("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"}, http.StatusNotFound, protocol.ErrNotInRoom)
	e.mustFail("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p3"}, http.StatusForbidden, protocol.ErrForbidden)
	r := e.start(code, "p2")
	if r.Room.CountdownMS <= 0 || !r.Room.Players[0].IsCreator || r.Room.Players[0].PlayerID != "p2" {
		t.Fatalf("new creator start: %+v", r.Room)
	}
	e.svc.Wait()
	if _, matches := e.peers.snapshot(); len(matches) != 1 || len(matches[0].Players) != 2 {
		t.Fatalf("matches %+v", matches)
	}
}

func TestCreatorStartIgnoresReadyFlags(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	r := e.start(code, "p1")
	if r.Room.CountdownMS <= 0 {
		t.Fatal("creator start with nobody ready should still count down")
	}
	e.svc.Wait()
	_, matches := e.peers.snapshot()
	if len(matches) != 1 {
		t.Fatalf("matches %+v", matches)
	}
	// Creator forcing the start does not pretend players were ready.
	for _, p := range matches[0].Players {
		if p.Ready {
			t.Fatalf("player %s reported ready", p.PlayerID)
		}
	}
}

func TestBotFillOnlyOnCreatorStartNotOnReady(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(3, true)).Room.Code
	e.join(code, "p2")
	e.ready(code, "p1", true)
	r := e.ready(code, "p2", true)
	if r.Room.CountdownMS != 0 || len(r.Room.Players) != 2 {
		t.Fatalf("readying below min_players must not fill bots or start: %+v", r.Room)
	}
	r = e.start(code, "p1")
	if r.Room.CountdownMS <= 0 || len(r.Room.Players) != 3 || !r.Room.Players[2].IsBot {
		t.Fatalf("creator start should fill one bot: %+v", r.Room)
	}
	if got := r.Room.Players[2].Name; got != "Bot-1" {
		t.Fatalf("bot name %q", got)
	}
	e.svc.Wait()
	_, matches := e.peers.snapshot()
	if len(matches) != 1 || len(matches[0].Players) != 3 || !matches[0].Players[2].IsBot {
		t.Fatalf("simulation should get the bot: %+v", matches)
	}
	// After the match the bot stays seated and ready; humans are un-readied.
	r = e.mustOK("POST", "/rooms/"+code+"/end", protocol.MMMatchEndRequest{MatchID: "m-" + code})
	if len(r.Room.Players) != 3 || !r.Room.Players[2].Ready || r.Room.Players[0].Ready {
		t.Fatalf("post-match roster %+v", r.Room.Players)
	}
	// Replay: bot counts toward min_players, so two humans ready auto-starts.
	e.ready(code, "p1", true)
	if r = e.ready(code, "p2", true); r.Room.CountdownMS <= 0 || len(r.Room.Players) != 3 {
		t.Fatalf("replay with bot: %+v", r.Room)
	}
	e.svc.Wait()
	if _, matches := e.peers.snapshot(); len(matches) != 2 {
		t.Fatalf("expected a second match: %d", len(matches))
	}
}

func TestBotsNeverInheritCreator(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(3, true)).Room.Code
	e.join(code, "p2")
	e.start(code, "p1")
	e.svc.Wait()
	e.mustOK("POST", "/rooms/"+code+"/end", protocol.MMMatchEndRequest{MatchID: "m-" + code})
	r := e.mustOK("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "p1"})
	var creators []string
	for _, p := range r.Room.Players {
		if p.IsCreator {
			creators = append(creators, p.PlayerID)
		}
	}
	if len(creators) != 1 || creators[0] != "p2" {
		t.Fatalf("creator after leave: %v (%+v)", creators, r.Room.Players)
	}
	// Last human leaving deletes the room even though a bot is still seated.
	e.mustOK("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "p2"})
	e.mustFail("GET", "/rooms/"+code, nil, http.StatusNotFound, protocol.ErrNotFound)
}

func TestConfigForwardedToSimulationUnchanged(t *testing.T) {
	e := newEnv(t)
	seed := int64(4242)
	c := protocol.RoomConfig{MazeSize: protocol.MazeLarge, Catchers: 3, CatcherSpeed: 1.3, RoundSeconds: 45, MinPlayers: 2, Seed: &seed}
	code := e.create("p1", c).Room.Code
	e.join(code, "p2")
	e.start(code, "p1")
	e.svc.Wait()
	_, matches := e.peers.snapshot()
	if len(matches) != 1 {
		t.Fatalf("matches %+v", matches)
	}
	got := matches[0].Config
	if got.MazeSize != c.MazeSize || got.Catchers != c.Catchers || got.CatcherSpeed != c.CatcherSpeed || got.RoundSeconds != c.RoundSeconds || got.MinPlayers != c.MinPlayers || got.FillBots != c.FillBots {
		t.Fatalf("config changed in flight: %+v", got)
	}
	if got.Seed == nil || *got.Seed != seed {
		t.Fatalf("seed dropped: %v", got.Seed)
	}
	if matches[0].RoomCode != code {
		t.Fatalf("room code %q", matches[0].RoomCode)
	}
	ps := matches[0].Players
	if len(ps) != 2 || !ps[0].IsCreator || ps[1].IsCreator || ps[0].Color == ps[1].Color || ps[0].Name != "N-p1" {
		t.Fatalf("roster %+v", ps)
	}
}

func TestConfigValidationBoundaries(t *testing.T) {
	e := newEnv(t)
	type mut func(c *protocol.RoomConfig)
	bad := map[string]mut{
		"catchers low":      func(c *protocol.RoomConfig) { c.Catchers = protocol.MinCatchers - 1 },
		"catchers negative": func(c *protocol.RoomConfig) { c.Catchers = -1 },
		"speed low":         func(c *protocol.RoomConfig) { c.CatcherSpeed = protocol.MinCatcherSpeed - 0.05 },
		"speed high":        func(c *protocol.RoomConfig) { c.CatcherSpeed = protocol.MaxCatcherSpeed + 0.05 },
		"round low":         func(c *protocol.RoomConfig) { c.RoundSeconds = protocol.MinRoundSeconds - 1 },
		"round high":        func(c *protocol.RoomConfig) { c.RoundSeconds = protocol.MaxRoundSeconds + 1 },
		"min players 1":     func(c *protocol.RoomConfig) { c.MinPlayers = protocol.MinMinPlayers - 1 },
		"min players 9":     func(c *protocol.RoomConfig) { c.MinPlayers = protocol.MaxMinPlayers + 1 },
		"maze size case":    func(c *protocol.RoomConfig) { c.MazeSize = "Small" },
		"maze size empty": func(c *protocol.RoomConfig) {
			c.MazeSize = ""
			c.Catchers = 3 // non-zero config so it is not replaced by the defaults
		},
	}
	for name, m := range bad {
		c := protocol.DefaultRoomConfig()
		m(&c)
		st, _, he := e.do("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: "p1", Config: c})
		if st != http.StatusBadRequest || he.Code != protocol.ErrBadRequest || he.Message == "" {
			t.Fatalf("%s: %d %+v", name, st, he)
		}
	}
	good := map[string]mut{
		"min bounds": func(c *protocol.RoomConfig) {
			c.MazeSize, c.Catchers, c.CatcherSpeed, c.RoundSeconds, c.MinPlayers = protocol.MazeSmall, protocol.MinCatchers, protocol.MinCatcherSpeed, protocol.MinRoundSeconds, protocol.MinMinPlayers
		},
		"max bounds": func(c *protocol.RoomConfig) {
			c.MazeSize, c.Catchers, c.CatcherSpeed, c.RoundSeconds, c.MinPlayers = protocol.MazeLarge, 32, protocol.MaxCatcherSpeed, protocol.MaxRoundSeconds, protocol.MaxMinPlayers
		},
		"five catchers":      func(c *protocol.RoomConfig) { c.Catchers = 5 },
		"seventeen catchers": func(c *protocol.RoomConfig) { c.Catchers = 17 },
		"fill bots":          func(c *protocol.RoomConfig) { c.FillBots = true },
	}
	for name, m := range good {
		c := protocol.DefaultRoomConfig()
		m(&c)
		r := e.mustOK("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: "p1", Config: c})
		if r.Room.Config != c {
			t.Fatalf("%s: config echoed as %+v", name, r.Room.Config)
		}
	}
	// Malformed bodies and blank names are handled.
	e.mustFail("POST", "/rooms", "not-an-object", http.StatusBadRequest, protocol.ErrBadRequest)
	e.mustFail("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: "   "}, http.StatusBadRequest, protocol.ErrBadRequest)
	r := e.mustOK("POST", "/rooms", protocol.MMCreateRoomRequest{PlayerID: "abcdefghij", Name: "   "})
	if r.Room.Players[0].Name != "Player-abcdef" {
		t.Fatalf("blank name fallback %q", r.Room.Players[0].Name)
	}
	long := e.mustOK("POST", "/rooms/"+r.Room.Code+"/join", protocol.MMJoinRoomRequest{PlayerID: "p2", Name: "abcdefghijklmnopqrstuvwxyz0123"})
	if n := long.Room.Players[1].Name; len(n) != 24 {
		t.Fatalf("name should be clamped to 24 chars, got %d", len(n))
	}
}

func TestRoomCodesAreUniqueAndAvoidAmbiguousLetters(t *testing.T) {
	e := newEnv(t)
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		code := e.create("p", cfg(2, false)).Room.Code
		if seen[code] {
			t.Fatalf("duplicate live code %s", code)
		}
		seen[code] = true
		for _, r := range code {
			if r == 'I' || r == 'O' {
				t.Fatalf("code %s uses an ambiguous letter", code)
			}
		}
	}
}

func TestEndOnLobbyRoomIsNoOpAndLeaveInMatchKeepsPhase(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", cfg(2, false)).Room.Code
	e.join(code, "p2")
	e.ready(code, "p1", true)
	r := e.mustOK("POST", "/rooms/"+code+"/end", protocol.MMMatchEndRequest{MatchID: "whatever"})
	if !r.Room.Players[0].Ready || r.Room.CountdownMS != 0 {
		t.Fatalf("end in lobby should not touch ready flags: %+v", r.Room)
	}
	e.start(code, "p1")
	e.svc.Wait()
	e.mustFail("POST", "/rooms/"+code+"/join", protocol.MMJoinRoomRequest{PlayerID: "p3"}, http.StatusConflict, protocol.ErrInMatch)
	// A player leaving mid-match does not drop the room out of the match.
	e.mustOK("POST", "/rooms/"+code+"/leave", protocol.MMPlayerRequest{PlayerID: "p2"})
	e.mustFail("POST", "/rooms/"+code+"/ready", protocol.MMReadyRequest{PlayerID: "p1", Ready: true}, http.StatusConflict, protocol.ErrInMatch)
	// An empty match_id from the gateway still ends the match.
	r = e.mustOK("POST", "/rooms/"+code+"/end", protocol.MMMatchEndRequest{})
	if len(r.Room.Players) != 1 || r.Room.Players[0].Ready {
		t.Fatalf("after end %+v", r.Room)
	}
	e.mustFail("POST", "/rooms/"+code+"/start", protocol.MMPlayerRequest{PlayerID: "p1"}, http.StatusBadRequest, protocol.ErrBadRequest)
}
