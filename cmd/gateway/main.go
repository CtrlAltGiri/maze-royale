// Command gateway runs the gateway service on port 8090.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/COG-GTM/riot-games-enablement-game/internal/gateway"
	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

func main() {
	def := gateway.ConfigFromEnv()
	port := flag.Int("port", protocol.PortGateway, "listen port")
	cfg := gateway.Config{}
	flag.StringVar(&cfg.MatchmakingURL, "matchmaking", def.MatchmakingURL, "matchmaking base URL ($MATCHMAKING_URL)")
	flag.StringVar(&cfg.SimulationURL, "simulation", def.SimulationURL, "simulation base URL ($SIMULATION_URL)")
	flag.StringVar(&cfg.StatsURL, "stats", def.StatsURL, "stats base URL ($STATS_URL)")
	flag.StringVar(&cfg.WebDir, "web", def.WebDir, "static files directory ($WEB_DIR)")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := httpx.ListenAndServe(ctx, "gateway", *port, gateway.NewHandlerWithConfig(cfg)); err != nil {
		log.Fatalf("[gateway] %v", err)
	}
}
