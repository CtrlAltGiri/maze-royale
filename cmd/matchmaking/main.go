// Command matchmaking runs the matchmaking service on port 8081.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/matchmaking"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

func main() {
	port := flag.Int("port", protocol.PortMatchmaking, "listen port")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := httpx.ListenAndServe(ctx, "matchmaking", *port, matchmaking.NewHandler()); err != nil {
		log.Fatalf("[matchmaking] %v", err)
	}
}
