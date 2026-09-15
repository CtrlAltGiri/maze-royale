// Command gateway runs the gateway service on port 8080.
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
	port := flag.Int("port", protocol.PortGateway, "listen port")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := httpx.ListenAndServe(ctx, "gateway", *port, gateway.NewHandler()); err != nil {
		log.Fatalf("[gateway] %v", err)
	}
}
