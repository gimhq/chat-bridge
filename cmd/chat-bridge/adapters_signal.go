//go:build signal

package main

import (
	"log/slog"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/signal"
)

// extraAdapters adds the cgo-backed Signal adapter when built with `-tags signal`.
func extraAdapters(log *slog.Logger) []adapter.Adapter {
	return []adapter.Adapter{signal.New(log.With("adapter", "signal"))}
}
