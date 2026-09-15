// Command publicproxy runs a GET/HEAD-only allowlist reverse proxy in front of
// the gateway, so a public tunnel never reaches the gateway's unauthenticated
// internal endpoints.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/signal"
	"syscall"
	"time"

	"github.com/COG-GTM/riot-games-enablement-game/internal/protocol"
)

// allowed is the exact set of paths the proxy forwards upstream. Everything
// else is a 404, including the gateway's /internal/* endpoints.
var allowed = map[string]struct{}{
	"/":            {},
	"/index.html":  {},
	"/style.css":   {},
	"/lobby.js":    {},
	"/net.js":      {},
	"/game.js":     {},
	"/render.js":   {},
	"/ws":          {},
	"/leaderboard": {},
	"/healthz":     {},
}

func main() {
	port := flag.Int("port", 18090, "listen port")
	upstream := flag.String("upstream", protocol.GatewayURL, "gateway base URL")
	origin := flag.String("origin", "", "if set, required Origin header value for /ws")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	target, err := url.Parse(*upstream)
	if err != nil {
		log.Fatalf("[publicproxy] bad -upstream %q: %v", *upstream, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", *port),
		Handler:           handler(proxy, *origin),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Printf("[publicproxy] listening on http://127.0.0.1:%d -> %s", *port, *upstream)
		errc <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Fatalf("[publicproxy] %v", err)
		}
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[publicproxy] %v", err)
		}
	}
}

// handler enforces the path allowlist, the method allowlist and, when origin is
// non-empty, an exact Origin match on the websocket upgrade path.
func handler(proxy http.Handler, origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[r.URL.Path]; !ok {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/ws" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if origin != "" && r.Header.Get("Origin") != origin {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
