# Build stage: compile frontend and Go binary
FROM --platform=$BUILDPLATFORM golang:1.27.4-bookworm AS builder

# Install Node.js for frontend build
RUN apt-get update && apt-get install -y --no-install-recommends \
    nodejs \
    npm \
    ca-certificates

WORKDIR /app

COPY . .

RUN cd server && go mod download
RUN cd client && npm ci
RUN cd client && npm run build
RUN cp -r client/frontend-build server/frontend-build

# Build Go binary (static, no cgo)
RUN cd server && CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/server ./cmd/server

# Runtime stage: distroless static nonroot
FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=builder /app/server/bin/server .
EXPOSE 8080
ENTRYPOINT ["./server"]