# FileToaster monorepo Makefile (single, self-contained).
#
# Layout:
#   server/  — Go backend (embeds the built frontend).
#   client/  — SvelteKit + TypeScript frontend.
#
# The Go binary embeds the built frontend. Because go:embed cannot reference
# paths outside its package, the client build output (client/frontend-build) is
# staged into server/frontend-build before the Go binary is compiled.

.PHONY: frontend build run test vet clean all

# Make sure the Go toolchain (and npm) are reachable and avoid a toolchain
# re-download when invoking go from these recipes.
export PATH := /usr/local/go/bin:$(PATH)
export GOTOOLCHAIN := local

all: build

# Build the client (SvelteKit) static assets into client/frontend-build.
frontend:
	cd client && npm run build

# Build the client, stage its output for embedding, and compile the binary.
build: frontend
	rm -rf server/frontend-build
	cp -r client/frontend-build server/frontend-build
	cd server && go build -o bin/server ./cmd/server

# Build the client and run the server.
run: frontend
	cd server && go run ./cmd/server

# Run the Go tests.
test:
	cd server && go test ./...

# Vet the Go source.
vet:
	cd server && go vet ./...

# Remove all build artifacts.
clean:
	rm -rf server/bin
	rm -rf server/frontend-build
	rm -rf client/frontend-build
	rm -rf client/.svelte-kit
