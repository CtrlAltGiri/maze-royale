// Package simulation runs the authoritative 20Hz tick loop per match: movement,
// collision, catchers, elimination, win condition. Port 8082.
package simulation

import (
	"net/http"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
)

// NewHandler returns the service's HTTP handler. Routes are stubbed with 501
// until the service is implemented; see docs/CONTRACT.md for the shapes.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("simulation"))
	mux.HandleFunc("POST /matches", httpx.NotImplemented)
	mux.HandleFunc("GET /matches/{id}", httpx.NotImplemented)
	mux.HandleFunc("POST /matches/{id}/input", httpx.NotImplemented)
	return mux
}
