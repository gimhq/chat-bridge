package server

import (
	"context"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

func TestContactViewEndpoints(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connected("a1")
	now := time.Now().UTC()
	_ = e.fake.Push(ctx, "a1",
		adapter.Event{Kind: adapter.EvMessage,
			Message: &model.Message{ID: "m1", ChatID: "u1@fake", Sender: model.Sender{ID: "u1@fake"}, Timestamp: now, Content: model.Content{Type: "text", Text: "hello"}},
			Chat:    &model.Chat{ID: "u1@fake", Kind: model.ChatDirect},
			Sender:  &model.Contact{ID: "u1@fake", Names: model.Names{Profile: "Alice"}}},
		adapter.Event{Kind: adapter.EvMessage,
			Message: &model.Message{ID: "g1", ChatID: "team@fake", Sender: model.Sender{ID: "u1@fake"}, Timestamp: now, Content: model.Content{Type: "text", Text: "team"}},
			Chat:    &model.Chat{ID: "team@fake", Kind: model.ChatGroup, Name: "Team"}},
		adapter.Event{Kind: adapter.EvRequest, Request: &adapter.Request{Key: "call:1", Kind: model.RequestKindCall, FromID: "u1@fake", ChatID: "u1@fake", CreatedAt: now}},
	)

	if rec, out := e.do("GET", "/v1/accounts/a1/contacts/u1@fake/chats", nil); rec.Code != 200 || len(out["chats"].([]any)) != 2 {
		t.Fatalf("chats: %d %v", rec.Code, out)
	}
	if rec, out := e.do("GET", "/v1/accounts/a1/contacts/u1@fake/messages", nil); rec.Code != 200 || len(out["messages"].([]any)) != 1 {
		t.Fatalf("direct messages: %d %v", rec.Code, out)
	}
	if rec, out := e.do("GET", "/v1/accounts/a1/contacts/u1@fake/messages?scope=all", nil); rec.Code != 200 || len(out["messages"].([]any)) != 2 {
		t.Fatalf("all messages: %d %v", rec.Code, out)
	}
	if rec, _ := e.do("GET", "/v1/accounts/a1/contacts/u1@fake/messages?scope=bogus", nil); rec.Code != 400 {
		t.Fatalf("bad scope: %d", rec.Code)
	}
	if rec, _ := e.do("GET", "/v1/accounts/a1/contacts/nobody/chats", nil); rec.Code != 404 {
		t.Fatalf("unknown contact: %d", rec.Code)
	}
	if rec, out := e.do("GET", "/v1/accounts/a1/requests?from=u1@fake", nil); rec.Code != 200 || len(out["requests"].([]any)) != 1 {
		t.Fatalf("requests from: %d %v", rec.Code, out)
	}
	if rec, out := e.do("GET", "/v1/accounts/a1/requests?from=someone-else", nil); rec.Code != 200 || len(out["requests"].([]any)) != 0 {
		t.Fatalf("requests from other: %d %v", rec.Code, out)
	}
}

func TestMessageListRaw(t *testing.T) {
	e := newEnv(t)
	e.connected("a1")
	if err := e.fake.Push(context.Background(), "a1", adapter.Event{Kind: adapter.EvMessage,
		Message: &model.Message{ID: "m1", ChatID: "u1@fake", Sender: model.Sender{ID: "u1@fake"}, Timestamp: time.Now().UTC(), Content: model.Content{Type: "text", Text: "hello"}},
		Chat:    &model.Chat{ID: "u1@fake", Kind: model.ChatDirect},
		Raw:     []byte(`{"platform":"payload"}`)}); err != nil {
		t.Fatal(err)
	}
	first := func(path string) map[string]any {
		rec, out := e.do("GET", path, nil)
		if rec.Code != 200 || len(out["messages"].([]any)) != 1 {
			t.Fatalf("%s: %d %v", path, rec.Code, out)
		}
		return out["messages"].([]any)[0].(map[string]any)
	}
	if m := first("/v1/accounts/a1/chats/u1@fake/messages"); m["raw"] != nil {
		t.Fatalf("raw must be off by default: %v", m)
	}
	if m := first("/v1/accounts/a1/chats/u1@fake/messages?raw=1"); m["raw"] == nil {
		t.Fatalf("raw=1 must attach the payload: %v", m)
	}
}

func TestReadyz(t *testing.T) {
	e := newEnv(t)
	if rec, out := e.doAs("", "GET", "/readyz", nil); rec.Code != 200 || out["status"] != "ok" {
		t.Fatalf("readyz needs no token and reports ok: %d %v", rec.Code, out)
	}
}
