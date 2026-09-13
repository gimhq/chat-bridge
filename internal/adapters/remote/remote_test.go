package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/core"
	"gimhq/chat-bridge/internal/media"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

const token = "adapter-token-0123456789"

// client is a minimal foreign adapter: it answers core → adapter calls from a handler map and
// can issue adapter → core requests.
type client struct {
	t        *testing.T
	conn     *websocket.Conn
	base     string
	mu       sync.Mutex
	nextID   int
	pending  map[int]chan rpcMessage
	handlers map[string]func(params json.RawMessage) (any, *rpcError)
	seen     chan string
}

func newEnv(t *testing.T) (*core.Core, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	blobs, _ := media.Open(filepath.Join(dir, "media"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := core.New(core.Options{Store: st, Blobs: blobs, DataDir: dir, Logger: log, Media: core.MediaPolicy{AutoDownload: []string{model.ContentImage}, MaxBytes: 1 << 20}})
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	hub := NewHub(c, token, "", log)
	srv := httptest.NewServer(hub)
	t.Cleanup(func() { srv.Close(); c.Stop(context.Background()); _ = st.Close() })
	return c, srv
}

func dial(t *testing.T, srv *httptest.Server) *client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, conn: conn, base: srv.URL, pending: map[int]chan rpcMessage{}, handlers: map[string]func(json.RawMessage) (any, *rpcError){}, seen: make(chan string, 64)}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return c
}

func (c *client) send(msg rpcMessage) {
	msg.JSONRPC = "2.0"
	b, _ := json.Marshal(msg)
	if err := c.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Log("write:", err)
	}
}

