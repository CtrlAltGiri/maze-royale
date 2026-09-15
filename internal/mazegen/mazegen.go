// Package mazegen generates seeded, validated mazes (recursive backtracker) and
// serves them over HTTP on port 8083.
package mazegen

import (
	"net/http"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
)

// NewHandler returns the service's HTTP handler. Routes are stubbed with 501
// until the service is implemented; see docs/CONTRACT.md for the shapes.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("maze"))
	mux.HandleFunc("POST /generate", httpx.NotImplemented)
	return mux
}
