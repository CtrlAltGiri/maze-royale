# Maze Royale

Top down, multiplayer, last runner standing maze game. Runs entirely on one
laptop; players use separate browser tabs on localhost.

```
make run          # start all five services (http://localhost:8090)
make test         # go test ./...
make vet          # go vet ./...
```

Requires Go 1.24. Dependencies are vendored; no network is needed to build or run.

- Rules, config, and the wire contract: `docs/CONTRACT.md`
- Service layout and the path of a keypress: `docs/ARCHITECTURE.md`
- Definition of done: `docs/ACCEPTANCE.md`
