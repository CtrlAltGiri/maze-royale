---
description: Keep Maze Royale Go changes consistent with the repository
trigger: always_on
---

# Go conventions

- Follow neighboring Go code and format changed Go files with `gofmt`.
- Dependencies are vendored; use `-mod=vendor` for direct Go commands.
- For Go code changes, run the relevant package tests. Use `make test` and `make vet` for a full check when appropriate.
- Preserve unrelated working-tree changes.
