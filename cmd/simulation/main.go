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
	mazeURL := flag.String("maze-url", "", "maze service base URL (default $SIM_MAZE_URL or "+protocol.MazeURL+")")
	gatewayURL := flag.String("gateway-url", "", "gateway base URL (default $SIM_GATEWAY_URL or "+protocol.GatewayURL+")")
	statsURL := flag.String("stats-url", "", "stats service base URL (default $SIM_STATS_URL or "+protocol.StatsURL+")")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc := simulation.New(simulation.Config{MazeURL: *mazeURL, GatewayURL: *gatewayURL, StatsURL: *statsURL})
	if err := httpx.ListenAndServe(ctx, "simulation", *port, svc.Handler()); err != nil {
		log.Fatalf("[simulation] %v", err)
	}
}
