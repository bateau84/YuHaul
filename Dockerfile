# SPA build stage
FROM node:22-alpine AS spa-builder

WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci

COPY web/ ./
RUN npm run build

# Go build stage
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Install ca-certificates so go mod download can reach module proxies over TLS.
RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is not required: modernc.org/sqlite is a pure-Go SQLite driver.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/yuhaul ./cmd/server

# Runtime stage
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 yuhaul && \
    mkdir -p /data /app/web

COPY --from=builder /bin/yuhaul /app/yuhaul
COPY --from=spa-builder /web/dist /app/web

USER yuhaul

# The application serves the admin SPA and REST API on this port.
EXPOSE 8080

# Persist the SQLite database and any WAL/shm files here.
VOLUME ["/data"]

ENTRYPOINT ["/app/yuhaul"]
CMD ["-db-path=/data/yuhaul.db", "-http-addr=:8080", "-web-root=/app/web"]
