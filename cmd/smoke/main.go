// Command smoke drives one full round against a running stack (make run)
// with two headless websocket clients and exits non-zero if anything in the
// contract flow does not happen: welcome, room_state fanout, countdown,
// match_start, snapshots to both clients, a caught event, match_end and the
// result on the leaderboard.
//
// Usage: go run ./cmd/smoke [-gateway ws://localhost:8090]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
	"github.com/coder/websocket"
)

type client struct {
	name string
	conn *websocket.Conn
	id   string
	in   chan any
	raw  chan []byte
}

func dial(ctx context.Context, url, name string) *client {
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		log.Fatalf("%s: dial: %v", name, err)
	}
	conn.SetReadLimit(1 << 22)
	c := &client{name: name, conn: conn, in: make(chan any, 4096), raw: make(chan []byte, 4096)}
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				close(c.in)
				return
			}
			msg, err := protocol.Decode(data)
			if err != nil {
				log.Printf("%s: undecodable frame %s: %v", name, data, err)
				continue
			}
			c.in <- msg
		}
	}()
	return c
}

func (c *client) send(ctx context.Context, msg any) {
	b, err := protocol.Encode(msg)
	if err != nil {
		log.Fatalf("%s: encode: %v", c.name, err)
	}
	if err := c.conn.Write(ctx, websocket.MessageText, b); err != nil {
		log.Fatalf("%s: write: %v", c.name, err)
	}
}

// waitFor drains frames until pred returns true or the timeout elapses.
func (c *client) waitFor(what string, timeout time.Duration, pred func(any) bool) any {
	deadline := time.After(timeout)
	for {
		select {
		case msg, ok := <-c.in:
			if !ok {
				log.Fatalf("%s: connection closed while waiting for %s", c.name, what)
			}
			if e, isErr := msg.(*protocol.Error); isErr {
				log.Printf("%s: error frame: %s %s", c.name, e.Code, e.Message)
			}
			if pred(msg) {
				return msg
			}
		case <-deadline:
			log.Fatalf("%s: timed out waiting for %s", c.name, what)
		}
	}
}

