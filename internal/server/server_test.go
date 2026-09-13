package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapter/fake"
	"gimhq/chat-bridge/internal/core"
	"gimhq/chat-bridge/internal/media"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

const testToken = "0123456789abcdef0123456789abcdef"

type env struct {
	t    *testing.T
	h    http.Handler
	fake *fake.Adapter
	core *core.Core
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	blobs, _ := media.Open(filepath.Join(dir, "media"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := core.New(core.Options{Store: st, Blobs: blobs, DataDir: dir, Logger: log})
	f := fake.New()
	c.Register(f)
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Stop(context.Background()); _ = st.Close() })
	return &env{t: t, h: New(Options{Core: c, Token: testToken, Version: "t", MaxUploadBytes: 1 << 20, Logger: log}), fake: f, core: c}
}

func (e *env) do(method, path string, body any, headers ...string) (*httptest.ResponseRecorder, map[string]any) {
	e.t.Helper()
	var rdr io.Reader
	ct := "application/json"
	switch b := body.(type) {
	case nil:
	case *bytes.Buffer:
		rdr, ct = b, headers[0]
	default:
		raw, _ := json.Marshal(b)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if rdr != nil {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func (e *env) connected(id string) {
	e.t.Helper()
	if rec, _ := e.do("POST", "/v1/accounts", map[string]any{"id": id, "platform": fake.Platform}); rec.Code != 201 {
		e.t.Fatalf("create account: %d %s", rec.Code, rec.Body)
	}
	if err := e.fake.Connect(context.Background(), id); err != nil {
		e.t.Fatal(err)
	}
}

func errCode(out map[string]any) string {
	if e, ok := out["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func TestAuthAndMeta(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("GET", "/v1/status", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("no token: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/healthz", nil)
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("healthz: %d", rec.Code)
	}
	rec, out := e.do("GET", "/v1/platforms", nil)
	if rec.Code != 200 || len(out["platforms"].([]any)) != 1 {
		t.Fatalf("platforms: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/status", nil)
	if rec.Code != 200 || out["version"] != "t" || out["events_cursor"] == nil {
		t.Fatalf("status: %d %v", rec.Code, out)
	}
}

func TestAccountsAndLogin(t *testing.T) {
	e := newEnv(t)
	rec, out := e.do("POST", "/v1/accounts", map[string]any{"id": "a1", "platform": "nope"})
	if rec.Code != 400 || errCode(out) != "invalid_request" {
		t.Fatalf("bad platform: %d %v", rec.Code, out)
	}
	rec, out = e.do("POST", "/v1/accounts", map[string]any{"id": "a1", "platform": fake.Platform, "config": map[string]any{"secret": "x"}})
	if rec.Code != 201 || out["status"] != model.StatusUnpaired || out["config"].(map[string]any)["secret"] != "***" {
		t.Fatalf("create: %d %v", rec.Code, out)
	}
	rec, out = e.do("POST", "/v1/accounts/a1/login", map[string]any{"flow": "qr"})
	if rec.Code != 200 || out["step"] != "display" {
		t.Fatalf("login qr: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/accounts/a1/login", nil)
	if rec.Code != 200 || out["display"].(map[string]any)["data"] != "QR1" {
		t.Fatalf("login get: %d %v", rec.Code, out)
	}
	if rec, _ := e.do("DELETE", "/v1/accounts/a1/login", nil); rec.Code != 204 {
		t.Fatalf("cancel: %d", rec.Code)
	}
	rec, out = e.do("GET", "/v1/accounts/a1/login", nil)
	if rec.Code != 404 {
		t.Fatalf("login after cancel: %d %v", rec.Code, out)
	}
	_, _ = e.do("POST", "/v1/accounts/a1/login", map[string]any{"flow": "phone"})
	rec, out = e.do("POST", "/v1/accounts/a1/login/submit", map[string]any{"fields": map[string]string{"phone": "+1"}})
	if rec.Code != 200 || out["step"] != "done" {
		t.Fatalf("submit: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/accounts/a1", nil)
	if rec.Code != 200 || out["login"].(map[string]any)["identifier"] != "+1" || out["self"].(map[string]any)["phone"] != "+10000" {
		t.Fatalf("account detail: %d %v", rec.Code, out)
	}
	rec, out = e.do("PATCH", "/v1/accounts/a1/self", map[string]any{"name": "x"})
	if rec.Code != 409 || errCode(out) != "account_not_ready" { // capability present, account still connecting
		t.Fatalf("self update: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/accounts", nil)
	if rec.Code != 200 || len(out["accounts"].([]any)) != 1 {
		t.Fatalf("list: %d %v", rec.Code, out)
	}
	if rec, _ := e.do("DELETE", "/v1/accounts/a1", nil); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec, _ := e.do("GET", "/v1/accounts/a1", nil); rec.Code != 404 {
		t.Fatalf("after delete: %d", rec.Code)
	}
}

func TestMessagesMediaAndChats(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, out := e.do("POST", "/v1/accounts", map[string]any{"id": "a1", "platform": fake.Platform})
	rec, out := e.do("POST", "/v1/accounts/a1/chats/u1%40fake/messages", map[string]any{"content": map[string]any{"type": "text", "text": "hi"}})
	if rec.Code != 409 || errCode(out) != "account_not_ready" {
		t.Fatalf("send unpaired: %d %v", rec.Code, out)
	}
	_ = e.fake.Connect(ctx, "a1")

	rec, out = e.do("POST", "/v1/accounts/a1/chats/resolve", map[string]any{"handle": "+1"})
	if rec.Code != 200 || out["chat_id"] != "1@fake" {
		t.Fatalf("resolve: %d %v", rec.Code, out)
	}
	rec, out = e.do("POST", "/v1/accounts/a1/chats/u1%40fake/messages", map[string]any{"client_id": "c1", "content": map[string]any{"type": "text", "text": "hi"}})
	if rec.Code != 201 || out["chat_id"] != "u1@fake" || out["from_me"] != true {
		t.Fatalf("send: %d %v", rec.Code, out)
	}
	msgID := out["id"].(string)
	rec, _ = e.do("POST", "/v1/accounts/a1/chats/u1%40fake/messages", map[string]any{"client_id": "c1", "content": map[string]any{"type": "text", "text": "hi"}})
	if rec.Code != 200 {
		t.Fatalf("replay: %d", rec.Code)
	}
	rec, out = e.do("POST", "/v1/accounts/a1/chats/u1@fake/messages", map[string]any{"content": map[string]any{"type": "image"}})
	if rec.Code != 400 {
		t.Fatalf("image without attachments: %d %v", rec.Code, out)
	}

	// Multipart upload then send.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "pic.png")
	_, _ = fw.Write([]byte("PNGDATA"))
	_ = mw.Close()
	rec, out = e.do("POST", "/v1/accounts/a1/media", &buf, mw.FormDataContentType())
	if rec.Code != 201 || out["state"] != "ready" {
		t.Fatalf("upload: %d %v", rec.Code, out)
	}
	mediaID := out["media_id"].(string)
	rec, out = e.do("POST", "/v1/accounts/a1/chats/u1@fake/messages", map[string]any{"content": map[string]any{
		"type": "image", "text": "cap", "attachments": []map[string]any{{"media_id": mediaID}}}})
	if rec.Code != 201 {
		t.Fatalf("send image: %d %v", rec.Code, out)
	}
	rec, _ = e.do("GET", "/v1/media/"+mediaID, nil)
	if rec.Code != 200 || rec.Body.String() != "PNGDATA" || rec.Header().Get("Content-Disposition") == "" {
		t.Fatalf("get media: %d %q", rec.Code, rec.Body.String())
	}
	rec, out = e.do("GET", "/v1/media/"+mediaID+"/meta", nil)
	if rec.Code != 200 || out["file_name"] != "pic.png" {
		t.Fatalf("meta: %d %v", rec.Code, out)
	}

	// Inbound via adapter, then list/get/react/delete/edit over HTTP.
	_ = e.fake.Push(ctx, "a1", adapter.Event{Kind: adapter.EvMessage,
		Message: &model.Message{ID: "in1", ChatID: "u1@fake", Sender: model.Sender{ID: "u1@fake"}, Content: model.Content{Type: "text", Text: "yo"}},
		Chat:    &model.Chat{ID: "u1@fake", Kind: model.ChatDirect, Name: "Alice"}})
	rec, out = e.do("GET", "/v1/accounts/a1/chats/u1%40fake/messages?limit=2", nil)
	if rec.Code != 200 || len(out["messages"].([]any)) != 2 || out["next_cursor"] == nil {
		t.Fatalf("list messages: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/accounts/a1/chats/u1%40fake/messages?cursor="+out["next_cursor"].(string), nil)
	if rec.Code != 200 || len(out["messages"].([]any)) != 1 {
		t.Fatalf("page 2: %d %v", rec.Code, out)
	}
	if rec, _ := e.do("GET", "/v1/accounts/a1/chats/u1%40fake/messages?cursor=zzz", nil); rec.Code != 400 {
		t.Fatalf("bad cursor: %d", rec.Code)
	}
	if rec, _ := e.do("PUT", "/v1/accounts/a1/messages/in1/reactions/%F0%9F%91%8D", nil); rec.Code != 204 {
		t.Fatalf("react: %d", rec.Code)
	}
	rec, out = e.do("GET", "/v1/accounts/a1/messages/in1", nil)
	if rec.Code != 200 || len(out["reactions"].([]any)) != 1 || out["reactions"].([]any)[0].(map[string]any)["emoji"] != "👍" {
		t.Fatalf("get message: %d %v", rec.Code, out)
	}
	rec, out = e.do("PATCH", "/v1/accounts/a1/messages/"+msgID, map[string]any{"content": map[string]any{"type": "text", "text": "hi2"}})
	if rec.Code != 200 || out["content"].(map[string]any)["text"] != "hi2" {
		t.Fatalf("edit: %d %v", rec.Code, out)
	}
	rec, out = e.do("DELETE", "/v1/accounts/a1/messages/"+msgID, nil)
	if rec.Code != 200 || out["deleted_at"] == nil {
		t.Fatalf("delete: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/accounts/a1/chats", nil)
	if rec.Code != 200 || len(out["chats"].([]any)) != 1 {
		t.Fatalf("chats: %d %v", rec.Code, out)
	}
	rec, out = e.do("PATCH", "/v1/accounts/a1/chats/u1@fake", map[string]any{"tags": []string{"x"}, "muted": true})
	if rec.Code != 200 || out["muted"] != true {
		t.Fatalf("patch chat: %d %v", rec.Code, out)
	}
	if rec, _ := e.do("POST", "/v1/accounts/a1/chats/u1@fake/read", nil); rec.Code != 204 {
		t.Fatalf("read: %d", rec.Code)
	}
	if rec, _ := e.do("POST", "/v1/accounts/a1/chats/u1@fake/typing", map[string]any{"state": "typing"}); rec.Code != 204 {
		t.Fatalf("typing: %d", rec.Code)
	}
	rec, out = e.do("GET", "/v1/accounts/a1/chats/g1", nil)
	if rec.Code != 200 || len(out["participants"].([]any)) != 2 {
		t.Fatalf("group chat: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/accounts/a1/contacts?q=alice", nil)
	if rec.Code != 200 {
		t.Fatalf("contacts: %d %v", rec.Code, out)
	}
	rec, out = e.do("PATCH", "/v1/accounts/a1/contacts/u1@fake", map[string]any{"alias": "Al"})
	if rec.Code != 200 || out["name"] != "Al" {
		t.Fatalf("alias: %d %v", rec.Code, out)
	}
}

func TestEventsAndWebhooks(t *testing.T) {
	e := newEnv(t)
	e.connected("a1")
	rec, out := e.do("GET", "/v1/events?types=account.status", nil)
	if rec.Code != 200 || len(out["events"].([]any)) == 0 {
		t.Fatalf("events: %d %v", rec.Code, out)
	}
	cursor := out["next_cursor"].(string)
	rec, out = e.do("GET", "/v1/events?cursor="+cursor+"&wait=0", nil)
	if rec.Code != 200 || len(out["events"].([]any)) != 0 {
		t.Fatalf("events after cursor: %d %v", rec.Code, out)
	}
	rec, out = e.do("POST", "/v1/webhooks", map[string]any{"url": "http://127.0.0.1:1/x", "secret": "s"})
	if rec.Code != 201 || out["id"] == nil {
		t.Fatalf("create webhook: %d %v", rec.Code, out)
	}
	id := out["id"].(string)
	rec, out = e.do("GET", "/v1/webhooks", nil)
	if rec.Code != 200 || len(out["webhooks"].([]any)) != 1 {
		t.Fatalf("list webhooks: %d %v", rec.Code, out)
	}
	if rec, _ := e.do("DELETE", "/v1/webhooks/"+id, nil); rec.Code != 204 {
		t.Fatalf("delete webhook: %d", rec.Code)
	}
	if rec, _ := e.do("DELETE", "/v1/webhooks/"+id, nil); rec.Code != 404 {
		t.Fatalf("delete twice: %d", rec.Code)
	}
}

func TestChatCreateSearchSelfAndBlock(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connected("a1")

	rec, out := e.do("POST", "/v1/accounts/a1/chats", map[string]any{"name": "Team", "members": []string{"u1@fake"}})
	if rec.Code != 201 || out["kind"] != "group" || out["name"] != "Team" {
		t.Fatalf("create chat: %d %v", rec.Code, out)
	}
	chatID := out["id"].(string)
	rec, out = e.do("POST", "/v1/accounts/a1/chats", map[string]any{"kind": "direct", "name": "x"})
	if rec.Code != 400 {
		t.Fatalf("create direct: %d %v", rec.Code, out)
	}
	rec, out = e.do("PATCH", "/v1/accounts/a1/chats/"+chatID, map[string]any{"name": "Team 2", "muted": true})
	if rec.Code != 200 || out["name"] != "Team 2" || out["muted"] != true {
		t.Fatalf("rename: %d %v", rec.Code, out)
	}

	// Search over stored text; backfill query is accepted and served by the fake's history.
	_ = e.fake.Push(ctx, "a1", adapter.Event{Kind: adapter.EvMessage, Message: &model.Message{ID: "m1", ChatID: "u1@fake", Sender: model.Sender{ID: "u1@fake"},
		Timestamp: time.Now().UTC(), Content: model.Content{Type: model.ContentText, Text: "quarterly numbers"}}})
	rec, out = e.do("GET", "/v1/accounts/a1/messages/search?q=quarterly", nil)
	if rec.Code != 200 || len(out["messages"].([]any)) != 1 {
		t.Fatalf("search: %d %v", rec.Code, out)
	}
	if rec, _ = e.do("GET", "/v1/accounts/a1/messages/search", nil); rec.Code != 400 {
		t.Fatalf("search without q: %d", rec.Code)
	}
	e.fake.History = map[string][]model.Message{"u1@fake": {{ID: "h1", ChatID: "u1@fake", Sender: model.Sender{ID: "u1@fake"},
		Timestamp: time.Now().UTC().Add(-time.Hour), Content: model.Content{Type: model.ContentText, Text: "old"}}}}
	rec, out = e.do("GET", "/v1/accounts/a1/chats/u1%40fake/messages?limit=5&backfill=1", nil)
	if rec.Code != 200 || len(out["messages"].([]any)) != 2 {
		t.Fatalf("backfill: %d %v", rec.Code, out)
	}

	// Self update with an uploaded avatar.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "me.png")
	_, _ = fw.Write([]byte("PNG"))
	_ = mw.Close()
	rec, out = e.do("POST", "/v1/accounts/a1/media", &buf, mw.FormDataContentType())
	if rec.Code != 201 {
		t.Fatalf("upload: %d %v", rec.Code, out)
	}
	rec, out = e.do("PATCH", "/v1/accounts/a1/self", map[string]any{"name": "New Me", "avatar_media_id": out["media_id"]})
	self, _ := out["self"].(map[string]any)
	if rec.Code != 200 || self == nil || self["name"] != "New Me" || self["avatar"] == nil {
		t.Fatalf("self: %d %v", rec.Code, out)
	}
	if rec, _ = e.do("PATCH", "/v1/accounts/a1/self", map[string]any{}); rec.Code != 400 {
		t.Fatalf("empty self patch: %d", rec.Code)
	}

	// Block through the platform.
	rec, out = e.do("PATCH", "/v1/accounts/a1/contacts/u1%40fake", map[string]any{"blocked": true})
	if rec.Code != 200 || out["blocked"] != true || len(e.fake.Blocked) != 1 {
		t.Fatalf("block: %d %v", rec.Code, out)
	}
}

func TestRequestsEndpoints(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connected("a1")
	_ = e.fake.Push(ctx, "a1", adapter.Event{Kind: adapter.EvRequest, Request: &adapter.Request{Key: "call:1", Kind: model.RequestKindCall,
		FromID: "u1@fake", FromName: "Alice", ChatID: "u1@fake", CallKind: "voice", CreatedAt: time.Now().UTC()}})
	_ = e.fake.Push(ctx, "a1", adapter.Event{Kind: adapter.EvRequest, Request: &adapter.Request{Key: "invite:g1", Kind: model.RequestKindChatInvite,
		FromID: "u1@fake", ChatID: "g1@fake", ChatName: "Team", CreatedAt: time.Now().UTC().Add(time.Second)}})

	rec, out := e.do("GET", "/v1/accounts/a1/requests?state=pending", nil)
	list, _ := out["requests"].([]any)
	if rec.Code != 200 || len(list) != 2 {
		t.Fatalf("list: %d %v", rec.Code, out)
	}
	invite := list[0].(map[string]any)
	call := list[1].(map[string]any)
	if invite["kind"] != "chat_invite" || call["kind"] != "call" || len(call["actions"].([]any)) != 1 {
		t.Fatalf("rows: %v", list)
	}
	rec, out = e.do("GET", "/v1/accounts/a1/requests/"+invite["id"].(string), nil)
	if rec.Code != 200 || out["chat"].(map[string]any)["name"] != "Team" {
		t.Fatalf("get: %d %v", rec.Code, out)
	}
	if rec, out = e.do("POST", "/v1/accounts/a1/requests/"+call["id"].(string)+"/accept", nil); rec.Code != 400 {
		t.Fatalf("accept call: %d %v", rec.Code, out)
	}
	rec, out = e.do("POST", "/v1/accounts/a1/requests/"+call["id"].(string)+"/reject", map[string]any{"reason": "busy"})
	if rec.Code != 200 || out["state"] != "rejected" || e.fake.Answered[0].Reason != "busy" {
		t.Fatalf("reject call: %d %v", rec.Code, out)
	}
	if rec, _ = e.do("POST", "/v1/accounts/a1/requests/"+call["id"].(string)+"/reject", nil); rec.Code != 409 {
		t.Fatalf("reject twice: %d", rec.Code)
	}
	rec, out = e.do("POST", "/v1/accounts/a1/requests/"+invite["id"].(string)+"/accept", nil)
	if rec.Code != 200 || out["state"] != "accepted" {
		t.Fatalf("accept invite: %d %v", rec.Code, out)
	}
	_ = e.fake.Push(ctx, "a1", adapter.Event{Kind: adapter.EvRequest, Request: &adapter.Request{Key: "call:2", Kind: model.RequestKindCall, FromID: "u2@fake", CreatedAt: time.Now().UTC().Add(2 * time.Second)}})
	_, out = e.do("GET", "/v1/accounts/a1/requests?state=pending&kind=call", nil)
	ringing := out["requests"].([]any)[0].(map[string]any)
	if acts := ringing["actions"].([]any); len(acts) != 1 || acts[0] != "ignore" {
		t.Fatalf("call actions: %v", acts)
	}
	answers := len(e.fake.Answered)
	if rec, out = e.do("POST", "/v1/accounts/a1/requests/"+ringing["id"].(string)+"/ignore", nil); rec.Code != 200 || out["state"] != "ignored" || len(e.fake.Answered) != answers {
		t.Fatalf("ignore call: %d %v", rec.Code, out)
	}
	if rec, _ = e.do("GET", "/v1/accounts/a1/requests/req_nope", nil); rec.Code != 404 {
		t.Fatalf("missing: %d", rec.Code)
	}
	rec, out = e.do("GET", "/v1/events?types=request.new,request.updated", nil)
	if evs, _ := out["events"].([]any); rec.Code != 200 || len(evs) != 6 {
		t.Fatalf("events: %d %v", rec.Code, out)
	}
}

func TestEmbeddedUI(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ui := fstest.MapFS{
		"index.html":       {Data: []byte("<html>app</html>")},
		"assets/app-1a.js": {Data: []byte("console.log(1)")},
		"favicon.svg":      {Data: []byte("<svg/>")},
	}
	h := New(Options{Token: testToken, Logger: log, UI: ui})
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := get("/"); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/" {
		t.Fatalf("root redirect: %d %v", rec.Code, rec.Header())
	}
	if rec := get("/ui"); rec.Code != http.StatusMovedPermanently {
		t.Fatalf("/ui redirect: %d", rec.Code)
	}
	rec := get("/ui/")
	if rec.Code != 200 || rec.Body.String() != "<html>app</html>" || rec.Header().Get("Cache-Control") != "no-cache" ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("index: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	rec = get("/ui/assets/app-1a.js")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %v", rec.Code, rec.Header())
	}
	if rec = get("/ui/accounts/wa-main/chats"); rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
		t.Fatalf("spa fallback: %d %q", rec.Code, rec.Body.String())
	}
	if rec = get("/ui/assets/missing.js"); rec.Code != 404 {
		t.Fatalf("missing asset: %d", rec.Code)
	}
	if rec = get("/ui/../../etc/passwd"); rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
		t.Fatalf("traversal must stay inside the app: %d %q", rec.Code, rec.Body.String())
	}
	// Without a build the UI path explains how to build it; the API is unaffected.
	h = New(Options{Token: testToken, Logger: log})
	if rec = get("/ui/"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "bun run build") {
		t.Fatalf("unbuilt: %d %q", rec.Code, rec.Body.String())
	}
}

func TestPersonsEndpoints(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.connected("a1")
	_ = e.fake.Push(ctx, "a1", adapter.Event{Kind: adapter.EvMessage,
		Message: &model.Message{ID: "m1", ChatID: "u1@fake", Sender: model.Sender{ID: "u1@fake"}, Timestamp: time.Now().UTC(), Content: model.Content{Type: "text", Text: "hello"}},
		Chat:    &model.Chat{ID: "u1@fake", Kind: model.ChatDirect},
		Sender:  &model.Contact{ID: "u1@fake", Names: model.Names{Profile: "Alice"}}})

	rec, out := e.do("POST", "/v1/persons", map[string]any{"name": "Alice", "tags": []string{"work"}, "links": []map[string]string{{"account_id": "a1", "user_id": "u1@fake"}}})
	if rec.Code != 201 || len(out["links"].([]any)) != 1 || len(out["channels"].([]any)) != 1 {
		t.Fatalf("create: %d %v", rec.Code, out)
	}
	id := out["id"].(string)
	if rec, out = e.do("POST", "/v1/persons", map[string]any{"name": "Other", "links": []map[string]string{{"account_id": "a1", "user_id": "u1@fake"}}}); rec.Code != 409 {
		t.Fatalf("conflict: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/persons?tag=work&q=alice", nil)
	if rec.Code != 200 || len(out["persons"].([]any)) != 1 {
		t.Fatalf("list: %d %v", rec.Code, out)
	}
	if rec, out = e.do("PATCH", "/v1/persons/"+id, map[string]any{"notes": "PM"}); rec.Code != 200 || out["notes"] != "PM" || out["name"] != "Alice" {
		t.Fatalf("patch: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/persons/"+id+"/chats", nil)
	if chats, _ := out["chats"].([]any); rec.Code != 200 || len(chats) != 1 || chats[0].(map[string]any)["person_id"] != id {
		t.Fatalf("chats: %d %v", rec.Code, out)
	}
	rec, out = e.do("GET", "/v1/persons/"+id+"/messages?scope=all", nil)
	if msgs, _ := out["messages"].([]any); rec.Code != 200 || len(msgs) != 1 || msgs[0].(map[string]any)["sender"].(map[string]any)["person_id"] != id {
		t.Fatalf("messages: %d %v", rec.Code, out)
	}
	if rec, _ = e.do("GET", "/v1/persons/"+id+"/messages?scope=bogus", nil); rec.Code != 400 {
		t.Fatalf("bad scope: %d", rec.Code)
	}
	if rec, out = e.do("GET", "/v1/accounts/a1/chats?person="+id, nil); rec.Code != 200 || len(out["chats"].([]any)) != 1 {
		t.Fatalf("chat filter: %d %v", rec.Code, out)
	}
	if rec, out = e.do("GET", "/v1/events?types=message.new&person="+id, nil); rec.Code != 200 || len(out["events"].([]any)) != 1 {
		t.Fatalf("event filter: %d %v", rec.Code, out)
	}
	if rec, _ = e.do("GET", "/v1/events?person=per_nope", nil); rec.Code != 404 {
		t.Fatalf("unknown person filter: %d", rec.Code)
	}
	if rec, out = e.do("GET", "/v1/persons/suggest", nil); rec.Code != 200 || out["suggestions"] == nil {
		t.Fatalf("suggest: %d %v", rec.Code, out)
	}
	_, other := e.do("POST", "/v1/persons", map[string]any{"name": "Dup", "tags": []string{"vip"}})
	if rec, out = e.do("POST", "/v1/persons/"+id+"/merge", map[string]any{"from": []string{other["id"].(string)}}); rec.Code != 200 || len(out["tags"].([]any)) != 2 {
		t.Fatalf("merge: %d %v", rec.Code, out)
	}
	if rec, out = e.do("DELETE", "/v1/persons/"+id+"/links/a1/u1%40fake", nil); rec.Code != 200 || len(out["links"].([]any)) != 0 {
		t.Fatalf("unlink: %d %v", rec.Code, out)
	}
	if rec, out = e.do("POST", "/v1/persons/"+id+"/links", map[string]any{"account_id": "a1", "user_id": "u1@fake"}); rec.Code != 200 || len(out["links"].([]any)) != 1 {
		t.Fatalf("relink: %d %v", rec.Code, out)
	}
	if rec, _ = e.do("DELETE", "/v1/persons/"+id, nil); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec, _ = e.do("GET", "/v1/persons/"+id, nil); rec.Code != 404 {
		t.Fatalf("deleted: %d", rec.Code)
	}
}
