// Package matchmaking owns rooms: join codes, config validation, ready state, start
// criteria, countdown, role and color assignment. Port 8081.
package matchmaking

import (
	"net/http"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
)

// NewHandler returns the service's HTTP handler. Routes are stubbed with 501
// until the service is implemented; see docs/CONTRACT.md for the shapes.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Health("matchmaking"))
	mux.HandleFunc("POST /rooms", httpx.NotImplemented)
	mux.HandleFunc("GET /rooms/{code}", httpx.NotImplemented)
	mux.HandleFunc("POST /rooms/{code}/join", httpx.NotImplemented)
	mux.HandleFunc("POST /rooms/{code}/leave", httpx.NotImplemented)
	mux.HandleFunc("POST /rooms/{code}/ready", httpx.NotImplemented)
	mux.HandleFunc("POST /rooms/{code}/start", httpx.NotImplemented)
	return mux
}
