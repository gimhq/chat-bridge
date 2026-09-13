//go:build signal

// Package signal exposes mautrix-signal's bridgev2 connector as a chat-bridge adapter. It links
// libsignal through cgo, so it is only compiled with `-tags signal`; see scripts/build-libsignal.sh.
package signal

import (
	"log/slog"

	"maunium.net/go/mautrix/bridgev2"

	sigconn "go.mau.fi/mautrix-signal/pkg/connector"

	"gimhq/chat-bridge/internal/adapters/connector"
)

// Platform is the platform id under which Signal accounts are created.
const Platform = "signal"

// New returns the hosted Signal adapter.
func New(log *slog.Logger) *connector.Adapter {
	return connector.New(log, Platform, func() bridgev2.NetworkConnector { return &sigconn.SignalConnector{} },
		connector.WithProbe(&sigconn.SignalClient{}))
}
