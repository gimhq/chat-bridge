package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

func TestIdentityEventMovesChatAndContact(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	connected(t, c, f, "a1")
	if err := f.Push(ctx, "a1", inbound("m1", "u-phone", "u-phone", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := f.Push(ctx, "a1", adapter.Event{Kind: adapter.EvIdentity, UserID: "u-phone", NewID: "u-lid"},
		inbound("m2", "u-lid", "u-lid", "again")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.st.GetChat(ctx, "a1", "u-phone"); err == nil {
		t.Fatal("old chat kept")
	}
	msgs, _, err := c.st.ListMessages(ctx, "a1", "u-lid", "", time.Time{}, time.Time{}, 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("messages under new id: %d %v", len(msgs), err)
	}
	ct, err := c.st.GetContact(ctx, "a1", "u-lid")
	if err != nil || ct.Phone != "+1" {
		t.Fatalf("contact: %+v %v", ct, err)
	}
	evs, err := c.st.ListEvents(ctx, 0, store.EventFilter{AccountID: "a1"}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var merged, contact bool
	for _, e := range evs {
		merged = merged || (e.Type == model.EvChatUpdated && strings.Contains(string(e.Data), `"merged_from":"u-phone"`))
		contact = contact || (e.Type == model.EvContactUpdated && strings.Contains(string(e.Data), `"id":"u-lid"`))
	}
	if !merged || !contact {
		t.Fatalf("events: merged=%t contact=%t", merged, contact)
	}
}

func TestIdentitiesReconciledOnConnect(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	connected(t, c, f, "a1")
	if err := f.Push(ctx, "a1", inbound("m1", "u2", "u2", "hello")); err != nil {
		t.Fatal(err)
	}
	f.Identities = map[string]string{"u2": "u2-lid"}
	if err := f.Connect(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		_, errOld := c.st.GetChat(ctx, "a1", "u2")
		_, errNew := c.st.GetChat(ctx, "a1", "u2-lid")
		return errOld != nil && errNew == nil
	})
}
