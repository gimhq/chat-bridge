//go:build signal

package main

import (
	"log/slog"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/signal"
	"gimhq/chat-bridge/internal/config"
)

// `-tags signal` adds the cgo-backed Signal adapter.
func init() {
	extraAdapters = append(extraAdapters, func(log *slog.Logger, _ config.Config) adapter.Adapter {
		return signal.New(log.With("adapter", "signal"))
	})
}
