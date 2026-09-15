// Command stats runs the stats service on port 8094.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
	"github.com/COG-GTM/riot-games-enablement-game/internal/stats"
)

func main() {
	port := flag.Int("port", protocol.PortStats, "listen port")
	dataDir := flag.String("data", stats.DefaultDataDir, "directory holding stats.json")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	h, err := stats.NewHandlerWithDir(*dataDir)
	if err != nil {
		log.Fatalf("[stats] %v", err)
	}
	if err := httpx.ListenAndServe(ctx, "stats", *port, h); err != nil {
		log.Fatalf("[stats] %v", err)
	}
}
