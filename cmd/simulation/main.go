// Command simulation runs the simulation service on port 8082.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
	"github.com/COG-GTM/riot-games-enablement-game/internal/simulation"
)

func main() {
	port := flag.Int("port", protocol.PortSimulation, "listen port")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := httpx.ListenAndServe(ctx, "simulation", *port, simulation.NewHandler()); err != nil {
		log.Fatalf("[simulation] %v", err)
	}
}
