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
RUN cd server && CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/filetoaster ./cmd/server

# Runtime stage: Alpine with su-exec for PUID/PGID support
FROM alpine:3.24

# Install su-exec for privilege dropping
RUN apk add --no-cache su-exec

WORKDIR /app

COPY --from=builder /app/server/bin/filetoaster .
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
