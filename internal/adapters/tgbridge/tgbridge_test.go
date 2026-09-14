//go:build tgbridge

package tgbridge

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

type statusSink struct {
	mu   sync.Mutex
	last string
}

func (s *statusSink) Status(_ context.Context, _ string, st adapter.Status) error {
	s.mu.Lock()
	s.last = st.Status
	s.mu.Unlock()
	return nil
}
func (s *statusSink) LoginStep(context.Context, string, model.LoginStep) error { return nil }
func (s *statusSink) Events(context.Context, string, []adapter.Event) error    { return nil }
func (s *statusSink) PutMedia(context.Context, string, string, adapter.MediaMeta, io.Reader) (model.Attachment, error) {
	return model.Attachment{}, nil
}

func TestHostedTelegram(t *testing.T) {
	ctx := context.Background()
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 12345, "hash")
	info := a.Info()
	if info.Platform != "telegram" || info.Instance != Instance || !info.Has(adapter.CapEdit) || !info.Has(adapter.CapReaction) {
		t.Fatalf("info: %+v", info)
	}
	flows := map[string]bool{}
	for _, f := range info.LoginFlows {
		flows[f.ID] = true
	}
	if !flows["phone"] || !flows["qr"] {
		t.Fatalf("login flows: %+v", info.LoginFlows)
	}

	// The real connector initialises against the virtual homeserver (database upgrades, command
	// handlers, server defaults in its config) without contacting Telegram.
	sink := &statusSink{}
	if err := a.Start(ctx, sink); err != nil {
		t.Fatal(err)
	}
	if err := a.AddAccount(ctx, "tg1", nil, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		sink.mu.Lock()
		last := sink.last
		sink.mu.Unlock()
		if last == model.StatusUnpaired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status: %q", last)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := a.RemoveAccount(ctx, "tg1"); err != nil {
		t.Fatal(err)
	}
}
