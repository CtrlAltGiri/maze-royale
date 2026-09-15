// Command devrun starts all five services in one terminal with prefixed logs.
// Usage: go run ./cmd/devrun   (or: make run)
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

// Start order matters only for log readability; services retry their peers.
var services = []string{"maze", "stats", "simulation", "matchmaking", "gateway"}

var colors = map[string]string{
	"maze":        "\033[36m",
	"stats":       "\033[35m",
	"simulation":  "\033[33m",
	"matchmaking": "\033[32m",
	"gateway":     "\033[34m",
}

const reset = "\033[0m"

func main() {
	log.SetFlags(0)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll("data", 0o755); err != nil {
		log.Fatalf("devrun: %v", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := false
	for _, name := range services {
		cmd := exec.CommandContext(ctx, "go", "run", "./cmd/"+name)
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=vendor")
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		out, err := cmd.StdoutPipe()
		if err != nil {
			log.Fatalf("devrun: %v", err)
		}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			log.Fatalf("devrun: start %s: %v", name, err)
		}
		wg.Add(2)
		go func(name string, r io.Reader) {
			defer wg.Done()
			prefix(name, r)
		}(name, out)
		go func(name string, cmd *exec.Cmd) {
			defer wg.Done()
			if err := cmd.Wait(); err != nil && ctx.Err() == nil {
				mu.Lock()
				failed = true
				mu.Unlock()
				log.Printf("%s[%-11s]%s exited: %v", colors[name], name, reset, err)
				stop()
			}
		}(name, cmd)
	}
	fmt.Println("devrun: all services starting; open http://localhost:8080  (Ctrl+C to stop)")
	wg.Wait()
	if failed {
		os.Exit(1)
	}
}

func prefix(name string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Printf("%s[%-11s]%s %s\n", colors[name], name, reset, sc.Text())
	}
}
