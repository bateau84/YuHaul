# SPA build stage
FROM node:22-alpine AS spa-builder

WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci

COPY web/ ./
# Vite compiles import.meta.env.VITE_USE_MOCK into the bundle at build time;
# runtime container env cannot change it. The shipped image must use the real
# API/SSE clients, while local development keeps the mock default.
RUN VITE_USE_MOCK=false npm run build

# Go build stage
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Install ca-certificates so go mod download can reach module proxies over TLS.
RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

# CGO is not required: modernc.org/sqlite is a pure-Go SQLite driver.
# VERSION/COMMIT/BUILT_AT are injected as the build identity served at
# GET /api/version; they default to "unknown" when not passed.
ARG VERSION=unknown
ARG COMMIT=unknown
ARG BUILT_AT=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w \
      -X github.com/bateau84/YuHaul/internal/buildinfo.Version=${VERSION} \
      -X github.com/bateau84/YuHaul/internal/buildinfo.Commit=${COMMIT} \
      -X github.com/bateau84/YuHaul/internal/buildinfo.BuiltAt=${BUILT_AT}" \
    -o /bin/yuhaul ./cmd/server

# Runtime stage
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 yuhaul && \
    mkdir -p /data /app/web && \
    chown 1000:1000 /data

COPY --from=builder /bin/yuhaul /app/yuhaul
COPY --from=spa-builder /web/dist /app/web

USER yuhaul

# The application serves the admin SPA and REST API on this port.
EXPOSE 8080

# Persist the SQLite database and any WAL/shm files here.
VOLUME ["/data"]

ENTRYPOINT ["/app/yuhaul"]
CMD ["-db-path=/data/yuhaul.db", "-http-addr=:8080", "-web-root=/app/web"]
