# syntax=docker/dockerfile:1.7

# ---- libsignal stage (Rust) ---------------------------------------------------
# The Signal adapter hosts mautrix-signal's connector, which links libsignal through cgo. The
# Rust half is built here once per MAUTRIX_SIGNAL_VERSION and cached as a layer.
FROM rust:1-alpine AS libsignal
ARG MAUTRIX_SIGNAL_VERSION=v0.2608.0
RUN apk add --no-cache git make cmake protoc musl-dev g++ clang-dev protobuf-dev
WORKDIR /build
RUN git clone --depth 1 --branch "${MAUTRIX_SIGNAL_VERSION}" https://github.com/mautrix/signal.git . \
    && ./build-rust.sh \
    && cp pkg/libsignalgo/libsignal/target/*/libsignal_ffi.a /libsignal_ffi.a

# ---- web UI stage (bun) ------------------------------------------------------------
# Builds web/ into internal/webui/dist, which the Go binary embeds.
FROM oven/bun:1-alpine AS web
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN mkdir -p ../internal/webui/dist && bun run typecheck && bun run build

# ---- build stage --------------------------------------------------------------
FROM golang:1.26-alpine AS build
RUN apk add --no-cache build-base zlib-dev
WORKDIR /src

# cgo is needed for libsignal and mautrix-telegram's bundled libwebp (`tgbridge`); everything else
# stays pure Go (`goolm` for Matrix E2EE).
ENV CGO_ENABLED=1 GOTOOLCHAIN=local GOFLAGS=-trimpath LIBRARY_PATH=/usr/local/lib
COPY --from=libsignal /libsignal_ffi.a /usr/local/lib/

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist

# `docker build --target test .` runs the unit tests inside the same toolchain.
FROM build AS test
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet -tags goolm,signal,tgbridge ./... && go test -tags goolm,signal,tgbridge ./...

FROM build AS compile
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -tags goolm,signal,tgbridge -ldflags="-s -w -X main.version=${VERSION}" -o /out/chat-bridge ./cmd/chat-bridge \
    && mkdir -p /empty

# ---- runtime stage ------------------------------------------------------------
# alpine rather than distroless: the cgo binary needs musl, libstdc++ and zlib at runtime.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata libstdc++ zlib \
    && addgroup -g 65532 nonroot && adduser -D -u 65532 -G nonroot nonroot
COPY --from=compile /out/chat-bridge /chat-bridge
# Pre-create the data directory owned by the nonroot user so named volumes inherit it.
COPY --from=compile --chown=nonroot:nonroot /empty /data
ENV CHATBRIDGE_SERVER_ADDR=:8080 CHATBRIDGE_STORAGE_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
USER nonroot
ENTRYPOINT ["/chat-bridge"]