func (c *client) call(method string, params any) rpcMessage {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	raw, _ := json.Marshal(params)
	c.send(rpcMessage{ID: json.RawMessage(strings.TrimSpace(string(mustJSON(id)))), Method: method, Params: raw})
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		c.t.Fatalf("no response to %s", method)
		return rpcMessage{}
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// loop serves incoming frames; must run after hello.
func (c *client) loop() {
	for {
		_, data, err := c.conn.Read(context.Background())
		if err != nil {
			return
		}
		var msg rpcMessage
		_ = json.Unmarshal(data, &msg)
		if msg.Method == "" {
			var id int
			_ = json.Unmarshal(msg.ID, &id)
			c.mu.Lock()
			ch := c.pending[id]
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		c.seen <- msg.Method
		reply := rpcMessage{ID: msg.ID}
		if h, ok := c.handlers[msg.Method]; ok {
			res, rerr := h(msg.Params)
			if rerr != nil {
				reply.Error = rerr
			} else {
				reply.Result = mustJSON(res)
			}
		} else {
			reply.Error = &rpcError{Code: codeMethodNotFound, Message: "unhandled " + msg.Method}
		}
		c.send(reply)
	}
}

func (c *client) expect(method string) {
	c.t.Helper()
	for {
		select {
		case m := <-c.seen:
			if m == method {
				return
			}
		case <-time.After(5 * time.Second):
			c.t.Fatalf("did not receive %s", method)
		}
	}
}

func (c *client) put(account, mediaID, mime string, body []byte) map[string]any {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPut, c.base+"/media", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Account-Id", account)
	req.Header.Set("X-Media-Id", mediaID)
	req.Header.Set("Content-Type", mime)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 {
		c.t.Fatalf("put media: %d %v", resp.StatusCode, out)
	}
	return out
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
	t.Fatal("condition not met")
}

func TestRemoteAdapterEndToEnd(t *testing.T) {
	c, srv := newEnv(t)
	ctx := context.Background()
	cl := dial(t, srv)

	// hello must be the first frame; the reply lists accounts and media endpoints.
	hello := map[string]any{"protocol": 1, "platform": "signal", "adapter": map[string]string{"name": "sig-py", "version": "0.1"},
		"capabilities":  []string{"send.text", "send.media", "chat.resolve"},
		"login_flows":   []map[string]string{{"id": "qr", "name": "QR"}},
		"config_schema": map[string]any{"type": "object", "properties": map[string]any{"number": map[string]any{"type": "string", "x-secret": true}}}}
	raw, _ := json.Marshal(hello)
	cl.send(rpcMessage{ID: json.RawMessage("1"), Method: "hello", Params: raw})
	_, data, err := cl.conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var reply rpcMessage
	_ = json.Unmarshal(data, &reply)
	var hr helloResult
	if err := json.Unmarshal(reply.Result, &hr); err != nil || reply.Error != nil {
		t.Fatalf("hello reply: %s", data)
	}
	if !strings.HasSuffix(hr.Media.PutURL, "/adapter/v1/media") || len(hr.Accounts) != 0 {
		t.Fatalf("hello result: %+v", hr)
	}
	eventually(t, func() bool {
		for _, p := range c.Platforms() {
			if p.ID == "signal" && len(p.Capabilities) == 3 && len(p.LoginFlows) == 1 {
				return true
			}
		}
		return false
	})

	// Handlers for core → adapter calls.
	cl.handlers["account.add"] = func(json.RawMessage) (any, *rpcError) { return map[string]any{}, nil }
	cl.handlers["login.start"] = func(json.RawMessage) (any, *rpcError) {
		return model.LoginStep{Flow: "qr", Step: model.StepDisplay, Display: &model.LoginDisplay{Type: "qr", Data: "QR-DATA"}}, nil
	}
	cl.handlers["login.refresh"] = cl.handlers["login.start"]
	cl.handlers["contact.list"] = func(json.RawMessage) (any, *rpcError) {
		return map[string]any{"contacts": []model.Contact{{ID: "+1", Handle: "+1", Names: model.Names{Profile: "Alice"}}}}, nil
	}
	cl.handlers["message.send"] = func(p json.RawMessage) (any, *rpcError) {
		var req struct {
			ChatID      string           `json:"chat_id"`
			Content     model.Content    `json:"content"`
			Attachments []wireAttachment `json:"attachments"`
		}
		_ = json.Unmarshal(p, &req)
		for _, att := range req.Attachments {
			// The adapter fetches upload bytes over HTTP before sending.
			r, _ := http.NewRequest(http.MethodGet, cl.base+strings.TrimPrefix(att.URL, "/adapter/v1"), nil)
			r.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(r)
			if err != nil || resp.StatusCode != 200 {
				return nil, &rpcError{Code: codePlatform, Message: "fetch upload failed"}
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(b) != "UPLOAD-BYTES" {
				return nil, &rpcError{Code: codePlatform, Message: "wrong upload bytes"}
			}
		}
		return model.Message{ID: "s1", ChatID: req.ChatID, Timestamp: time.Now().UTC(), Content: req.Content}, nil
	}
	cl.handlers["media.fetch"] = func(p json.RawMessage) (any, *rpcError) {
		var req struct {
			AccountID string          `json:"account_id"`
			MediaID   string          `json:"media_id"`
			RemoteRef json.RawMessage `json:"remote_ref"`
		}
		_ = json.Unmarshal(p, &req)
		if string(req.RemoteRef) != `{"k":"v"}` {
			return nil, &rpcError{Code: codeInvalidInput, Message: "remote_ref lost: " + string(req.RemoteRef)}
		}
		cl.put(req.AccountID, req.MediaID, "image/png", []byte("PNG-BYTES"))
		return map[string]any{}, nil
	}
	cl.handlers["chat.resolve"] = func(json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: codeInvalidTarget, Message: "nobody there"}
	}
	go cl.loop()

	// Creating an account routes account.add to the remote adapter.
	acc, err := c.CreateAccount(ctx, "sig1", "signal", "", json.RawMessage(`{"number":"+1"}`))
	if err != nil {
		t.Fatal(err)
	}
	cl.expect("account.add")
	if string(acc.Config) != `{"number":"***"}` {
		t.Fatalf("schema from hello not applied: %s", acc.Config)
	}
	step, err := c.LoginStart(ctx, "sig1", "qr")
	if err != nil || step.Display == nil || step.Display.Data != "QR-DATA" {
		t.Fatalf("login via rpc: %+v %v", step, err)
	}
	// A pushed login.step and a status report land in the core.
	if r := cl.call("login.step", map[string]any{"account_id": "sig1", "step": model.LoginStep{Flow: "qr", Step: model.StepDone,
		Self: &model.Contact{ID: "+10000", Handle: "+10000", Phone: "+10000"}}}); r.Error != nil {
		t.Fatalf("login.step: %v", r.Error)
	}
	if r := cl.call("status", map[string]any{"account_id": "sig1", "status": "connected",
		"self": model.Contact{ID: "+10000", Handle: "+10000"}, "device": map[string]any{"v": 2}}); r.Error != nil {
		t.Fatalf("status: %v", r.Error)
	}
	acc, _ = c.GetAccount(ctx, "sig1")
	if acc.Status != model.StatusConnected || acc.Self == nil || acc.Self.ID != "+10000" || acc.Login == nil {
		t.Fatalf("account after status: %+v", acc)
	}
	eventually(t, func() bool { cs, _, _ := c.ListContacts(ctx, "sig1", "", "", 10); return len(cs) == 2 })

	// Inbound events with an image: the core asks media.fetch, the adapter PUTs the bytes.
	ev := map[string]any{"account_id": "sig1", "events": []map[string]any{{
		"kind": "message",
		"message": map[string]any{"id": "m1", "chat_id": "+1", "sender": map[string]string{"id": "+1"}, "timestamp": time.Now().UTC(),
			"content": map[string]any{"type": "image", "text": "pic", "attachments": []map[string]any{{"media_id": "m1", "mime": "image/png", "size": 9, "state": "remote", "remote_ref": map[string]string{"k": "v"}}}},
			"raw":     map[string]any{"orig": true}},
		"chat":   map[string]any{"id": "+1", "kind": "direct", "name": "Alice"},
		"sender": map[string]any{"id": "+1", "names": map[string]string{"profile": "Alice"}},
	}, {"kind": "typing", "chat_id": "+1", "user_id": "+1", "state": "typing"},
		{"kind": "weird_new_kind", "chat_id": "+1"}}}
	if r := cl.call("events", ev); r.Error != nil {
		t.Fatalf("events: %v", r.Error)
	}
	cl.expect("media.fetch")
	eventually(t, func() bool {
		m, err := c.GetMessage(ctx, "sig1", "m1", true)
		return err == nil && len(m.Content.Attachments) == 1 && m.Content.Attachments[0].State == model.MediaReady && string(m.Raw) == `{"orig":true}`
	})
	evs, _, _ := c.ListEvents(ctx, "", store.EventFilter{Types: []string{model.EvPlatform}}, 10)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), "weird_new_kind") {
		t.Fatalf("unknown kind not surfaced: %+v", evs)
	}

	// Outbound: upload bytes, send image through the remote adapter, which GETs them back.
	up, err := c.Upload(ctx, "sig1", strings.NewReader("UPLOAD-BYTES"), adapter.MediaMeta{Mime: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	sent, _, err := c.Send(ctx, "sig1", "+1", model.SendRequest{Content: model.Content{Type: model.ContentImage, Text: "hi", Attachments: []model.Attachment{{MediaID: up.MediaID}}}})
	if err != nil || sent.ID != "s1" || !sent.FromMe {
		t.Fatalf("send: %+v %v", sent, err)
	}
	// Error codes map back to adapter errors.
	if _, err := c.ResolveChat(ctx, "sig1", "+2"); core.AsError(err).Status != http.StatusNotFound {
		t.Fatalf("resolve error mapping: %v", err)
	}
	if _, _, err := c.Send(ctx, "sig1", "+1", model.SendRequest{ThreadID: "t", Content: model.Content{Type: "text", Text: "x"}}); core.AsError(err).Code != "unsupported" {
		t.Fatalf("capability gate: %v", err)
	}

	// A second adapter for the same platform is refused.
	dup := dial(t, srv)
	dup.send(rpcMessage{ID: json.RawMessage("1"), Method: "hello", Params: raw})
	_, data, _ = dup.conn.Read(ctx)
	_ = json.Unmarshal(data, &reply)
	if reply.Error == nil {
		t.Fatalf("duplicate accepted: %s", data)
	}

	// Disconnect: accounts go disconnected / adapter_offline and sends fail fast.
	_ = cl.conn.Close(websocket.StatusNormalClosure, "bye")
	eventually(t, func() bool {
		a, _ := c.GetAccount(ctx, "sig1")
		return a.Status == model.StatusDisconnected && a.Error != nil && a.Error.Code == "adapter_offline"
	})
	if _, _, err := c.Send(ctx, "sig1", "+1", model.SendRequest{Content: model.Content{Type: "text", Text: "x"}}); core.AsError(err).Code != "account_not_ready" {
		t.Fatalf("send after disconnect: %v", err)
	}
}

func TestHubRejectsBadHello(t *testing.T) {
	_, srv := newEnv(t)
	ctx := context.Background()
	cl := dial(t, srv)
	cl.send(rpcMessage{ID: json.RawMessage("1"), Method: "hello", Params: json.RawMessage(`{"protocol":2,"platform":"x"}`)})
	_, data, err := cl.conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var reply rpcMessage
	_ = json.Unmarshal(data, &reply)
	if reply.Error == nil || !strings.Contains(reply.Error.Message, "protocol") {
		t.Fatalf("bad protocol accepted: %s", data)
	}
	// Wrong token never reaches the upgrade.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/media", nil)
	req.Header.Set("Authorization", "Bearer nope")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("auth: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRemoteAdapterPhaseAMethods(t *testing.T) {
	c, srv := newEnv(t)
	ctx := context.Background()
	cl := dial(t, srv)
	hello := map[string]any{"protocol": 1, "platform": "signal", "adapter": map[string]string{"name": "sig-py", "version": "0.1"},
		"capabilities": []string{"send.text", "chat.create", "message.history", "self.update"},
		"login_flows":  []map[string]string{{"id": "qr", "name": "QR"}}}
	raw, _ := json.Marshal(hello)
	cl.send(rpcMessage{ID: json.RawMessage("1"), Method: "hello", Params: raw})
	if _, _, err := cl.conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	go cl.loop()
	eventually(t, func() bool {
		for _, p := range c.Platforms() {
			if p.ID == "signal" {
				return true
			}
		}
		return false
	})
	var got []string
	record := func(name string, result any) {
		cl.handlers[name] = func(p json.RawMessage) (any, *rpcError) {
			got = append(got, name+" "+string(p))
			return result, nil
		}
	}
	cl.handlers["account.add"] = func(json.RawMessage) (any, *rpcError) { return map[string]any{}, nil }
	record("contact.list", map[string]any{"contacts": []model.Contact{{ID: "+2", Handle: "+2", Names: model.Names{Profile: "Bob"}}}})
	record("chat.create", model.Chat{ID: "g1", Kind: model.ChatGroup, Name: "Team"})
	record("chat.update", model.Chat{ID: "g1", Kind: model.ChatGroup, Name: "Team 2"})
	record("chat.backfill", map[string]any{"messages": []model.Message{{ID: "h1", ChatID: "g1", Sender: model.Sender{ID: "+2"},
		Timestamp: time.Now().UTC().Add(-time.Hour), Content: model.Content{Type: model.ContentText, Text: "old"}}}, "more": false})
	record("self.update", model.Contact{ID: "+10000", Names: model.Names{Profile: "Renamed"}})
	record("contact.block", map[string]any{})

	if _, err := c.CreateAccount(ctx, "sig1", "signal", "", nil); err != nil {
		t.Fatal(err)
	}
	cl.expect("account.add")
	cl.send(rpcMessage{ID: json.RawMessage("2"), Method: "status", Params: mustJSON(map[string]any{"account_id": "sig1", "status": "connected",
		"self": model.Contact{ID: "+10000", Handle: "+10000", Names: model.Names{Profile: "Me"}}})})
	eventually(t, func() bool { cs, _, _ := c.ListContacts(ctx, "sig1", "", "", 10); return len(cs) == 2 })

	ch, err := c.CreateChat(ctx, "sig1", adapter.CreateChatRequest{Name: "Team", Members: []string{"+2"}})
	if err != nil || ch.ID != "g1" || ch.Name != "Team" {
		t.Fatalf("create: %+v %v", ch, err)
	}
	name := "Team 2"
	if ch, err = c.PatchChat(ctx, "sig1", "g1", core.ChatPatch{Name: &name}); err != nil || ch.Name != "Team 2" {
		t.Fatalf("rename: %+v %v", ch, err)
	}
	msgs, _, err := c.ListMessages(ctx, "sig1", "g1", core.MessageQuery{Limit: 5, Backfill: true})
	if err != nil || len(msgs) != 1 || msgs[0].ID != "h1" {
		t.Fatalf("backfill: %v %v", msgs, err)
	}
	up, err := c.Upload(ctx, "sig1", strings.NewReader("PNG"), adapter.MediaMeta{Mime: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	newName := "Renamed"
	acc, err := c.UpdateSelf(ctx, "sig1", core.SelfPatch{Name: &newName, AvatarMediaID: up.MediaID})
	if err != nil || acc.Self == nil || acc.Self.Name != "Renamed" {
		t.Fatalf("self: %+v %v", acc, err)
	}
	if _, err := c.ResolveChat(ctx, "sig1", "+2"); core.AsError(err).Code != "unsupported" {
		t.Fatalf("resolve without capability: %v", err)
	}
	yes := true
	if _, err := c.PatchContact(ctx, "sig1", "+2", core.ContactPatch{Blocked: &yes}); err != nil {
		t.Fatalf("block: %v", err)
	}
	want := []string{`contact.list`, `chat.create {"account_id":"sig1","kind":"group","members":["+2"],"name":"Team"}`,
		`chat.update {"account_id":"sig1","chat_id":"g1","name":"Team 2"}`,
		`chat.backfill {"account_id":"sig1","chat_id":"g1","limit":5}`,
		`self.update`, `contact.block {"account_id":"sig1","blocked":true,"user_id":"+2"}`}
	if len(got) != len(want) {
		t.Fatalf("calls: %v", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("call %d: %s\nwant prefix %s", i, got[i], want[i])
		}
	}
	if !strings.Contains(got[4], `"avatar_url":"`) || !strings.Contains(got[4], `"name":"Renamed"`) {
		t.Fatalf("self.update params: %s", got[4])
	}
}

func TestRemoteAdapterRequests(t *testing.T) {
	c, srv := newEnv(t)
	ctx := context.Background()
	cl := dial(t, srv)
	hello := map[string]any{"protocol": 1, "platform": "signal", "adapter": map[string]string{"name": "sig-py", "version": "0.1"},
		"capabilities": []string{"send.text"}, "login_flows": []map[string]string{{"id": "qr", "name": "QR"}}}
	raw, _ := json.Marshal(hello)
	cl.send(rpcMessage{ID: json.RawMessage("1"), Method: "hello", Params: raw})
	if _, _, err := cl.conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	go cl.loop()
	eventually(t, func() bool {
		for _, p := range c.Platforms() {
			if p.ID == "signal" {
				return true
			}
		}
		return false
	})
	var answered []string
	cl.handlers["account.add"] = func(json.RawMessage) (any, *rpcError) { return map[string]any{}, nil }
	cl.handlers["contact.list"] = func(json.RawMessage) (any, *rpcError) { return map[string]any{"contacts": []model.Contact{}}, nil }
	cl.handlers["request.answer"] = func(p json.RawMessage) (any, *rpcError) {
		answered = append(answered, string(p))
		return map[string]any{}, nil
	}
	if _, err := c.CreateAccount(ctx, "sig1", "signal", "", nil); err != nil {
		t.Fatal(err)
	}
	cl.expect("account.add")
	cl.send(rpcMessage{ID: json.RawMessage("2"), Method: "status", Params: mustJSON(map[string]any{"account_id": "sig1", "status": "connected",
		"self": model.Contact{ID: "+10000", Handle: "+10000"}})})
	eventually(t, func() bool { a, _ := c.GetAccount(ctx, "sig1"); return a.Status == model.StatusConnected })

	// A request event over the wire becomes a pending request; answering reaches the adapter.
	cl.send(rpcMessage{ID: json.RawMessage("3"), Method: "events", Params: mustJSON(map[string]any{"account_id": "sig1", "events": []map[string]any{{
		"kind": "request", "request": map[string]any{"key": "call:9", "kind": "call", "from": map[string]any{"id": "+2", "name": "Bob"},
			"chat": map[string]any{"id": "+2", "kind": "direct"}, "call_kind": "video", "platform_ref": map[string]any{"call": 9}}}}})})
	var reqID string
	eventually(t, func() bool {
		list, _, _ := c.ListRequests(ctx, "sig1", store.RequestFilter{}, "", 10)
		if len(list) == 1 && list[0].Call != nil && list[0].Call.Kind == "video" && list[0].From.Name == "Bob" {
			reqID = list[0].ID
			return true
		}
		return false
	})
	if _, err := c.AnswerRequest(ctx, "sig1", reqID, model.ActionReject, "busy"); err != nil {
		t.Fatal(err)
	}
	if len(answered) != 1 || !strings.Contains(answered[0], `"request_key":"call:9"`) || !strings.Contains(answered[0], `"platform_ref":{"call":9}`) ||
		!strings.Contains(answered[0], `"action":"reject"`) {
		t.Fatalf("request.answer params: %v", answered)
	}
	// A malformed request event is rejected as invalid params.
	resp := cl.call("events", map[string]any{"account_id": "sig1", "events": []map[string]any{{"kind": "request", "request": map[string]any{"kind": "call"}}}})
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("malformed request event: %+v", resp)
	}
}
