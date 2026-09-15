// Command maze runs the maze service on port 8093.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/COG-GTM/riot-games-enablement-game/internal/httpx"
	"github.com/COG-GTM/riot-games-enablement-game/internal/mazegen"
	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

func main() {
	port := flag.Int("port", protocol.PortMaze, "listen port")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := httpx.ListenAndServe(ctx, "maze", *port, mazegen.NewHandler()); err != nil {
		log.Fatalf("[maze] %v", err)
	}
}
