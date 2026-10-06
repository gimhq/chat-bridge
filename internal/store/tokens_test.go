package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/model"
)

func TestTokensStore(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	created := time.Unix(1_700_000_000, 0).UTC()
	tok := model.Token{ID: "tok_1", Name: "bot", CreatedAt: created,
		Scope: model.TokenScope{Persons: []string{}, Contacts: []model.TokenContact{{AccountID: "wa", UserID: "u1"}}, Chats: []model.TokenChat{}}}
	if err := s.CreateToken(ctx, tok, "hash1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.TokenBySecret(ctx, "hash1")
	if err != nil || got.ID != "tok_1" || got.Scope.Contacts[0].UserID != "u1" || got.LastUsedAt != nil || !got.CreatedAt.Equal(created) {
		t.Fatalf("by secret: %+v %v", got, err)
	}
	if _, err := s.TokenBySecret(ctx, "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown secret: %v", err)
	}

	// A use is recorded, then not again within the minute.
	now := time.Unix(1_700_000_100, 0)
	_ = s.TouchToken(ctx, "tok_1", now)
	_ = s.TouchToken(ctx, "tok_1", now.Add(30*time.Second))
	if got, _ = s.GetToken(ctx, "tok_1"); got.LastUsedAt == nil || got.LastUsedAt.Unix() != now.Unix() {
		t.Fatalf("last used: %v", got.LastUsedAt)
	}
	_ = s.TouchToken(ctx, "tok_1", now.Add(2*time.Minute))
	if got, _ = s.GetToken(ctx, "tok_1"); got.LastUsedAt.Unix() != now.Add(2*time.Minute).Unix() {
		t.Fatalf("last used after a minute: %v", got.LastUsedAt)
	}

	name := "renamed"
	if err := s.UpdateToken(ctx, "tok_1", &name, &model.TokenScope{Persons: []string{"per_1"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetToken(ctx, "tok_1"); got.Name != "renamed" || len(got.Scope.Persons) != 1 || len(got.Scope.Contacts) != 0 {
		t.Fatalf("update: %+v", got)
	}
	if err := s.UpdateToken(ctx, "missing", &name, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	if list, _ := s.ListTokens(ctx); len(list) != 1 {
		t.Fatalf("list: %+v", list)
	}
	if err := s.DeleteToken(ctx, "tok_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteToken(ctx, "tok_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestScopeFilters(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for i, chat := range []string{"c1", "c2"} {
		if _, err := s.UpsertChat(ctx, model.Chat{AccountID: "wa", ID: chat, Kind: model.ChatDirect}); err != nil {
			t.Fatal(err)
		}
		m := msg("m"+chat, int64(100+i), "lunch tomorrow")
		m.ChatID = chat
		if _, _, err := s.InsertMessage(ctx, m, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpsertContact(ctx, "wa", model.Contact{ID: chat}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendEvent(ctx, "wa", model.EvMessageNew, m); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendEvent(ctx, "wa", model.EvChatUpdated, model.Chat{ID: chat}); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = s.AppendEvent(ctx, "wa", model.EvRequestNew, map[string]any{"chat_id": "c1"})
	_, _ = s.AppendEvent(ctx, "wa", model.EvAccountStatus, map[string]any{"id": "wa"})

	for name, c := range map[string]struct {
		only Only
		want int
	}{"unrestricted": {Only{}, 2}, "one": {Only{Set: true, IDs: []string{"c1"}}, 1}, "none": {Only{Set: true}, 0}} {
		chats, _, err := s.ListChats(ctx, "wa", ChatFilter{Only: c.only}, "", 10)
		if err != nil || len(chats) != c.want {
			t.Errorf("%s: chats %d %v", name, len(chats), err)
		}
		found, _, err := s.SearchMessages(ctx, "wa", "", "lunch", "", 10, c.only)
		if err != nil || len(found) != c.want {
			t.Errorf("%s: search %d %v", name, len(found), err)
		}
		contacts, _, err := s.ListContacts(ctx, "wa", "", "", 10, c.only)
		if err != nil || len(contacts) != c.want {
			t.Errorf("%s: contacts %d %v", name, len(contacts), err)
		}
	}
	if direct, err := s.DirectChats(ctx, LinkRef{AccountID: "wa", UserID: "c1"}); err != nil || len(direct) != 1 || direct[0] != "c1" {
		t.Fatalf("direct chats: %v %v", direct, err)
	}

	count := func(sc *EventScope) int {
		evs, err := s.ListEvents(ctx, 0, EventFilter{Scope: sc}, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(evs)
	}
	if n := count(nil); n != 6 {
		t.Fatalf("all events: %d", n)
	}
	// One chat: its message and chat events, never the request; the account event needs the account.
	if n := count(&EventScope{Chats: []ChatRef{{AccountID: "wa", ChatID: "c1"}}}); n != 2 {
		t.Fatalf("chat scope: %d", n)
	}
	if n := count(&EventScope{Chats: []ChatRef{{AccountID: "wa", ChatID: "c1"}}, Accounts: []string{"wa"}}); n != 3 {
		t.Fatalf("chat and account scope: %d", n)
	}
	if n := count(&EventScope{Chats: []ChatRef{{AccountID: "other", ChatID: "c1"}}}); n != 0 {
		t.Fatalf("same chat id on another account: %d", n)
	}
	if n := count(&EventScope{}); n != 0 {
		t.Fatalf("empty scope: %d", n)
	}
}
