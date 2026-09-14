package store

import (
	"context"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/model"
)

func TestReIDMergesUserAndDirectChat(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	const oldID, newID = "66995618240@s.whatsapp.net", "235978975346820@lid"
	at := func(sec int64) time.Time { return time.Unix(1_700_000_000+sec, 0) }

	// Old identity: address-book contact with a local alias, linked to a person, blocked.
	if _, err := s.UpsertContact(ctx, "wa", model.Contact{ID: oldID, Phone: "+66995618240", Names: model.Names{First: "Sun"}, IsContact: true, Blocked: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetLocalAlias(ctx, "wa", oldID, "向日葵"); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePerson(ctx, model.Person{Name: "Sun"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LinkContact(ctx, p.ID, LinkRef{AccountID: "wa", UserID: oldID}, "manual"); err != nil {
		t.Fatal(err)
	}
	// New identity: push name only, and its own direct chat with one message.
	if _, err := s.UpsertContact(ctx, "wa", model.Contact{ID: newID, Names: model.Names{Profile: "youli"}}); err != nil {
		t.Fatal(err)
	}
	insert := func(chat, id, sender string, sec int64, fromMe bool, extra func(*model.Message)) int64 {
		t.Helper()
		if _, err := s.UpsertChat(ctx, model.Chat{AccountID: "wa", ID: chat, Kind: model.ChatDirect}); err != nil {
			t.Fatal(err)
		}
		m := model.Message{ID: id, AccountID: "wa", ChatID: chat, Sender: model.Sender{ID: sender, Name: sender}, FromMe: fromMe, Timestamp: at(sec),
			Content: model.Content{Type: model.ContentText, Text: id}}
		if extra != nil {
			extra(&m)
		}
		seq, _, err := s.InsertMessage(ctx, m, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.TouchChat(ctx, "wa", chat, m.Timestamp, seq, !fromMe); err != nil {
			t.Fatal(err)
		}
		return seq
	}
	insert(newID, "dup", newID, 5, false, nil)
	seqOld := insert(oldID, "m1", oldID, 1, false, func(m *model.Message) { m.Mentions = []string{oldID} })
	insert(oldID, "dup", oldID, 5, false, nil)
	mine := insert(oldID, "m2", "me@s.whatsapp.net", 9, true, nil)
	if err := s.UpsertMember(ctx, "wa", oldID, oldID, "", "", false); err != nil {
		t.Fatal(err)
	}
	// A group where the old id is a member and posted a system notice.
	if _, err := s.UpsertChat(ctx, model.Chat{AccountID: "wa", ID: "g1@g.us", Kind: model.ChatGroup}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMember(ctx, "wa", "g1@g.us", oldID, "Sunny", "admin", false); err != nil {
		t.Fatal(err)
	}
	sys := model.Message{ID: "s1", AccountID: "wa", ChatID: "g1@g.us", Sender: model.Sender{ID: oldID}, Timestamp: at(2),
		Content: model.Content{Type: model.ContentSystem, System: &model.System{Kind: "member_added", Actor: oldID, Targets: []string{oldID}}}}
	if _, _, err := s.InsertMessage(ctx, sys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReaction(ctx, mine, oldID, "👍", false, at(10)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetReceipt(ctx, mine, oldID, "read", at(11)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertRequest(ctx, StoredRequest{Request: model.Request{AccountID: "wa", Kind: model.RequestKindCall, From: &model.Sender{ID: oldID},
		Chat: &model.RequestChat{ID: oldID}, CreatedAt: at(3)}, PlatformKey: "call:1"}); err != nil {
		t.Fatal(err)
	}

	res, err := s.ReID(ctx, "wa", oldID, newID)
	if err != nil || !res.Changed || !res.Contact || !res.Chat {
		t.Fatalf("re-id: %+v %v", res, err)
	}

	if _, err := s.GetContact(ctx, "wa", oldID); err == nil {
		t.Fatal("old contact still present")
	}
	ct, err := s.GetContact(ctx, "wa", newID)
	if err != nil || ct.Phone != "+66995618240" || ct.Names.Alias != "向日葵" || ct.Names.AliasSource != "local" || ct.Names.Profile != "youli" ||
		!ct.IsContact || !ct.Blocked {
		t.Fatalf("merged contact: %+v %v", ct, err)
	}
	if got, _ := s.PersonOf(ctx, LinkRef{AccountID: "wa", UserID: newID}); got != p.ID {
		t.Fatalf("person link not moved: %q", got)
	}
	if _, err := s.GetChat(ctx, "wa", oldID); err == nil {
		t.Fatal("old chat still present")
	}
	ch, err := s.GetChat(ctx, "wa", newID)
	if err != nil || ch.UnreadCount != 3 || ch.Kind != model.ChatDirect {
		t.Fatalf("merged chat: %+v %v", ch, err)
	}
	msgs, _, err := s.ListMessages(ctx, "wa", newID, "", time.Time{}, time.Time{}, 10)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("messages: %d %v", len(msgs), err)
	}
	for _, m := range msgs {
		if m.ID == "m1" && (m.Sender.ID != newID || len(m.Mentions) != 1 || m.Mentions[0] != newID) {
			t.Fatalf("moved message: %+v", m.Message)
		}
		if m.ID == "m2" && (len(m.Reactions) != 1 || m.Reactions[0].SenderID != newID) {
			t.Fatalf("reaction: %+v", m.Reactions)
		}
	}
	if last, err := s.GetMessageBySeq(ctx, seqOld); err != nil || last.ChatID != newID {
		t.Fatalf("seq kept: %+v %v", last.Message, err)
	}
	members, err := s.ListMembers(ctx, "wa", "g1@g.us")
	if err != nil || len(members) != 1 || members[0].ID != newID {
		t.Fatalf("group members: %+v %v", members, err)
	}
	notice, err := s.GetMessage(ctx, "wa", "g1@g.us", "s1")
	if err != nil || notice.Content.System.Actor != newID || notice.Content.System.Targets[0] != newID || notice.Sender.ID != newID {
		t.Fatalf("notice: %+v %v", notice.Message, err)
	}
	reqs, _, err := s.ListRequests(ctx, "wa", RequestFilter{}, "", 10)
	if err != nil || len(reqs) != 1 || reqs[0].From == nil || reqs[0].From.ID != newID {
		t.Fatalf("requests: %+v %v", reqs, err)
	}

	// The merged chat has no name of its own; reads show the contact's.
	direct, _, err := s.ListChats(ctx, "wa", ChatFilter{Kind: model.ChatDirect}, "", 10)
	if err != nil || len(direct) != 1 || direct[0].Name != "向日葵" {
		t.Fatalf("direct chat name: %+v %v", direct, err)
	}
	if raw, err := s.GetChat(ctx, "wa", newID); err != nil || raw.Name != "" {
		t.Fatalf("name must not be stored: %+v %v", raw, err)
	}

	if again, err := s.ReID(ctx, "wa", oldID, newID); err != nil || again.Changed {
		t.Fatalf("second re-id: %+v %v", again, err)
	}
	if stranger, err := s.ReID(ctx, "wa", "999@s.whatsapp.net", "999@lid"); err != nil || stranger.Changed {
		t.Fatalf("unknown id: %+v %v", stranger, err)
	}
	ids, err := s.IdentityCandidates(ctx, "wa")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id == oldID {
			t.Fatalf("old id still a candidate: %v", ids)
		}
	}
}
