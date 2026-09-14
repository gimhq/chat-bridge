package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/model"
)

func TestContactChatsAndMessages(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"u1", "u2"} {
		if _, err := s.UpsertContact(ctx, "wa", model.Contact{ID: id, Names: model.Names{Profile: id}}); err != nil {
			t.Fatal(err)
		}
	}
	// u1: a DM keyed by the user, a direct room found through members, a group; u1 left "old".
	for _, c := range []model.Chat{
		{AccountID: "wa", ID: "u1", Kind: model.ChatDirect},
		{AccountID: "wa", ID: "!room", Kind: model.ChatDirect},
		{AccountID: "wa", ID: "g1", Kind: model.ChatGroup, Name: "Team"},
		{AccountID: "wa", ID: "old", Kind: model.ChatGroup, Name: "Old"},
		{AccountID: "wa", ID: "u2", Kind: model.ChatDirect},
	} {
		if _, err := s.UpsertChat(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []struct {
		chat, user string
		left       bool
	}{{"!room", "u1", false}, {"g1", "u1", false}, {"g1", "u2", false}, {"old", "u1", true}} {
		if err := s.UpsertMember(ctx, "wa", m.chat, m.user, "", "", m.left); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range []model.Message{
		{ChatID: "u1", ID: "dm-in", Sender: model.Sender{ID: "u1"}},
		{ChatID: "u1", ID: "dm-out", Sender: model.Sender{ID: "me"}, FromMe: true},
		{ChatID: "!room", ID: "room", Sender: model.Sender{ID: "me"}, FromMe: true},
		{ChatID: "g1", ID: "g-u1", Sender: model.Sender{ID: "u1"}},
		{ChatID: "g1", ID: "g-u2", Sender: model.Sender{ID: "u2"}},
		{ChatID: "u2", ID: "u2-dm", Sender: model.Sender{ID: "u2"}},
	} {
		m.AccountID, m.Timestamp = "wa", time.Unix(int64(1000+i), 0)
		m.Content = model.Content{Type: model.ContentText, Text: m.ID}
		seq, _, err := s.InsertMessage(ctx, m, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.TouchChat(ctx, "wa", m.ChatID, m.Timestamp, seq, !m.FromMe); err != nil {
			t.Fatal(err)
		}
	}

	chats, err := s.ContactChats(ctx, "wa", "u1")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range chats {
		ids = append(ids, c.ID)
	}
	if len(ids) != 3 || ids[0] != "g1" || ids[1] != "!room" || ids[2] != "u1" || chats[0].LastMessage == nil {
		t.Fatalf("contact chats (recent first, left groups excluded): %v", ids)
	}

	texts := func(ms []Stored) (out []string) {
		for _, m := range ms {
			out = append(out, m.Content.Text)
		}
		return out
	}
	direct, next, err := s.ContactMessages(ctx, "wa", "u1", "direct", "", 10)
	if got := texts(direct); err != nil || next != "" || len(got) != 3 || got[0] != "room" || got[2] != "dm-in" {
		t.Fatalf("direct: %v %q %v", got, next, err)
	}
	page, next, _ := s.ContactMessages(ctx, "wa", "u1", "all", "", 2)
	if got := texts(page); len(got) != 2 || got[0] != "g-u1" || next == "" {
		t.Fatalf("all page 1: %v %q", got, next)
	}
	rest, next, _ := s.ContactMessages(ctx, "wa", "u1", "all", next, 10)
	if got := texts(rest); len(got) != 2 || got[0] != "dm-out" || next != "" {
		t.Fatalf("all page 2: %v %q", got, next)
	}
	if _, err := s.ContactChats(ctx, "wa", "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown contact chats: %v", err)
	}
	if _, _, err := s.ContactMessages(ctx, "wa", "nobody", "direct", "", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown contact messages: %v", err)
	}

	for key, from := range map[string]string{"call:1": "u1", "call:2": "u2"} {
		if _, _, err := s.UpsertRequest(ctx, StoredRequest{Request: model.Request{AccountID: "wa", Kind: model.RequestKindCall,
			From: &model.Sender{ID: from}, CreatedAt: time.Unix(2000, 0)}, PlatformKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	if reqs, _, err := s.ListRequests(ctx, "wa", RequestFilter{From: "u1"}, "", 10); err != nil || len(reqs) != 1 || reqs[0].From.ID != "u1" {
		t.Fatalf("requests from u1: %+v %v", reqs, err)
	}
}
