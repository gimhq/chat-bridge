//go:build !signal

package main

import (
	"log/slog"

	"gimhq/chat-bridge/internal/adapter"
)

// extraAdapters is empty in the pure-Go build; `-tags signal` adds Signal.
func extraAdapters(*slog.Logger) []adapter.Adapter { return nil }
