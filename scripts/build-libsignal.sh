#!/usr/bin/env bash
# Builds libsignal_ffi.a (the Rust half of mautrix-signal) in a throwaway container and drops it
# in .tmp/libsignal/ so a developer machine can `go build -tags goolm,signal` without a Rust
# toolchain. Pick an image whose libc matches the host: rust:1-bookworm for glibc (default),
# rust:1-alpine for musl. The Go link step also needs zlib and libstdc++ development files.
#
#   scripts/build-libsignal.sh
#   CGO_LDFLAGS="-L$PWD/.tmp/libsignal" go build -tags goolm,signal ./...
set -euo pipefail

VERSION="${MAUTRIX_SIGNAL_VERSION:-v0.2608.0}"
IMAGE="${LIBSIGNAL_IMAGE:-rust:1-bookworm}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/.tmp/libsignal"
mkdir -p "$OUT/cargo"

case "$IMAGE" in
  *alpine*) PREP='apk add --no-cache git make cmake protoc musl-dev g++ clang-dev protobuf-dev' ;;
  *)        PREP='apt-get update -qq && apt-get install -y -qq --no-install-recommends git make cmake protobuf-compiler clang libclang-dev g++ >/dev/null' ;;
esac

docker run --rm --label ai-agent=true --name "ai-agent-chat-bridge-libsignal-$$" \
  -v "$OUT:/out" -v "$OUT/cargo:/usr/local/cargo/registry" \
  -e VERSION="$VERSION" -e PREP="$PREP" "$IMAGE" sh -euc '
    eval "$PREP"
    git clone --depth 1 --branch "$VERSION" https://github.com/mautrix/signal.git /build
    cd /build && ./build-rust.sh
    cp pkg/libsignalgo/libsignal/target/*/libsignal_ffi.a /out/
    chown "$(stat -c %u /out):$(stat -c %g /out)" /out/libsignal_ffi.a
  '
echo "libsignal_ffi.a written to $OUT (mautrix-signal $VERSION, image $IMAGE)"