func main() {
	gw := flag.String("gateway", "ws://localhost:8090", "gateway websocket base URL")
	httpGW := flag.String("http", protocol.GatewayURL, "gateway http base URL")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	a := dial(ctx, *gw, "alice")
	b := dial(ctx, *gw, "bob")

	a.send(ctx, protocol.Hello{T: protocol.TypeHello, Name: "alice"})
	b.send(ctx, protocol.Hello{T: protocol.TypeHello, Name: "bob"})
	a.id = a.waitFor("welcome", 5*time.Second, isType[*protocol.Welcome]).(*protocol.Welcome).PlayerID
	b.id = b.waitFor("welcome", 5*time.Second, isType[*protocol.Welcome]).(*protocol.Welcome).PlayerID
	log.Printf("welcome: alice=%s bob=%s", a.id, b.id)

	seed := int64(42)
	cfg := protocol.RoomConfig{MazeSize: protocol.MazeSmall, Catchers: 4, CatcherSpeed: 1.4, RoundSeconds: 30, MinPlayers: 2, Seed: &seed}
	a.send(ctx, protocol.CreateRoom{T: protocol.TypeCreateRoom, Config: cfg})
	rs := a.waitFor("room_state after create", 5*time.Second, isType[*protocol.RoomState]).(*protocol.RoomState)
	code := rs.Code
	log.Printf("room created: %s players=%d", code, len(rs.Players))

	b.send(ctx, protocol.JoinRoom{T: protocol.TypeJoinRoom, Code: code})
	b.waitFor("room_state after join", 5*time.Second, func(m any) bool {
		r, ok := m.(*protocol.RoomState)
		return ok && len(r.Players) == 2
	})
	a.waitFor("room_state fanout after join", 5*time.Second, func(m any) bool {
		r, ok := m.(*protocol.RoomState)
		return ok && len(r.Players) == 2
	})
	log.Printf("both clients see 2 players in room %s", code)

	a.send(ctx, protocol.SetReady{T: protocol.TypeSetReady, Ready: true})
	b.send(ctx, protocol.SetReady{T: protocol.TypeSetReady, Ready: true})
	for _, c := range []*client{a, b} {
		c.waitFor("countdown", 10*time.Second, func(m any) bool {
			switch v := m.(type) {
			case *protocol.RoomState:
				return v.CountdownMS > 0
			case *protocol.Event:
				return v.Kind == protocol.EventCountdown
			}
			return false
		})
	}
	log.Printf("countdown running on both clients")

	starts := map[*client]*protocol.MatchStart{}
	for _, c := range []*client{a, b} {
		ms := c.waitFor("match_start", 15*time.Second, isType[*protocol.MatchStart]).(*protocol.MatchStart)
		if ms.You.PlayerID != c.id || ms.You.Role != protocol.RoleRunner {
			log.Fatalf("%s: match_start you=%+v want player %s runner", c.name, ms.You, c.id)
		}
		if ms.Maze.W*ms.Maze.H != len(ms.Maze.Walls) || ms.Maze.W != 15 {
			log.Fatalf("%s: bad maze %dx%d walls=%d", c.name, ms.Maze.W, ms.Maze.H, len(ms.Maze.Walls))
		}
		if len(ms.Players) != 2 || len(ms.Catchers) != 4 {
			log.Fatalf("%s: match_start players=%d catchers=%d", c.name, len(ms.Players), len(ms.Catchers))
		}
		starts[c] = ms
	}
	if starts[a].MatchID != starts[b].MatchID {
		log.Fatalf("match ids differ: %s vs %s", starts[a].MatchID, starts[b].MatchID)
	}
	matchID := starts[a].MatchID
	log.Printf("match_start %s delivered to both (alice %s, bob %s)", matchID, starts[a].You.Color, starts[b].You.Color)

	// Inputs: alice walks, bob stands still. Snapshots must arrive within a tick.
	var seq int64
	dirs := []protocol.Direction{protocol.DirUp, protocol.DirLeft, protocol.DirDown, protocol.DirRight}
	a.send(ctx, protocol.Input{T: protocol.TypeInput, Seq: seq, Dir: protocol.DirUp})
	for _, c := range []*client{a, b} {
		t0 := time.Now()
		snap := c.waitFor("first snapshot", 2*time.Second, isType[*protocol.Snapshot]).(*protocol.Snapshot)
		log.Printf("%s: first snapshot tick=%d alive=%d time_left=%d after %s", c.name, snap.Tick, snap.AliveCount, snap.TimeLeftMS, time.Since(t0))
		next := c.waitFor("second snapshot", 2*time.Second, isType[*protocol.Snapshot]).(*protocol.Snapshot)
		if gap := time.Since(t0); gap > 4*time.Duration(protocol.TickMillis)*time.Millisecond {
			log.Fatalf("%s: consecutive snapshots %d..%d took %s (> tick budget)", c.name, snap.Tick, next.Tick, gap)
		}
	}

	moved := false
	caught := false
	var end *protocol.MatchEnd
	startPos := map[string][2]float64{}
	for _, p := range starts[a].Players {
		startPos[p.PlayerID] = [2]float64{p.X, p.Y}
	}
	inputTicker := time.NewTicker(400 * time.Millisecond)
	defer inputTicker.Stop()
	deadline := time.After(60 * time.Second)
	var bobEnd *protocol.MatchEnd
loop:
	for end == nil || bobEnd == nil {
		select {
		case <-inputTicker.C:
			seq++
			a.send(ctx, protocol.Input{T: protocol.TypeInput, Seq: seq, Dir: dirs[int(seq)%len(dirs)]})
		case msg, ok := <-a.in:
			if !ok {
				log.Fatalf("alice: connection closed mid match")
			}
			switch v := msg.(type) {
			case *protocol.Snapshot:
				for _, p := range v.Players {
					if p.ID == a.id && (p.X != startPos[a.id][0] || p.Y != startPos[a.id][1]) {
						moved = true
					}
				}
			case *protocol.Event:
				if v.Kind == protocol.EventCaught {
					caught = true
					log.Printf("caught: player=%s by %s at tick %d", v.PlayerID, v.CatcherID, v.Tick)
				}
			case *protocol.MatchEnd:
				end = v
			case *protocol.Error:
				log.Printf("alice: error frame %s %s", v.Code, v.Message)
			}
		case msg, ok := <-b.in:
			if !ok {
				log.Fatalf("bob: connection closed mid match")
			}
			if v, isEnd := msg.(*protocol.MatchEnd); isEnd {
				bobEnd = v
			}
		case <-deadline:
			break loop
		}
	}
	if end == nil || bobEnd == nil {
		log.Fatalf("no match_end on both clients (alice=%v bob=%v)", end != nil, bobEnd != nil)
	}
	if !moved {
		log.Fatalf("alice never moved from spawn despite inputs")
	}
	if !caught {
		log.Fatalf("no caught event during the round")
	}
	if end.MatchID != matchID || len(end.Standings) != 2 {
		log.Fatalf("bad match_end: %+v", end)
	}
	for _, s := range end.Standings {
		log.Printf("standing: place=%d %s (%s) survived=%dms", s.Place, s.Name, s.PlayerID, s.SurvivedMS)
	}
	log.Printf("winners: %v", end.WinnerIDs)

	// The room must return to the lobby (countdown 0, nobody ready) and be
	// startable again after the match.
	for _, c := range []*client{a, b} {
		c.waitFor("lobby room_state after match_end", 5*time.Second, func(m any) bool {
			r, ok := m.(*protocol.RoomState)
			if !ok || r.CountdownMS != 0 {
				return false
			}
			for _, p := range r.Players {
				if p.Ready {
					return false
				}
			}
			return true
		})
	}
	a.send(ctx, protocol.SetReady{T: protocol.TypeSetReady, Ready: true})
	b.send(ctx, protocol.SetReady{T: protocol.TypeSetReady, Ready: true})
	b.waitFor("second countdown", 10*time.Second, func(m any) bool {
		r, ok := m.(*protocol.RoomState)
		return ok && r.CountdownMS > 0
	})
	log.Printf("room %s re-armed for a second round", code)
	a.send(ctx, protocol.LeaveRoom{T: protocol.TypeLeaveRoom})
	b.send(ctx, protocol.LeaveRoom{T: protocol.TypeLeaveRoom})

	// Leaderboard: stats is written asynchronously by simulation; poll briefly.
	var body string
	for i := 0; i < 20; i++ {
		resp, err := http.Get(*httpGW + "/leaderboard")
		if err != nil {
			log.Fatalf("leaderboard: %v", err)
		}
		bb, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body = string(bb)
		if resp.StatusCode == 200 && strings.Contains(body, "alice") && strings.Contains(body, "bob") {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !strings.Contains(body, "alice") || !strings.Contains(body, "bob") {
		log.Fatalf("leaderboard does not list both players:\n%s", body)
	}
	resp, err := http.Get(strings.Replace(*httpGW, strconv.Itoa(protocol.PortGateway), strconv.Itoa(protocol.PortStats), 1) + "/leaderboard")
	if err == nil {
		var lb protocol.LeaderboardResponse
		_ = json.NewDecoder(resp.Body).Decode(&lb)
		resp.Body.Close()
		log.Printf("stats leaderboard: total=%d entries=%+v", lb.Total, lb.Entries)
	}
	fmt.Println("SMOKE OK: full round played, caught event seen, match_end on both clients, leaderboard updated")
	a.conn.Close(websocket.StatusNormalClosure, "done")
	b.conn.Close(websocket.StatusNormalClosure, "done")
	os.Exit(0)
}

func isType[T any](m any) bool {
	_, ok := m.(T)
	return ok
}
