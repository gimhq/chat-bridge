package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	got, next, err := s.SearchMessages(ctx, "a1", "", "tomorrow", "", 10, Only{})
	if err != nil || len(got) != 2 || got[0].ID != "m3" || got[1].ID != "m1" || next != "" {
		t.Fatalf("search: %v %v", got, err)
	}
	got, _, _ = s.SearchMessages(ctx, "a1", "c1", "tomorrow", "", 10, Only{})
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("search in chat: %v", got)
	}
	got, _, _ = s.SearchMessages(ctx, "a1", "", "开会", "", 10, Only{}) // two characters: LIKE path
	if len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("search cjk short: %v", got)
	}
	got, _, _ = s.SearchMessages(ctx, "a1", "", "上开会", "", 10, Only{}) // three characters: trigram path
	if len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("search cjk trigram: %v", got)
	}
	got, _, _ = s.SearchMessages(ctx, "a1", "", "tomorrow ten", "", 10, Only{})
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("search all terms: %v", got)
	}
	got, next, _ = s.SearchMessages(ctx, "a1", "", "tomorrow", "", 1, Only{})
	if len(got) != 1 || next == "" {
		t.Fatalf("search page 1: %v %q", got, next)
	}
	got, next, _ = s.SearchMessages(ctx, "a1", "", "tomorrow", next, 1, Only{})
	if len(got) != 1 || got[0].ID != "m1" || next != "" {
		t.Fatalf("search page 2: %v %q", got, next)
	}
	if _, _, err := s.SearchMessages(ctx, "a1", "", "  ", "", 1, Only{}); err == nil {
		t.Fatal("empty query must fail")
	}
	// Edits keep the index in step.
	st, _ := s.GetMessage(ctx, "a1", "c2", "m4")
	if _, err := s.UpdateMessage(ctx, st.Seq(), func(m *model.Message) { m.Content.Text = "now about tomorrow" }); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.SearchMessages(ctx, "a1", "", "tomorrow", "", 10, Only{}); len(got) != 3 {
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

func TestRequestsUpsertStateAndPaging(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.CreateAccount(ctx, "a1", "fake", "", nil); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1_700_000_000, 0).UTC()
	in := StoredRequest{PlatformKey: "invite:g1", PlatformRef: json.RawMessage(`{"code":"x"}`), Request: model.Request{AccountID: "a1",
		Kind: model.RequestKindChatInvite, From: &model.Sender{ID: "u1", Name: "Alice"}, Chat: &model.RequestChat{ID: "g1", Name: "Team"}, CreatedAt: base}}
	got, change, err := s.UpsertRequest(ctx, in)
	if err != nil || change != RequestCreated || got.ID == "" || got.State != model.RequestPending || len(got.Actions) != 3 {
		t.Fatalf("create: %+v %v %v", got, change, err)
	}
	// Same payload: nothing changes. New chat name: updated.
	if _, change, _ = s.UpsertRequest(ctx, in); change != RequestUnchanged {
		t.Fatalf("replay: %v", change)
	}
	in.Chat.Name = "Team 2"
	if got, change, _ = s.UpsertRequest(ctx, in); change != RequestUpdated || got.Chat.Name != "Team 2" {
		t.Fatalf("rename: %+v %v", got, change)
	}
	// Answered requests are terminal: a replayed pending invite does not reopen them...
	answered := base.Add(time.Minute)
	if _, change, _ := s.UpsertRequest(ctx, StoredRequest{PlatformKey: "call:gone", Request: model.Request{AccountID: "a1", Kind: model.RequestKindCall,
		State: model.RequestExpired}}); change != RequestUnchanged {
		t.Fatalf("terminal first sighting must not create a row: %v", change)
	}
	if got, err = s.SetRequestState(ctx, "a1", got.ID, model.RequestRejected, &answered); err != nil || got.State != model.RequestRejected || got.AnsweredAt == nil || len(got.Actions) != 0 {
		t.Fatalf("reject: %+v %v", got, err)
	}
	if got, change, _ = s.UpsertRequest(ctx, in); change != RequestUnchanged || got.State != model.RequestRejected {
		t.Fatalf("replay after reject: %+v %v", got, change)
	}
	// ...but a later invite for the same key does.
	in.CreatedAt = base.Add(time.Hour)
	if got, change, _ = s.UpsertRequest(ctx, in); change != RequestUpdated || got.State != model.RequestPending || got.AnsweredAt != nil {
		t.Fatalf("reinvite: %+v %v", got, change)
	}
	st, err := s.GetRequest(ctx, "a1", got.ID)
	if err != nil || string(st.PlatformRef) != `{"code":"x"}` || st.PlatformKey != "invite:g1" {
		t.Fatalf("get: %+v %v", st, err)
	}
	// Adapter-reported terminal state applies to pending rows.
	call := StoredRequest{PlatformKey: "call:c1", Request: model.Request{AccountID: "a1", Kind: model.RequestKindCall, Call: &model.CallInfo{Kind: "voice"},
		From: &model.Sender{ID: "u2"}, CreatedAt: base.Add(2 * time.Hour), ExpiresAt: ptrTime(base.Add(2*time.Hour + time.Minute))}}
	c1, _, _ := s.UpsertRequest(ctx, call)
	if len(c1.Actions) != 1 || c1.Actions[0] != model.ActionIgnore || c1.Call == nil || c1.Call.Kind != "voice" {
		t.Fatalf("call: %+v", c1)
	}
	call.State = model.RequestExpired
	if c1, change, _ = s.UpsertRequest(ctx, call); change != RequestUpdated || c1.State != model.RequestExpired {
		t.Fatalf("call ended: %+v %v", c1, change)
	}
	// Expiry sweep, listing filters and paging.
	for i := 0; i < 3; i++ {
		exp := base.Add(time.Duration(10+i) * time.Hour)
		_, _, _ = s.UpsertRequest(ctx, StoredRequest{PlatformKey: fmt.Sprintf("join:%d", i), Request: model.Request{AccountID: "a1",
			Kind: model.RequestKindJoin, From: &model.Sender{ID: "u"}, CreatedAt: base.Add(time.Duration(3+i) * time.Hour), ExpiresAt: &exp}})
	}
	expired, err := s.ExpireRequests(ctx, base.Add(11*time.Hour+time.Minute))
	if err != nil || len(expired) != 2 || expired[0].State != model.RequestExpired {
		t.Fatalf("expire: %+v %v", expired, err)
	}
	all, next, err := s.ListRequests(ctx, "a1", RequestFilter{}, "", 3)
	if err != nil || len(all) != 3 || next == "" || all[0].Kind != model.RequestKindJoin {
		t.Fatalf("list page 1: %+v %q %v", all, next, err)
	}
	rest, next, _ := s.ListRequests(ctx, "a1", RequestFilter{}, next, 3)
	if len(rest) != 2 || next != "" {
		t.Fatalf("list page 2: %+v %q", rest, next)
	}
	pending, _, _ := s.ListRequests(ctx, "a1", RequestFilter{State: model.RequestPending}, "", 10)
	if len(pending) != 2 {
		t.Fatalf("pending: %+v", pending)
	}
	calls, _, _ := s.ListRequests(ctx, "a1", RequestFilter{Kind: model.RequestKindCall}, "", 10)
	if len(calls) != 1 {
		t.Fatalf("calls: %+v", calls)
	}
	if n, _ := s.PendingRequests(ctx, "a1"); n != 2 {
		t.Fatalf("pending count: %d", n)
	}
	if _, err := s.GetRequest(ctx, "a1", "req_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestPersonsStore(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, a := range []string{"wa1", "tg1", "mx1"} {
		if err := s.CreateAccount(ctx, a, "fake", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	contact := func(account, id, phone, name string) {
		t.Helper()
		if _, err := s.UpsertContact(ctx, account, model.Contact{ID: id, Phone: phone, Names: model.Names{Profile: name}}); err != nil {
			t.Fatal(err)
		}
	}
	contact("wa1", "8613800000000@s.whatsapp.net", "+86 138-0000-0000", "Alice")
	contact("tg1", "12345", "+8613800000000", "Alice T")
	contact("mx1", "@alice:example.org", "", "Alice M")
	contact("tg1", "999", "+1 (555) 010-0000", "Bob")
	contact("wa1", "15550100000@s.whatsapp.net", "+15550100000", "Bob W")
	contact("wa1", "self@s.whatsapp.net", "+15550100000", "Me")
	if _, err := s.UpsertContact(ctx, "wa1", model.Contact{ID: "self@s.whatsapp.net", IsSelf: true}); err != nil {
		t.Fatal(err)
	}

	// Phone matches ignore formatting, stay on other accounts and skip self contacts.
	wa := LinkRef{AccountID: "wa1", UserID: "8613800000000@s.whatsapp.net"}
	tg := LinkRef{AccountID: "tg1", UserID: "12345"}
	mx := LinkRef{AccountID: "mx1", UserID: "@alice:example.org"}
	matches, err := s.PhoneMatches(ctx, wa)
	if err != nil || len(matches) != 1 || matches[0].LinkRef != tg || matches[0].PersonID != "" {
		t.Fatalf("phone matches: %+v %v", matches, err)
	}
	if m, _ := s.PhoneMatches(ctx, mx); len(m) != 0 {
		t.Fatalf("no phone, no matches: %+v", m)
	}

	// Suggestions: both phone groups, self excluded.
	sugg, err := s.SuggestPersons(ctx)
	if err != nil || len(sugg) != 2 || sugg[0].Reason != "phone" {
		t.Fatalf("suggest: %+v %v", sugg, err)
	}
	for _, g := range sugg {
		for _, c := range g.Contacts {
			if c.UserID == "self@s.whatsapp.net" {
				t.Fatal("self contact suggested")
			}
		}
	}

	p, err := s.CreatePerson(ctx, model.Person{Name: "Alice Wang", Tags: []string{"work"}, Notes: "PM"})
	if err != nil || p.ID == "" || len(p.Links) != 0 || p.Tags[0] != "work" {
		t.Fatalf("create: %+v %v", p, err)
	}
	if err := s.LinkContact(ctx, p.ID, wa, model.LinkPhone); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkContact(ctx, p.ID, tg, model.LinkPhone); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkContact(ctx, p.ID, mx, model.LinkManual); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkContact(ctx, p.ID, LinkRef{AccountID: "wa1", UserID: "nobody"}, model.LinkManual); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown contact: %v", err)
	}
	other, _ := s.CreatePerson(ctx, model.Person{Name: "Other"})
	if err := s.LinkContact(ctx, other.ID, tg, model.LinkManual); !errors.Is(err, ErrConflict) {
		t.Fatalf("linked elsewhere: %v", err)
	}
	if err := s.LinkContact(ctx, p.ID, tg, model.LinkManual); err != nil {
		t.Fatalf("relinking to the same person is a no-op: %v", err)
	}
	got, err := s.GetPerson(ctx, p.ID)
	if err != nil || len(got.Links) != 3 || got.Links[0].Platform != "fake" || got.Links[0].Name == "" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if c, _ := s.GetContact(ctx, "tg1", "12345"); c.PersonID != p.ID {
		t.Fatalf("contact person_id: %+v", c)
	}
	if id, _ := s.PersonOf(ctx, mx); id != p.ID {
		t.Fatalf("person of: %q", id)
	}
	if sugg, _ = s.SuggestPersons(ctx); len(sugg) != 1 {
		t.Fatalf("linked contacts drop out of suggestions: %+v", sugg)
	}

	// Chats: a WhatsApp-style DM (chat id = user id) and a Matrix-style DM room found through members.
	for _, c := range []model.Chat{
		{AccountID: "wa1", ID: wa.UserID, Kind: model.ChatDirect, Name: "Alice"},
		{AccountID: "mx1", ID: "!dm:example.org", Kind: model.ChatDirect},
		{AccountID: "mx1", ID: "!group:example.org", Kind: model.ChatGroup, Name: "Team"},
	} {
		if _, err := s.UpsertChat(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, room := range []string{"!dm:example.org", "!group:example.org"} {
		if err := s.UpsertMember(ctx, "mx1", room, mx.UserID, "", "member", false); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range []model.Message{
		{AccountID: "wa1", ChatID: wa.UserID, ID: "w1", Sender: model.Sender{ID: wa.UserID}, Timestamp: time.Unix(1000, 0), Content: model.Content{Type: "text", Text: "wa dm"}},
		{AccountID: "mx1", ChatID: "!dm:example.org", ID: "$m1", Sender: model.Sender{ID: mx.UserID}, Timestamp: time.Unix(2000, 0), Content: model.Content{Type: "text", Text: "mx dm"}},
		{AccountID: "mx1", ChatID: "!group:example.org", ID: "$g1", Sender: model.Sender{ID: mx.UserID}, Timestamp: time.Unix(3000, 0), Content: model.Content{Type: "text", Text: "mx group"}},
		{AccountID: "mx1", ChatID: "!group:example.org", ID: "$g2", Sender: model.Sender{ID: "@carol:example.org"}, Timestamp: time.Unix(4000, 0), Content: model.Content{Type: "text", Text: "carol"}},
	} {
		seq, _, err := s.InsertMessage(ctx, m, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.TouchChat(ctx, m.AccountID, m.ChatID, m.Timestamp, seq, true); err != nil {
			t.Fatalf("touch %d: %v", i, err)
		}
	}
	if ch, _ := s.GetChat(ctx, "mx1", "!dm:example.org"); ch.PersonID != p.ID {
		t.Fatalf("matrix dm person_id: %+v", ch)
	}
	if ch, _ := s.GetChat(ctx, "mx1", "!group:example.org"); ch.PersonID != "" {
		t.Fatalf("groups have no person: %+v", ch)
	}
	if list, _, _ := s.ListChats(ctx, "mx1", ChatFilter{Person: p.ID}, "", 10); len(list) != 1 || list[0].ID != "!dm:example.org" {
		t.Fatalf("chat filter: %+v", list)
	}
	chats, err := s.PersonChats(ctx, p.ID)
	if err != nil || len(chats) != 2 {
		t.Fatalf("person chats: %+v %v", chats, err)
	}
	if got, _ = s.GetPerson(ctx, p.ID); len(got.Channels) != 2 {
		t.Fatalf("channels: %+v", got.Channels)
	}
	direct, next, err := s.PersonMessages(ctx, p.ID, "direct", "", 10)
	if err != nil || len(direct) != 2 || direct[0].Content.Text != "mx dm" || next != "" || direct[0].Sender.PersonID != p.ID {
		t.Fatalf("direct messages: %+v %q %v", direct, next, err)
	}
	all, next, _ := s.PersonMessages(ctx, p.ID, "all", "", 2)
	if len(all) != 2 || all[0].Content.Text != "mx group" || next == "" {
		t.Fatalf("all messages page 1: %+v %q", all, next)
	}
	if rest, _, _ := s.PersonMessages(ctx, p.ID, "all", next, 2); len(rest) != 1 || rest[0].Content.Text != "wa dm" {
		t.Fatalf("all messages page 2: %+v", rest)
	}

	// Unlinking records the split pairs, so phone matching no longer offers them.
	if err := s.UnlinkContact(ctx, p.ID, tg); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlinkContact(ctx, p.ID, tg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlink twice: %v", err)
	}
	if m, _ := s.PhoneMatches(ctx, tg); len(m) != 0 {
		t.Fatalf("split pair must not match again: %+v", m)
	}

	// Listing, patching, merging, deleting.
	name, notes := "Alice W.", "PM at Acme"
	if got, err = s.UpdatePerson(ctx, p.ID, &name, &notes, []string{"work", "vip"}); err != nil || got.Name != name || len(got.Tags) != 2 {
		t.Fatalf("update: %+v %v", got, err)
	}
	if err := s.LinkContact(ctx, other.ID, tg, model.LinkManual); err != nil {
		t.Fatal(err)
	}
	if list, _, _ := s.ListPersons(ctx, PersonFilter{Tag: "vip"}, "", 10); len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("tag filter: %+v", list)
	}
	if list, _, _ := s.ListPersons(ctx, PersonFilter{Q: "alice t"}, "", 10); len(list) != 1 || list[0].ID != other.ID {
		t.Fatalf("q matches linked contact names: %+v", list)
	}
	merged, err := s.MergePersons(ctx, p.ID, []string{other.ID})
	if err != nil || len(merged.Links) != 3 {
		t.Fatalf("merge: %+v %v", merged, err)
	}
	if _, err := s.GetPerson(ctx, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("merged source must be gone: %v", err)
	}
	unlinked, err := s.DeletePerson(ctx, p.ID)
	if err != nil || len(unlinked) != 3 {
		t.Fatalf("delete: %+v %v", unlinked, err)
	}
	if c, _ := s.GetContact(ctx, "tg1", "12345"); c.PersonID != "" {
		t.Fatalf("person_id after delete: %+v", c)
	}
}
