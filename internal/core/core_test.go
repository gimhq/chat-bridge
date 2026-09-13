package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapter/fake"
	"gimhq/chat-bridge/internal/media"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

func newCore(t *testing.T) (*Core, *fake.Adapter) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "chatbridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := media.Open(filepath.Join(dir, "media"))
	if err != nil {
		t.Fatal(err)
	}
	c := New(Options{Store: st, Blobs: blobs, DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Media: MediaPolicy{AutoDownload: []string{model.ContentImage}, MaxBytes: 1 << 20}})
	f := fake.New()
	c.Register(f)
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Stop(context.Background()); _ = st.Close() })
	return c, f
}

func connected(t *testing.T, c *Core, f *fake.Adapter, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.CreateAccount(ctx, id, fake.Platform, "", json.RawMessage(`{"secret":"s3","name":"n"}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.Connect(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func inbound(id, chat, sender, text string) adapter.Event {
	return adapter.Event{Kind: adapter.EvMessage,
		Message: &model.Message{ID: id, ChatID: chat, Sender: model.Sender{ID: sender}, Timestamp: time.Now().UTC(), Content: model.Content{Type: model.ContentText, Text: text}},
		Chat:    &model.Chat{ID: chat, Kind: model.ChatDirect, Name: "Alice"},
		Sender:  &model.Contact{ID: sender, Names: model.Names{Profile: "alice"}, Phone: "+1"},
		Raw:     json.RawMessage(`{"raw":true}`),
	}
}

func TestAccountLoginLifecycle(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	if _, err := c.CreateAccount(ctx, "Bad ID", fake.Platform, "", nil); err == nil {
		t.Fatal("bad id accepted")
	}
	acc, err := c.CreateAccount(ctx, "a1", fake.Platform, "", json.RawMessage(`{"secret":"s3","name":"n"}`))
	if err != nil || acc.Status != model.StatusUnpaired {
		t.Fatalf("create: %+v %v", acc, err)
	}
	if string(acc.Config) != `{"name":"n","secret":"***"}` {
		t.Fatalf("secret not redacted: %s", acc.Config)
	}
	if _, err := c.CreateAccount(ctx, "a1", fake.Platform, "", nil); AsError(err).Code != "conflict" {
		t.Fatalf("duplicate: %v", err)
	}
	if _, _, err := c.Send(ctx, "a1", "u1@fake", model.SendRequest{Content: model.Content{Type: "text", Text: "x"}}); AsError(err).Code != "account_not_ready" {
		t.Fatalf("send while unpaired: %v", err)
	}

	step, err := c.LoginStart(ctx, "a1", "phone")
	if err != nil || step.Step != model.StepInput {
		t.Fatalf("login start: %+v %v", step, err)
	}
	if _, err := c.LoginStart(ctx, "a1", "phone"); AsError(err).Code != "conflict" {
		t.Fatalf("double login: %v", err)
	}
	step, err = c.LoginSubmit(ctx, "a1", map[string]string{"phone": "+10000"})
	if err != nil || step.Step != model.StepDone {
		t.Fatalf("submit: %+v %v", step, err)
	}
	acc, _ = c.GetAccount(ctx, "a1")
	if acc.Status != model.StatusConnecting || acc.Login == nil || acc.Login.Identifier != "+10000" || acc.Self == nil || acc.Self.ID != "self@fake" {
		t.Fatalf("after done: %+v", acc)
	}
	if err := f.Connect(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	acc, _ = c.GetAccount(ctx, "a1")
	if acc.Status != model.StatusConnected || acc.ConnectedAt == nil || string(acc.Device) != `{"v":1}` {
		t.Fatalf("after connect: %+v", acc)
	}
	eventually(t, func() bool {
		cs, _, _ := c.ListContacts(ctx, "a1", "", "", 10)
		return len(cs) == 2 // self + synced Alice
	})
	evs, _, _ := c.ListEvents(ctx, "", store.EventFilter{}, 100)
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, model.EvLoginStep) || !strings.Contains(joined, model.EvAccountStatus) {
		t.Fatalf("events: %s", joined)
	}

	if _, err := c.Logout(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	acc, _ = c.GetAccount(ctx, "a1")
	if acc.Status != model.StatusUnpaired || acc.Login != nil {
		t.Fatalf("after logout: %+v", acc)
	}
	step, _ = c.LoginStart(ctx, "a1", "phone")
	step, _ = c.LoginSubmit(ctx, "a1", map[string]string{"phone": "bad"})
	if step.Step != model.StepFailed {
		t.Fatalf("failed step: %+v", step)
	}
	if err := c.DeleteAccount(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetAccount(ctx, "a1"); AsError(err).Status != http.StatusNotFound {
		t.Fatalf("deleted account: %v", err)
	}
}

func TestIngestSendAndMedia(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	connected(t, c, f, "a1")

	// Inbound text with hints creates chat, contact, and message.new.
	if err := f.Push(ctx, "a1", inbound("m1", "u1@fake", "u1@fake", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := f.Push(ctx, "a1", inbound("m1", "u1@fake", "u1@fake", "hi again")); err != nil {
		t.Fatal(err)
	}
	chats, _, err := c.ListChats(ctx, "a1", store.ChatFilter{}, "", 10)
	if err != nil || len(chats) != 1 || chats[0].UnreadCount != 1 || chats[0].Name != "Alice" || chats[0].LastMessage == nil {
		t.Fatalf("chats: %+v %v", chats, err)
	}
	m, err := c.GetMessage(ctx, "a1", "m1", true)
	if err != nil || m.Content.Text != "hi" || m.Sender.Name != "alice" || string(m.Raw) != `{"raw":true}` {
		t.Fatalf("message: %+v %v", m, err)
	}

	// Inbound image: policy wants it, so it is fetched and flips to ready.
	img := inbound("m2", "u1@fake", "u1@fake", "")
	img.Message.Content = model.Content{Type: model.ContentImage, Attachments: []model.Attachment{{MediaID: "m2", Mime: "image/png", Size: 8, State: model.MediaRemote, RemoteRef: json.RawMessage(`{"ok":1}`)}}}
	if err := f.Push(ctx, "a1", img); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		m, _ := c.GetMessage(ctx, "a1", "m2", false)
		return len(m.Content.Attachments) == 1 && m.Content.Attachments[0].State == model.MediaReady
	})
	mf, err := c.GetMedia(ctx, "m2")
	if err != nil || mf.Path == "" || mf.Attachment.SHA256 == "" {
		t.Fatalf("media: %+v %v", mf, err)
	}
	// A video is not auto-downloaded and stays remote until fetched.
	vid := inbound("m3", "u1@fake", "u1@fake", "")
	vid.Message.Content = model.Content{Type: model.ContentVideo, Attachments: []model.Attachment{{MediaID: "m3", Mime: "video/mp4", State: model.MediaRemote, RemoteRef: json.RawMessage(`{"ok":1}`)}}}
	_ = f.Push(ctx, "a1", vid)
	if mf, _ := c.GetMedia(ctx, "m3"); mf.Attachment.State != model.MediaRemote {
		t.Fatalf("video state: %s", mf.Attachment.State)
	}
	if _, err := c.FetchMedia(ctx, "m3"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { mf, _ := c.GetMedia(ctx, "m3"); return mf.Attachment.State == model.MediaReady })

	// Send text with client_id, then replay.
	sent, replay, err := c.Send(ctx, "a1", "u1@fake", model.SendRequest{ClientID: "c1", Content: model.Content{Type: "text", Text: "yo"}, ReplyTo: "m1"})
	if err != nil || replay || sent.ID != "sent-1" || !sent.FromMe || sent.Status != model.MsgSent || sent.Sender.ID != "self@fake" || sent.ReplyTo != "m1" {
		t.Fatalf("send: %+v %v %v", sent, replay, err)
	}
	if f.Sent[0].ReplyTarget == nil || f.Sent[0].ReplyTarget.Content.Text != "hi" {
		t.Fatalf("reply target not passed: %+v", f.Sent[0].ReplyTarget)
	}
	again, replay, err := c.Send(ctx, "a1", "u1@fake", model.SendRequest{ClientID: "c1", Content: model.Content{Type: "text", Text: "yo"}})
	if err != nil || !replay || again.ID != "sent-1" {
		t.Fatalf("replay: %+v %v %v", again, replay, err)
	}
	if _, _, err := c.Send(ctx, "a1", "u1@fake", model.SendRequest{Content: model.Content{Type: "poll"}}); AsError(err).Status != http.StatusBadRequest {
		t.Fatalf("poll send: %v", err)
	}
	if _, _, err := c.Send(ctx, "a1", "u1@fake", model.SendRequest{ThreadID: "t", Content: model.Content{Type: "text", Text: "x"}}); AsError(err).Code != "unsupported" {
		t.Fatalf("thread send: %v", err)
	}

	// Upload then send as image.
	att, err := c.Upload(ctx, "a1", strings.NewReader("hello-bytes"), adapter.MediaMeta{Mime: "image/png", FileName: "a.png"})
	if err != nil || !strings.HasPrefix(att.MediaID, "upl_") || att.State != model.MediaReady {
		t.Fatalf("upload: %+v %v", att, err)
	}
	sent, _, err = c.Send(ctx, "a1", "u1@fake", model.SendRequest{Content: model.Content{Type: model.ContentImage, Text: "cap", Attachments: []model.Attachment{{MediaID: att.MediaID}}}})
	if err != nil || len(sent.Content.Attachments) != 1 || sent.Content.Attachments[0].SHA256 != att.SHA256 {
		t.Fatalf("send image: %+v %v", sent, err)
	}
	if md, _ := c.GetMedia(ctx, att.MediaID); md.Attachment.URL == "" {
		t.Fatal("upload not linked")
	}
	c.gcOnce() // linked upload must survive gc
	if _, err := c.GetMedia(ctx, att.MediaID); err != nil {
		t.Fatalf("gc removed linked upload: %v", err)
	}

	msgs, _, err := c.ListMessages(ctx, "a1", "u1@fake", MessageQuery{Limit: 10})
	if err != nil || len(msgs) != 5 {
		t.Fatalf("list: %d %v", len(msgs), err)
	}
}

func TestBackfillDoesNotCountOrDownload(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	connected(t, c, f, "a1")
	old := inbound("h1", "u1@fake", "u1@fake", "")
	old.Backfill = true
	old.Chat.UnreadCount = 3
	old.Message.Content = model.Content{Type: model.ContentImage, Attachments: []model.Attachment{{MediaID: "h1", Mime: "image/png", Size: 8, State: model.MediaRemote, RemoteRef: json.RawMessage(`{"ok":1}`)}}}
	if err := f.Push(ctx, "a1", old); err != nil {
		t.Fatal(err)
	}
	ch, _ := c.GetChat(ctx, "a1", "u1@fake")
	if ch.UnreadCount != 3 {
		t.Fatalf("unread from hint: %d", ch.UnreadCount)
	}
	time.Sleep(100 * time.Millisecond)
	if mf, _ := c.GetMedia(ctx, "h1"); mf.Attachment.State != model.MediaRemote {
		t.Fatalf("history media was fetched: %s", mf.Attachment.State)
	}
	// Sender name is resolved at read time once the contact is known.
	m, _ := c.GetMessage(ctx, "a1", "h1", false)
	if m.Sender.Name != "alice" {
		t.Fatalf("sender name: %q", m.Sender.Name)
	}
	if err := f.Push(ctx, "a1", inbound("live1", "u1@fake", "u1@fake", "now")); err != nil {
		t.Fatal(err)
	}
	ch, _ = c.GetChat(ctx, "a1", "u1@fake")
	if ch.UnreadCount != 4 {
		t.Fatalf("live message unread: %d", ch.UnreadCount)
	}
}

func TestReactionsReceiptsEditDelete(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	connected(t, c, f, "a1")
	_ = f.Push(ctx, "a1", inbound("m1", "u1@fake", "u1@fake", "hi"))
	sent, _, _ := c.Send(ctx, "a1", "u1@fake", model.SendRequest{Content: model.Content{Type: "text", Text: "yo"}})

	now := time.Now().UTC()
	err := f.Push(ctx, "a1",
		adapter.Event{Kind: adapter.EvReaction, ChatID: "u1@fake", MessageID: sent.ID, UserID: "u1@fake", Emoji: "👍", At: now},
		adapter.Event{Kind: adapter.EvReceipt, ChatID: "u1@fake", MessageIDs: []string{sent.ID}, UserID: "u1@fake", Receipt: "read", At: now},
		adapter.Event{Kind: adapter.EvMessageUpdate, ChatID: "u1@fake", MessageID: "m1", Content: &model.Content{Type: "text", Text: "hi (edited)"}, At: now},
		adapter.Event{Kind: adapter.EvTyping, ChatID: "u1@fake", UserID: "u1@fake", State: "typing"},
		adapter.Event{Kind: adapter.EvMember, Member: &adapter.Member{ChatID: "g1", UserID: "u2@fake", ChatName: "Bobby", Role: "admin"}},
		adapter.Event{Kind: adapter.EvPlatform, PlatformType: "weird", Raw: json.RawMessage(`{}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := c.GetMessage(ctx, "a1", sent.ID, false)
	if len(m.Reactions) != 1 || m.Status != model.MsgRead {
		t.Fatalf("reaction/receipt: %+v", m)
	}
	m, _ = c.GetMessage(ctx, "a1", "m1", false)
	if m.Content.Text != "hi (edited)" || m.EditedAt == nil {
		t.Fatalf("edit: %+v", m)
	}
	g, err := c.GetChat(ctx, "a1", "g1")
	if err != nil || g.Kind != model.ChatGroup || len(g.Participants) != 1 || g.Participants[0].ChatName != "Bobby" || g.Participants[0].Name != "Bobby" {
		t.Fatalf("group: %+v %v", g, err)
	}
	// Unknown chat is fetched from the adapter (with participants) and stored.
	g2, err := c.GetChat(ctx, "a1", "g2")
	if err != nil || g2.Name != "Group g2" || len(g2.Participants) != 2 {
		t.Fatalf("adapter chat: %+v %v", g2, err)
	}
	if _, err := c.GetChat(ctx, "a1", "missing"); AsError(err).Status != http.StatusNotFound {
		t.Fatalf("missing chat: %v", err)
	}

	edited, err := c.Edit(ctx, "a1", sent.ID, model.Content{Type: "text", Text: "yo2"})
	if err != nil || edited.Content.Text != "yo2" {
		t.Fatalf("edit own: %+v %v", edited, err)
	}
	if _, err := c.Edit(ctx, "a1", "m1", model.Content{Type: "text", Text: "x"}); AsError(err).Status != http.StatusBadRequest {
		t.Fatalf("edit foreign: %v", err)
	}
	if err := c.React(ctx, "a1", "m1", "❤️", false); err != nil || len(f.Reacted) != 1 {
		t.Fatalf("react: %v", err)
	}
	del, err := c.Delete(ctx, "a1", sent.ID)
	if err != nil || del.DeletedAt == nil || del.Content.Type != model.ContentDeleted || len(f.Deleted) != 1 {
		t.Fatalf("delete: %+v %v", del, err)
	}
	if err := c.MarkRead(ctx, "a1", "u1@fake", ""); err != nil || len(f.Read) != 1 || f.Read[0] != "m1" {
		t.Fatalf("mark read: %v %v", err, f.Read)
	}
	ch, _, _ := c.ListChats(ctx, "a1", store.ChatFilter{}, "", 10)
	for _, x := range ch {
		if x.ID == "u1@fake" && x.UnreadCount != 0 {
			t.Fatalf("unread not cleared: %+v", x)
		}
	}
	// platform.event is hidden unless requested.
	evs, _, _ := c.ListEvents(ctx, "", store.EventFilter{Types: []string{model.EvPlatform}}, 10)
	if len(evs) != 1 {
		t.Fatalf("platform events: %d", len(evs))
	}
	evs, _, _ = c.ListEvents(ctx, "", store.EventFilter{}, 100)
	for _, e := range evs {
		if e.Type == model.EvPlatform {
			t.Fatal("platform.event leaked into default listing")
		}
	}
	ct, err := c.PatchContact(ctx, "a1", "u1@fake", ContactPatch{Alias: ptr("Ally")})
	if err != nil || ct.Name != "Ally" || ct.Names.AliasSource != "local" {
		t.Fatalf("alias: %+v %v", ct, err)
	}
	chat, err := c.PatchChat(ctx, "a1", "u1@fake", ChatPatch{Tags: []string{"work"}})
	if err != nil || len(chat.Tags) != 1 {
		t.Fatalf("tags: %+v %v", chat, err)
	}
	tagged, _, _ := c.ListChats(ctx, "a1", store.ChatFilter{Tag: "work"}, "", 10)
	if len(tagged) != 1 {
		t.Fatalf("tag filter: %d", len(tagged))
	}
}

func ptr(s string) *string { return &s }

func TestWaitEventsAndWebhook(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	connected(t, c, f, "a1")
	cursor := c.LastEventID(ctx)

	var mu sync.Mutex
	var got []map[string]any
	var sig, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var payload struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.Unmarshal(b, &payload)
		mu.Lock()
		got = append(got, payload.Events...)
		sig, body = r.Header.Get("X-ChatBridge-Signature"), string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if _, err := c.CreateWebhook(ctx, WebhookInput{URL: srv.URL, Secret: "sec", Types: []string{model.EvMessageNew}}); err != nil {
		t.Fatal(err)
	}

	// Long-poll returns once an event lands.
	done := make(chan int, 1)
	go func() {
		evs, _, _ := c.WaitEvents(ctx, cursor, store.EventFilter{Types: []string{model.EvMessageNew}}, 10, 5*time.Second)
		done <- len(evs)
	}()
	time.Sleep(50 * time.Millisecond)
	_ = f.Push(ctx, "a1", inbound("m1", "u1@fake", "u1@fake", "hi"))
	select {
	case n := <-done:
		if n != 1 {
			t.Fatalf("long poll got %d", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("long poll did not wake")
	}

	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	mu.Lock()
	defer mu.Unlock()
	mac := hmac.New(sha256.New, []byte("sec"))
	mac.Write([]byte(body))
	if sig != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("bad signature %s", sig)
	}
	if got[0]["type"] != model.EvMessageNew {
		t.Fatalf("webhook event: %+v", got[0])
	}
	mu.Unlock()
	// The ack is written after the HTTP response, so wait for it.
	eventually(t, func() bool { ws, _ := c.ListWebhooks(ctx); return len(ws) == 1 && ws[0].Cursor != cursor })
	mu.Lock()
}

func TestMultipleAdapterInstances(t *testing.T) {
	c, f := newCore(t) // f is fake/local
	ctx := context.Background()
	second := fake.New()
	second.Instance = "eu"
	if err := c.Attach(ctx, second); err != nil {
		t.Fatal(err)
	}
	ps := c.Platforms()
	if len(ps) != 1 || len(ps[0].Instances) != 2 || ps[0].Instances[0].ID != "eu" || ps[0].Instances[1].ID != "local" {
		t.Fatalf("platforms: %+v", ps)
	}
	// Two instances: the caller must choose.
	if _, err := c.CreateAccount(ctx, "amb", fake.Platform, "", nil); AsError(err).Status != http.StatusBadRequest {
		t.Fatalf("ambiguous create: %v", err)
	}
	// An account can be provisioned before its adapter connects; it is picked up on attach.
	early, err := c.CreateAccount(ctx, "x", fake.Platform, "later", nil)
	if err != nil || early.Status != model.StatusError || early.Error.Code != "adapter_offline" || early.Adapter != "later" {
		t.Fatalf("provision ahead: %+v %v", early, err)
	}
	if _, _, err := c.Send(ctx, "x", "u1@fake", model.SendRequest{Content: model.Content{Type: "text", Text: "hi"}}); AsError(err).Code != "account_not_ready" {
		t.Fatalf("send before adapter: %v", err)
	}
	later := fake.New()
	later.Instance = "later"
	if err := c.Attach(ctx, later); err != nil {
		t.Fatal(err)
	}
	if err := later.Connect(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.GetAccount(ctx, "x"); a.Status != model.StatusConnected {
		t.Fatalf("after late attach: %+v", a)
	}
	c.Detach(ctx, fake.Platform, "later")
	// Config and deletion are core-owned and work while the adapter is away.
	if a, err := c.UpdateConfig(ctx, "x", json.RawMessage(`{"name":"n2"}`)); err != nil || !strings.Contains(string(a.Config), "n2") {
		t.Fatalf("update config offline: %+v %v", a, err)
	}
	if err := c.DeleteAccount(ctx, "x"); err != nil {
		t.Fatalf("delete without adapter: %v", err)
	}
	acc, err := c.CreateAccount(ctx, "a-eu", fake.Platform, "eu", nil)
	if err != nil || acc.Adapter != "eu" {
		t.Fatalf("create on eu: %+v %v", acc, err)
	}
	if _, err := c.CreateAccount(ctx, "a-local", fake.Platform, "local", nil); err != nil {
		t.Fatal(err)
	}
	// Each instance only sees its own account.
	if rows := c.PlatformAccounts(ctx, fake.Platform, "eu"); len(rows) != 1 || rows[0].ID != "a-eu" {
		t.Fatalf("eu accounts: %+v", rows)
	}
	if err := second.Connect(ctx, "a-eu"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Send(ctx, "a-eu", "u1@fake", model.SendRequest{Content: model.Content{Type: "text", Text: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if len(second.Sent) != 1 || len(f.Sent) != 0 {
		t.Fatalf("routed to wrong instance: eu=%d local=%d", len(second.Sent), len(f.Sent))
	}
	// Rebinding is only allowed while unpaired.
	if _, err := c.Rebind(ctx, "a-eu", "local"); AsError(err).Code != "conflict" {
		t.Fatalf("rebind connected: %v", err)
	}
	moved, err := c.Rebind(ctx, "a-local", "eu")
	if err != nil || moved.Adapter != "eu" {
		t.Fatalf("rebind: %+v %v", moved, err)
	}
	// Detaching one instance only takes its accounts offline.
	c.Detach(ctx, fake.Platform, "eu")
	a, _ := c.GetAccount(ctx, "a-eu")
	if a.Status != model.StatusDisconnected || a.Error == nil || a.Error.Code != "adapter_offline" {
		t.Fatalf("after detach: %+v", a)
	}
	if _, err := c.CreateAccount(ctx, "a2", fake.Platform, "", nil); err != nil {
		t.Fatalf("single instance again: %v", err)
	}
}

func TestSendFailureMapsToPlatformError(t *testing.T) {
	c, f := newCore(t)
	ctx := context.Background()
	connected(t, c, f, "a1")
	f.FailSend = adapter.Errorf(adapter.ErrRateLimited, "slow down")
	_, _, err := c.Send(ctx, "a1", "u1@fake", model.SendRequest{Content: model.Content{Type: "text", Text: "x"}})
	if ce := AsError(err); ce.Status != http.StatusTooManyRequests || ce.Code != adapter.ErrRateLimited {
		t.Fatalf("mapped: %+v", ce)
	}
	f.FailSend = errors.New("boom")
	_, _, err = c.Send(ctx, "a1", "u1@fake", model.SendRequest{Content: model.Content{Type: "text", Text: "x"}})
	if ce := AsError(err); ce.Status != http.StatusInternalServerError {
		t.Fatalf("plain error: %+v", ce)
	}
	msgs, _, _ := c.ListMessages(ctx, "a1", "u1@fake", MessageQuery{Limit: 10})
	if len(msgs) != 0 {
		t.Fatal("failed send was stored")
	}
}
