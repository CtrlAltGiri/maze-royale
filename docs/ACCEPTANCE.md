# Acceptance test — the only definition of done

Run `make run`, then open four browser tabs at `http://localhost:8080`.

1. Tab 1 creates a room with 21x21, 2 catchers, speed 1.0, 120 seconds,
   minimum 3 players, bots off. It gets a join code.
2. Tabs 2, 3, and 4 join with that code. Every tab shows the same roster with
   four distinct colors.
3. All ready, countdown runs, the match starts in all four tabs at once.
4. Each tab moves only its own runner. Within one tick, all four tabs render
   every runner at the same position.
5. A catcher reaching a runner eliminates that runner in all four tabs
   simultaneously, the death animation plays in the eliminated tab, and that
   tab switches to spectator.
6. When one runner remains, every tab shows the same winner and the same
   standings.
7. The leaderboard at `http://localhost:8080/leaderboard` reflects the result.
8. Killing a tab mid match and reopening it rejoins as a spectator without
   desyncing anyone.
9. `go vet ./...` and `go test ./...` pass. The whole thing runs with
   networking disabled after the vendor step (`GOFLAGS=-mod=vendor`).

Report pass or fail for every step with evidence (screenshots or log lines).
