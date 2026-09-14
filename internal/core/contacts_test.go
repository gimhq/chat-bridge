package core

import (
	"context"
	"net/http"
	"testing"

	"gimhq/chat-bridge/internal/model"
)

func TestContactChatsAndMessages(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	connected(t, c, f, "a1")
	group := inbound("g1", "team@fake", "u1@fake", "in the group")
	group.Chat = &model.Chat{ID: "team@fake", Kind: model.ChatGroup, Name: "Team"}
	if err := f.Push(ctx, "a1", inbound("m1", "u1@fake", "u1@fake", "hello"), group); err != nil {
		t.Fatal(err)
	}

	chats, err := c.ContactChats(ctx, "a1", "u1@fake")
	if err != nil || len(chats) != 2 {
		t.Fatalf("chats: %+v %v", chats, err)
	}
	direct, _, err := c.ContactMessages(ctx, "a1", "u1@fake", "", "", 10)
	if err != nil || len(direct) != 1 || direct[0].ID != "m1" {
		t.Fatalf("direct (default scope): %+v %v", direct, err)
	}
	if all, _, _ := c.ContactMessages(ctx, "a1", "u1@fake", "all", "", 10); len(all) != 2 {
		t.Fatalf("all: %+v", all)
	}
	for name, err := range map[string]error{
		"bad scope":  second(c.ContactMessages(ctx, "a1", "u1@fake", "everything", "", 10)),
		"bad cursor": second(c.ContactMessages(ctx, "a1", "u1@fake", "all", "nope", 10)),
	} {
		if AsError(err).Status != http.StatusBadRequest {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := c.ContactChats(ctx, "a1", "nobody"); AsError(err).Status != http.StatusNotFound {
		t.Fatalf("unknown contact: %v", err)
	}
	if err := second(c.ContactMessages(ctx, "a1", "nobody", "", "", 10)); AsError(err).Status != http.StatusNotFound {
		t.Fatalf("unknown contact messages: %v", err)
	}
}

func second[A, B any](_ A, _ B, err error) error { return err }
