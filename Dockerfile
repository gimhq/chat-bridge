# syntax=docker/dockerfile:1.7

# ---- build stage --------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src

ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-trimpath

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# `docker build --target test .` runs the unit tests inside the same toolchain.
FROM build AS test
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet -tags goolm ./... && go test -tags goolm ./...

FROM build AS compile
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -tags goolm -ldflags="-s -w -X main.version=${VERSION}" -o /out/chat-bridge ./cmd/chat-bridge \
    && mkdir -p /empty

# ---- runtime stage ------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=compile /out/chat-bridge /chat-bridge
# Pre-create the data directory owned by the nonroot user so named volumes inherit it.
COPY --from=compile --chown=nonroot:nonroot /empty /data
ENV CHATBRIDGE_SERVER_ADDR=:8080 CHATBRIDGE_STORAGE_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
USER nonroot
ENTRYPOINT ["/chat-bridge"]
