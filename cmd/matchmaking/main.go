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
	defaults := matchmaking.DefaultOptions()
	port := flag.Int("port", protocol.PortMatchmaking, "listen port")
	gatewayURL := flag.String("gateway-url", defaults.GatewayURL, "gateway base URL (env "+matchmaking.EnvGatewayURL+")")
	simulationURL := flag.String("simulation-url", defaults.SimulationURL, "simulation base URL (env "+matchmaking.EnvSimulationURL+")")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc := matchmaking.NewService(matchmaking.Options{GatewayURL: *gatewayURL, SimulationURL: *simulationURL})
	if err := httpx.ListenAndServe(ctx, "matchmaking", *port, svc.Handler()); err != nil {
		log.Fatalf("[matchmaking] %v", err)
	}
	svc.Wait()
}
