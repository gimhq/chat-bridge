//go:build tgbridge

package main

import (
	"log/slog"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/tgbridge"
	"gimhq/chat-bridge/internal/config"
)

// `-tags tgbridge` adds mautrix-telegram's connector as the `telegram/bridgev2` instance, registered
// only when adapters.telegram.bridgev2 is on so single-instance deployments keep working.
func init() {
	extraAdapters = append(extraAdapters, func(log *slog.Logger, cfg config.Config) adapter.Adapter {
		tg := cfg.Adapters.Telegram
		if !tg.Bridgev2 {
			return nil
		}
		return tgbridge.New(log.With("adapter", tgbridge.Platform, "instance", tgbridge.Instance), tg.APIID, tg.APIHash)
	})
}
