package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateAccount(context.Background(), "wa", "whatsapp", "", nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func msg(id string, ts int64, text string) model.Message {
	return model.Message{
		ID: id, AccountID: "wa", ChatID: "c1", Sender: model.Sender{ID: "u1"}, Timestamp: time.Unix(ts, 0),
		Content: model.Content{Type: model.ContentText, Text: text},
	}
}

func TestAccountLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.CreateAccount(ctx, "wa", "whatsapp", "", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if err := s.SetAccountStatus(ctx, "wa", model.StatusConnected, "me@s", json.RawMessage(`{"v":1}`), nil); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAccount(ctx, "wa")
	if err != nil || a.Status != model.StatusConnected || a.SelfID != "me@s" || a.ConnectedAt == nil || a.Error != nil {
		t.Fatalf("bad account %+v %v", a, err)
	}
	if err := s.SetAccountStatus(ctx, "wa", model.StatusDisconnected, "", nil, &model.Error{Code: "network"}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAccount(ctx, "wa")
	if a.SelfID != "me@s" || a.Error == nil || a.Error.Code != "network" {
		t.Fatalf("self/error not kept: %+v", a)
	}
	if err := s.ClearAccountSession(ctx, "wa"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAccount(ctx, "wa")
	if a.Status != model.StatusUnpaired || a.SelfID != "" {
		t.Fatalf("not cleared: %+v", a)
	}
	if err := s.DeleteAccount(ctx, "wa"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAccount(ctx, "wa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestContactMergeAndNames(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	changed, err := s.UpsertContact(ctx, "wa", model.Contact{ID: "u1", Names: model.Names{Profile: "alice"}, Phone: "+1"})
	if err != nil || !changed {
		t.Fatalf("first upsert: %v %v", changed, err)
	}
	changed, _ = s.UpsertContact(ctx, "wa", model.Contact{ID: "u1", Names: model.Names{Profile: "alice"}})
	if changed {
		t.Fatal("no-op upsert reported change")
	}
	c, _ := s.GetContact(ctx, "wa", "u1")
	if c.Phone != "+1" || c.Name != "alice" {
		t.Fatalf("merge lost fields: %+v", c)
	}
	if _, err := s.SetLocalAlias(ctx, "wa", "u1", "Alice W"); err != nil {
		t.Fatal(err)
	}
	// A platform alias must not override a local one.
	if _, err := s.UpsertContact(ctx, "wa", model.Contact{ID: "u1", Names: model.Names{Alias: "from-phone"}}); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetContact(ctx, "wa", "u1")
	if c.Name != "Alice W" || c.Names.AliasSource != "local" {
		t.Fatalf("local alias lost: %+v", c.Names)
	}
	if got := ResolveName(model.Contact{ID: "x", Handle: "+2"}, "nick"); got != "nick" {
		t.Fatalf("chat_name precedence: %s", got)
	}
}

func TestMessagesCursorAndIdempotency(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.UpsertChat(ctx, model.Chat{AccountID: "wa", ID: "c1", Kind: model.ChatDirect}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		m := msg("m"+string(rune('0'+i)), int64(1000+i), "hi")
		seq, inserted, err := s.InsertMessage(ctx, m, json.RawMessage(`{"i":1}`))
		if err != nil || !inserted {
			t.Fatalf("insert %d: %v %v", i, inserted, err)
		}
		if err := s.TouchChat(ctx, "wa", "c1", m.Timestamp, seq, true); err != nil {
			t.Fatal(err)
		}
	}
	// Same (account, chat, id) again is a no-op.
	if _, inserted, err := s.InsertMessage(ctx, msg("m1", 1001, "dup"), nil); err != nil || inserted {
		t.Fatalf("dup insert: %v %v", inserted, err)
	}
	// Same timestamp, different id: ordering falls back to seq.
	if _, _, err := s.InsertMessage(ctx, msg("m6", 1005, "tie"), nil); err != nil {
		t.Fatal(err)
	}
	page, next, err := s.ListMessages(ctx, "wa", "c1", "", time.Time{}, time.Time{}, 2)
	if err != nil || len(page) != 2 || next == "" {
		t.Fatalf("page1: %v %d %q", err, len(page), next)
	}
	if page[0].ID != "m6" || page[1].ID != "m5" {
		t.Fatalf("order: %s %s", page[0].ID, page[1].ID)
	}
	page2, next2, err := s.ListMessages(ctx, "wa", "c1", next, time.Time{}, time.Time{}, 10)
	if err != nil || len(page2) != 4 || next2 != "" {
		t.Fatalf("page2: %v %d %q", err, len(page2), next2)
	}
	if page2[0].ID != "m4" {
		t.Fatalf("page2 order: %s", page2[0].ID)
	}
	raw, _ := s.RawPayload(ctx, page2[0].Seq())
	if string(raw) != `{"i":1}` {
		t.Fatalf("raw: %s", raw)
	}
	chat, _ := s.GetChat(ctx, "wa", "c1")
	if chat.UnreadCount != 5 || chat.LastMessageAt == nil || chat.LastMessageAt.Unix() != 1005 {
		t.Fatalf("chat rollup: %+v", chat)
	}
	chats, _, err := s.ListChats(ctx, "wa", ChatFilter{}, "", 10)
	if err != nil || len(chats) != 1 || chats[0].LastMessage == nil || chats[0].LastMessage.ID != "m5" {
		t.Fatalf("list chats: %v %+v", err, chats)
	}
}

func TestClientIDReactionsReceiptsEdit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, _ = s.UpsertChat(ctx, model.Chat{AccountID: "wa", ID: "c1"})
	m := msg("out1", 2000, "hello")
	m.FromMe, m.Status, m.ClientID = true, model.MsgSent, "cid-1"
	seq, _, err := s.InsertMessage(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindByClientID(ctx, "wa", "c1", "cid-1"); err != nil {
		t.Fatal(err)
	}
	dup := msg("out2", 2001, "again")
	dup.ClientID = "cid-1"
	if _, _, err := s.InsertMessage(ctx, dup, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("client_id uniqueness: %v", err)
	}
	if err := s.SetReaction(ctx, seq, "u2", "👍", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReaction(ctx, seq, "u2", "❤️", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMessageBySeq(ctx, seq)
	if len(got.Reactions) != 1 || got.Reactions[0].Emoji != "❤️" {
		t.Fatalf("reactions: %+v", got.Reactions)
	}
	st, err := s.SetReceipt(ctx, seq, "u2", "delivered", time.Now())
	if err != nil || st != model.MsgDelivered {
		t.Fatalf("receipt: %s %v", st, err)
	}
	st, _ = s.SetReceipt(ctx, seq, "u2", "read", time.Now())
	if st != model.MsgRead {
		t.Fatalf("read rollup: %s", st)
	}
	edited := time.Unix(2100, 0)
	upd, err := s.UpdateMessage(ctx, seq, func(m *model.Message) {
		m.Content.Text = "hello v2"
		m.EditedAt = &edited
	})
	if err != nil || upd.Content.Text != "hello v2" || upd.EditedAt == nil || upd.Status != model.MsgRead {
		t.Fatalf("edit: %+v %v", upd.Message, err)
	}
	var n int
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_versions WHERE message_seq = ?`, seq).Scan(&n); err != nil || n != 1 {
		t.Fatalf("versions: %d %v", n, err)
	}
}

func TestMediaAttachment(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, _ = s.UpsertChat(ctx, model.Chat{AccountID: "wa", ID: "c1"})
	m := msg("img1", 3000, "")
	m.Content = model.Content{Type: model.ContentImage, Attachments: []model.Attachment{{MediaID: "img1", Mime: "image/jpeg", State: model.MediaPending}}}
	seq, _, _ := s.InsertMessage(ctx, m, nil)
	if err := s.UpsertMedia(ctx, Media{ID: "img1", AccountID: "wa", MessageSeq: seq, Mime: "image/jpeg", State: model.MediaPending, RemoteRef: json.RawMessage(`{"k":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMedia(ctx, Media{ID: "img1", AccountID: "wa", Mime: "image/jpeg", State: model.MediaReady, SHA256: "abc", Size: 10}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMessageBySeq(ctx, seq)
	att := got.Content.Attachments
	if len(att) != 1 || att[0].State != model.MediaReady || att[0].SHA256 != "abc" || att[0].URL != "/v1/media/img1" {
		t.Fatalf("attachment: %+v", att)
	}
	md, _ := s.GetMedia(ctx, "img1")
	if string(md.RemoteRef) != `{"k":1}` || md.MessageSeq != seq {
		t.Fatalf("media row lost fields: %+v", md)
	}
}

func TestEventsAndWebhooks(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, typ := range []string{model.EvMessageNew, model.EvChatTyping, model.EvPlatform, model.EvMessageNew} {
		if _, err := s.AppendEvent(ctx, "wa", typ, map[string]int{"x": 1}); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := s.ListEvents(ctx, 0, EventFilter{}, 10)
	if err != nil || len(evs) != 3 {
		t.Fatalf("platform.event should be hidden by default: %d %v", len(evs), err)
	}
	if evs[0].ID != FormatEventID(1) {
		t.Fatalf("id format: %s", evs[0].ID)
	}
	evs, _ = s.ListEvents(ctx, 0, EventFilter{Types: []string{model.EvPlatform}}, 10)
	if len(evs) != 1 {
		t.Fatalf("type filter: %d", len(evs))
	}
	after, _ := ParseEventID("0000000000000002")
	evs, _ = s.ListEvents(ctx, after, EventFilter{AccountID: "wa"}, 10)
	if len(evs) != 1 || evs[0].ID != FormatEventID(4) {
		t.Fatalf("cursor: %+v", evs)
	}
	if err := s.CreateWebhook(ctx, model.Webhook{ID: "w1", URL: "http://x", Secret: "s", Types: []string{model.EvMessageNew}}); err != nil {
		t.Fatal(err)
	}
	due, _ := s.DueWebhooks(ctx, time.Now())
	if len(due) != 1 || due[0].CursorN != 0 || len(due[0].Types) != 1 {
		t.Fatalf("due: %+v", due)
	}
	_ = s.AckWebhook(ctx, "w1", 4)
	_ = s.FailWebhook(ctx, "w1", time.Now().Add(time.Hour), false)
	due, _ = s.DueWebhooks(ctx, time.Now())
	if len(due) != 0 {
		t.Fatal("backoff ignored")
	}
	ws, _ := s.ListWebhooks(ctx)
	if ws[0].Cursor != FormatEventID(4) || ws[0].Failures != 1 {
		t.Fatalf("webhook state: %+v", ws[0])
	}
}

func TestSearchRenameAndBlock(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.CreateAccount(ctx, "a1", "fake", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertChat(ctx, model.Chat{AccountID: "a1", ID: "c1", Kind: model.ChatGroup, Name: "Old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertChat(ctx, model.Chat{AccountID: "a1", ID: "c2", Kind: model.ChatDirect}); err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{"m1": "meeting tomorrow at ten", "m2": "早上开会 好的", "m3": "tomorrow again", "m4": "unrelated"}
	chats := map[string]string{"m1": "c1", "m2": "c1", "m3": "c2", "m4": "c2"}
	for i, id := range []string{"m1", "m2", "m3", "m4"} {
		m := msg(id, int64(1000+i), texts[id])
		m.AccountID, m.ChatID = "a1", chats[id]
		if _, _, err := s.InsertMessage(ctx, m, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, next, err := s.SearchMessages(ctx, "a1", "", "tomorrow", "", 10)
	if err != nil || len(got) != 2 || got[0].ID != "m3" || got[1].ID != "m1" || next != "" {
		t.Fatalf("search: %v %v", got, err)
	}
	got, _, _ = s.SearchMessages(ctx, "a1", "c1", "tomorrow", "", 10)
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("search in chat: %v", got)
	}
	got, _, _ = s.SearchMessages(ctx, "a1", "", "开会", "", 10) // two characters: LIKE path
	if len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("search cjk short: %v", got)
	}
	got, _, _ = s.SearchMessages(ctx, "a1", "", "上开会", "", 10) // three characters: trigram path
	if len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("search cjk trigram: %v", got)
	}
	got, _, _ = s.SearchMessages(ctx, "a1", "", "tomorrow ten", "", 10)
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("search all terms: %v", got)
	}
	got, next, _ = s.SearchMessages(ctx, "a1", "", "tomorrow", "", 1)
	if len(got) != 1 || next == "" {
		t.Fatalf("search page 1: %v %q", got, next)
	}
	got, next, _ = s.SearchMessages(ctx, "a1", "", "tomorrow", next, 1)
	if len(got) != 1 || got[0].ID != "m1" || next != "" {
		t.Fatalf("search page 2: %v %q", got, next)
	}
	if _, _, err := s.SearchMessages(ctx, "a1", "", "  ", "", 1); err == nil {
		t.Fatal("empty query must fail")
	}
	// Edits keep the index in step.
	st, _ := s.GetMessage(ctx, "a1", "c2", "m4")
	if _, err := s.UpdateMessage(ctx, st.Seq(), func(m *model.Message) { m.Content.Text = "now about tomorrow" }); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.SearchMessages(ctx, "a1", "", "tomorrow", "", 10); len(got) != 3 {
		t.Fatalf("after edit: %v", got)
	}

	ch, err := s.SetChatName(ctx, "a1", "c1", "New")
	if err != nil || ch.Name != "New" {
		t.Fatalf("rename: %+v %v", ch, err)
	}
	if _, err := s.UpsertContact(ctx, "a1", model.Contact{ID: "u1"}); err != nil {
		t.Fatal(err)
	}
	ct, err := s.SetBlocked(ctx, "a1", "u1", true)
	if err != nil || !ct.Blocked {
		t.Fatalf("block: %+v %v", ct, err)
	}
}
