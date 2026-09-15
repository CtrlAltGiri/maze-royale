---
name: game-tour
description: Explain Maze Royale architecture or a specific game subsystem
argument-hint: "[topic, e.g. matchmaking]"
agent: game-guide
triggers:
  - user
---

Give a read-only tour of Maze Royale using the game-guide profile.

1. If the user supplies a topic, focus on that subsystem. Otherwise, summarize the service layout and trace a player input through the system.
2. Read README.md and docs/ARCHITECTURE.md, then inspect the relevant files under cmd/, internal/, and web/ to verify the explanation.
3. Return a short overview, the key files with line references, and the local run/test commands documented in the Makefile. Describe commands without executing them.

Do not modify files or start services.
