// Package stats persists match results to ./data/stats.json and serves the
// leaderboard read model. Port 8084.
package stats

import (
	"net/http"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
)

// NewHandler returns the service's HTTP handler. Routes are stubbed with 501
// until the service is implemented; see docs/CONTRACT.md for the shapes.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("stats"))
	mux.HandleFunc("POST /results", httpx.NotImplemented)
	mux.HandleFunc("GET /leaderboard", httpx.NotImplemented)
	return mux
}
