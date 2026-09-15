// Package gateway serves the static client, terminates player websockets at /ws,
// routes intents to matchmaking and simulation, and fans snapshots out per
// room. Port 8080.
package gateway

import (
	"net/http"

	"github.com/coder/websocket"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
)

// AcceptOptions is the websocket accept configuration for /ws. Localhost only,
// so origin checking is disabled.
var AcceptOptions = &websocket.AcceptOptions{InsecureSkipVerify: true}

// NewHandler returns the service's HTTP handler. Routes are stubbed with 501
// until the service is implemented; see docs/CONTRACT.md for the shapes.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("gateway"))
	mux.HandleFunc("GET /ws", httpx.NotImplemented)
	mux.HandleFunc("POST /internal/push", httpx.NotImplemented)
	mux.HandleFunc("GET /leaderboard", httpx.NotImplemented)
	mux.Handle("/", http.FileServer(http.Dir("web")))
	return mux
}
