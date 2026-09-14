//go:build tgbridge

// Package tgbridge exposes mautrix-telegram's bridgev2 connector as a second Telegram adapter
// instance next to the gotd one. The connector encodes webp in C (bundled libwebp), so it needs
// cgo and is only compiled with `-tags tgbridge`.
package tgbridge

import (
	"log/slog"

	"gopkg.in/yaml.v3"
	"maunium.net/go/mautrix/bridgev2"

	tgconn "go.mau.fi/mautrix-telegram/pkg/connector"

	"gimhq/chat-bridge/internal/adapters/connector"
)

// Platform is shared with the gotd adapter; Instance tells the two apart.
const (
	Platform = "telegram"
	Instance = "bridgev2"
)

// New returns the hosted Telegram adapter. apiID / apiHash are the server-wide application
// credentials (adapters.telegram.*); an account's `config.network` may override them.
func New(log *slog.Logger, apiID int, apiHash string) *connector.Adapter {
	// Animated stickers stay gzipped lottie: the image has no lottieconverter / ffmpeg.
	defaults := map[string]any{"animated_sticker": map[string]any{"target": "disable"}}
	if apiID != 0 {
		defaults["api_id"] = apiID
	}
	if apiHash != "" {
		defaults["api_hash"] = apiHash
	}
	doc, _ := yaml.Marshal(defaults)
	return connector.New(log, Platform, func() bridgev2.NetworkConnector { return &tgconn.TelegramConnector{} },
		connector.WithInstance(Instance), connector.WithProbe(&tgconn.TelegramClient{}), connector.WithNetworkDefaults(string(doc)))
}
