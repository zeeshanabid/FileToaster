# FileToaster

A SvelteKit UI (draggable/floatable file windows) fronting a Go server that runs
all file operations as tracked async tasks, exposed over stateless HTTP + SSE,
packaged as a single embedded binary.

## Monorepo layout

```
FileToaster/
├── server/   Go backend (embeds the built frontend)
├── client/   SvelteKit + TypeScript + TailwindCSS frontend
├── Makefile  self-contained build orchestrator
└── AGENTS.md / ARCHITECTURE.md / TASKS.md
```

## Prerequisites

- Go 1.27.3+ (add to PATH): `export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local`
- `make` (GNU Make 4.3)
- Node 18+ / npm (for the client)

## Build, run, clean

| Task | Command |
| ---- | ------- |
| Build everything (client assets + embedded server binary) | `make build` |
| Build only the client (`client/frontend-build`) | `make frontend` |
| Build the client and run the server | `make run` |
| Run the Go tests | `make test` |
| Vet the Go source | `make vet` |
| Remove all build artifacts | `make clean` |

`make build` builds the client, stages its output into `server/frontend-build`,
and compiles the embedded binary into `server/bin/server`.

To run the binary directly (after `make build`):

```sh
export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local
FILETOASTER_SESSION_SECRET=change-me ./server/bin/server
# then open http://localhost:8080
```

## Development

```sh
# Go (from the server module root)
cd server && go test ./... && go vet ./...

# Frontend
cd client && npm install && npm run build   # build
cd client && npm run check                  # svelte-check (0 errors / 0 warnings)
```

## Configuration

Runtime settings come from a YAML file resolved in this order:
`--config-file <path>` → `FILETOASTER_CONFIG` env → `./config.yaml` →
`~/.filetoaster/config.yaml` (auto-created on first run if none exist).
See [`server/config.example.yaml`](server/config.example.yaml).

Required env: `FILETOASTER_SESSION_SECRET`. Optional: `FILETOASTER_ADMIN_PASSWORD`,
`FILETOASTER_ADDR`, `FILETOASTER_GOMAXPROCS`, `FILETOASTER_MAX_TASKS`,
`FILETOASTER_TMP_DIR`, `FILETOASTER_CONFIG`.

## Status

Milestones 0–1 (config) + Frontend POC implemented. Auth, the task engine, FS
ops, compression, upload, protocol, the full window manager/DnD, and packaging
are not yet implemented. See `TASKS.md`.
