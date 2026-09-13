package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

const (
	callTimeout  = 60 * time.Second
	sendTimeout  = 90 * time.Second
	fetchTimeout = 5 * time.Minute
	maxFrame     = 1 << 20
)

// Adapter is the in-process face of one connected out-of-process adapter. Every method is a
// JSON-RPC call over the WebSocket; inbound requests are dispatched by readLoop.
type Adapter struct {
	hub  *Hub
	conn *websocket.Conn
	info adapter.Info
	log  *slog.Logger
	sink adapter.Sink

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage
}

func newAdapter(h *Hub, conn *websocket.Conn, hello helloParams, log *slog.Logger) *Adapter {
	ctx, cancel := context.WithCancel(context.Background())
	return &Adapter{
		hub: h, conn: conn, log: log, ctx: ctx, cancel: cancel, done: make(chan struct{}), pending: map[int64]chan rpcMessage{},
		info: adapter.Info{Platform: hello.Platform, Instance: hello.Instance, Name: hello.Adapter.Name, Version: hello.Adapter.Version,
			Capabilities: hello.Capabilities, LoginFlows: hello.LoginFlows, ConfigSchema: hello.ConfigSchema},
	}
}

// Info returns what the adapter declared in hello.
func (a *Adapter) Info() adapter.Info { return a.info }

// Remote marks this adapter as out-of-process for GET /platforms.
func (a *Adapter) Remote() bool { return true }

// Start records the sink; the connection is already up.
func (a *Adapter) Start(_ context.Context, sink adapter.Sink) error { a.sink = sink; return nil }

// Stop closes the connection.
func (a *Adapter) Stop(context.Context) error {
	_ = a.conn.Close(websocket.StatusGoingAway, "core shutting down")
	a.cancel()
	return nil
}

// --- transport ---

func (a *Adapter) write(msg rpcMessage) error {
	msg.JSONRPC = "2.0"
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(b) > maxFrame {
		return fmt.Errorf("frame too large (%d bytes)", len(b))
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	return a.conn.Write(ctx, websocket.MessageText, b)
}

// call sends a request and decodes the result into out (may be nil).
func (a *Adapter) call(ctx context.Context, method string, params any, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.nextID++
	id := a.nextID
	ch := make(chan rpcMessage, 1)
	a.pending[id] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
	}()
	if err := a.write(rpcMessage{ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: raw}); err != nil {
		return adapter.Errorf(adapter.ErrNotConnected, "adapter offline: %v", err)
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return toAdapterErr(resp.Error)
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return adapter.Errorf(adapter.ErrPlatform, "%s: bad result: %v", method, err)
			}
		}
		return nil
	case <-a.done:
		return adapter.Errorf(adapter.ErrNotConnected, "adapter disconnected")
	case <-ctx.Done():
		return adapter.Errorf(adapter.ErrPlatform, "%s: %v", method, ctx.Err())
	}
}

// readLoop dispatches frames until the connection dies; it closes done on exit.
func (a *Adapter) readLoop() {
	defer func() {
		a.cancel()
		a.mu.Lock()
		for id, ch := range a.pending {
			close(ch)
			delete(a.pending, id)
		}
		a.mu.Unlock()
		close(a.done)
	}()
	for {
		_, data, err := a.conn.Read(a.ctx)
		if err != nil {
			if a.ctx.Err() == nil {
				a.log.Warn("adapter connection closed", "err", err)
			}
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			a.log.Warn("bad frame", "err", err)
			continue
		}
		if msg.Method == "" {
			a.deliver(msg)
			continue
		}
		a.handleRequest(msg)
	}
}

func (a *Adapter) deliver(msg rpcMessage) {
	id, err := strconv.ParseInt(string(msg.ID), 10, 64)
	if err != nil {
		return
	}
	a.mu.Lock()
	ch := a.pending[id]
	a.mu.Unlock()
	if ch != nil {
		ch <- msg
	}
}

// handleRequest serves adapter → core methods (§5) and answers on the same connection.
func (a *Adapter) handleRequest(msg rpcMessage) {
	ctx, cancel := context.WithTimeout(a.ctx, callTimeout)
	defer cancel()
	result, err := a.serve(ctx, msg.Method, msg.Params)
	if len(msg.ID) == 0 {
		return // notification: nothing to answer
	}
	reply := rpcMessage{ID: msg.ID}
	if err != nil {
		reply.Error = &rpcError{Code: codePlatform, Message: err.Error()}
		var re *rpcError
		if errors.As(err, &re) {
			reply.Error = re
		}
	} else {
		reply.Result, _ = json.Marshal(result)
		if reply.Result == nil {
			reply.Result = json.RawMessage("{}")
		}
	}
	if err := a.write(reply); err != nil {
		a.log.Warn("write reply", "method", msg.Method, "err", err)
	}
}

func (a *Adapter) serve(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "status":
		var p statusParams
		if err := json.Unmarshal(params, &p); err != nil || p.AccountID == "" || p.Status == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "status needs account_id and status"}
		}
		return map[string]any{}, a.sink.Status(ctx, p.AccountID, adapter.Status{Status: p.Status, Self: p.Self, Device: p.Device, Error: p.Error})
	case "login.step":
		var p struct {
			AccountID string          `json:"account_id"`
			Step      model.LoginStep `json:"step"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.AccountID == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "login.step needs account_id and step"}
		}
		return map[string]any{}, a.sink.LoginStep(ctx, p.AccountID, p.Step)
	case "events":
		var p struct {
			AccountID string      `json:"account_id"`
			Events    []wireEvent `json:"events"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.AccountID == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "events needs account_id and events"}
		}
		evs := make([]adapter.Event, 0, len(p.Events))
		for _, w := range p.Events {
			ev, err := w.decode()
			if err != nil {
				return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
			}
			evs = append(evs, ev)
		}
		return map[string]any{"count": len(evs)}, a.sink.Events(ctx, p.AccountID, evs)
	case "media.ready":
		return map[string]any{}, nil // the PUT already marked it ready
	case "media.failed":
		var p struct {
			MediaID string `json:"media_id"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.MediaID == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "media.failed needs media_id"}
		}
		a.log.Warn("remote media download failed", "media", p.MediaID, "err", p.Error)
		return map[string]any{}, a.hub.core.MarkMediaFailed(ctx, p.MediaID)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown method " + method}
}

// --- adapter.Adapter over the wire (§4) ---

type acct struct {
	AccountID string `json:"account_id"`
}

func (a *Adapter) AddAccount(ctx context.Context, id string, cfg json.RawMessage, dataDir string) error {
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	return a.call(ctx, "account.add", map[string]any{"account_id": id, "config": cfg, "data_dir": dataDir}, nil)
}

func (a *Adapter) RemoveAccount(ctx context.Context, id string) error {
	return a.call(ctx, "account.remove", acct{id}, nil)
}

func (a *Adapter) Reconnect(ctx context.Context, id string) error {
	return a.call(ctx, "account.reconnect", acct{id}, nil)
}

func (a *Adapter) LoginStart(ctx context.Context, id, flow string) (model.LoginStep, error) {
	var step model.LoginStep
	err := a.call(ctx, "login.start", map[string]any{"account_id": id, "flow": flow}, &step)
	return step, err
}

func (a *Adapter) LoginSubmit(ctx context.Context, id string, fields map[string]string) (model.LoginStep, error) {
	var step model.LoginStep
	err := a.call(ctx, "login.submit", map[string]any{"account_id": id, "fields": fields}, &step)
	return step, err
}

func (a *Adapter) LoginRefresh(ctx context.Context, id string) (model.LoginStep, error) {
	var step model.LoginStep
	err := a.call(ctx, "login.refresh", acct{id}, &step)
	return step, err
}

func (a *Adapter) LoginCancel(ctx context.Context, id string) error {
	return a.call(ctx, "login.cancel", acct{id}, nil)
}

func (a *Adapter) Logout(ctx context.Context, id string) error {
	return a.call(ctx, "logout", acct{id}, nil)
}

func (a *Adapter) GetChat(ctx context.Context, id, chatID string) (model.Chat, error) {
	var ch model.Chat
	err := a.call(ctx, "chat.get", map[string]any{"account_id": id, "chat_id": chatID}, &ch)
	return ch, err
}

func (a *Adapter) ListContacts(ctx context.Context, id string) ([]model.Contact, error) {
	var out struct {
		Contacts []model.Contact `json:"contacts"`
	}
	err := a.call(ctx, "contact.list", acct{id}, &out)
	return out.Contacts, err
}

func (a *Adapter) SendMessage(ctx context.Context, id string, req adapter.SendRequest) (model.Message, error) {
	atts := make([]wireAttachment, 0, len(req.Content.Attachments))
	for _, att := range req.Content.Attachments {
		atts = append(atts, wireAttachment{MediaID: att.MediaID, Mime: att.Mime, Size: att.Size, FileName: att.FileName, SHA256: att.SHA256,
			URL: a.hub.mediaGetURL(att.MediaID)})
	}
	params := map[string]any{
		"account_id": id, "chat_id": req.ChatID, "client_id": req.ClientID, "content": req.Content, "attachments": atts,
		"reply_to": req.ReplyTo, "reply_target": req.ReplyTarget, "thread_id": req.ThreadID, "mentions": req.Mentions,
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	var m model.Message
	err := a.call(ctx, "message.send", params, &m)
	return m, err
}

// FetchMedia asks the adapter to PUT the bytes itself; the writer is left untouched.
func (a *Adapter) FetchMedia(ctx context.Context, id, mediaID string, ref json.RawMessage, _ io.Writer) (adapter.MediaMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if err := a.call(ctx, "media.fetch", map[string]any{"account_id": id, "media_id": mediaID, "remote_ref": ref}, nil); err != nil {
		return adapter.MediaMeta{}, err
	}
	return adapter.MediaMeta{}, adapter.ErrMediaStoredBySink
}

func (a *Adapter) ResolveChat(ctx context.Context, id, handle string) (model.ResolvedChat, error) {
	var r model.ResolvedChat
	err := a.call(ctx, "chat.resolve", map[string]any{"account_id": id, "handle": handle}, &r)
	return r, err
}

func (a *Adapter) EditMessage(ctx context.Context, id, chatID, msgID string, c model.Content) (model.Message, error) {
	var m model.Message
	err := a.call(ctx, "message.edit", map[string]any{"account_id": id, "chat_id": chatID, "message_id": msgID, "content": c}, &m)
	return m, err
}

func (a *Adapter) DeleteMessage(ctx context.Context, id, chatID, msgID, senderID string) error {
	return a.call(ctx, "message.delete", map[string]any{"account_id": id, "chat_id": chatID, "message_id": msgID, "sender_id": senderID}, nil)
}

func (a *Adapter) React(ctx context.Context, id, chatID, msgID, senderID, emoji string, remove bool) error {
	return a.call(ctx, "message.react", map[string]any{"account_id": id, "chat_id": chatID, "message_id": msgID, "sender_id": senderID, "emoji": emoji, "remove": remove}, nil)
}

func (a *Adapter) MarkRead(ctx context.Context, id, chatID string, ids []string, senderID string) error {
	return a.call(ctx, "chat.mark_read", map[string]any{"account_id": id, "chat_id": chatID, "message_ids": ids, "sender_id": senderID}, nil)
}

func (a *Adapter) KeysStatus(ctx context.Context, id string) (model.KeyStatus, error) {
	var st model.KeyStatus
	err := a.call(ctx, "keys.status", acct{id}, &st)
	return st, err
}

func (a *Adapter) KeysVerify(ctx context.Context, id, recoveryKey string) (model.KeyVerifyResult, error) {
	var res model.KeyVerifyResult
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	err := a.call(ctx, "keys.verify", map[string]any{"account_id": id, "recovery_key": recoveryKey}, &res)
	return res, err
}

func (a *Adapter) KeysExport(ctx context.Context, id, passphrase string) ([]byte, error) {
	var out struct {
		Data []byte `json:"data"` // base64 on the wire
	}
	err := a.call(ctx, "keys.export", map[string]any{"account_id": id, "passphrase": passphrase}, &out)
	return out.Data, err
}

func (a *Adapter) KeysImport(ctx context.Context, id, passphrase string, data []byte) (int, error) {
	var out struct {
		SessionsImported int `json:"sessions_imported"`
	}
	err := a.call(ctx, "keys.import", map[string]any{"account_id": id, "passphrase": passphrase, "data": data}, &out)
	return out.SessionsImported, err
}

func (a *Adapter) Typing(ctx context.Context, id, chatID, state string) error {
	return a.call(ctx, "chat.typing", map[string]any{"account_id": id, "chat_id": chatID, "state": state}, nil)
}

// --- Phase A optional interfaces (docs/adapter-protocol.md §4) ---

func (a *Adapter) CreateChat(ctx context.Context, id string, req adapter.CreateChatRequest) (model.Chat, error) {
	var ch model.Chat
	err := a.call(ctx, "chat.create", map[string]any{"account_id": id, "kind": req.Kind, "name": req.Name, "members": req.Members}, &ch)
	return ch, err
}

func (a *Adapter) UpdateChat(ctx context.Context, id, chatID string, p adapter.ChatUpdate) (model.Chat, error) {
	var ch model.Chat
	err := a.call(ctx, "chat.update", map[string]any{"account_id": id, "chat_id": chatID, "name": p.Name}, &ch)
	return ch, err
}

func (a *Adapter) Backfill(ctx context.Context, id, chatID string, before adapter.BackfillCursor, limit int) ([]model.Message, bool, error) {
	params := map[string]any{"account_id": id, "chat_id": chatID, "limit": limit}
	if before.MessageID != "" || !before.Timestamp.IsZero() {
		params["before"] = map[string]any{"ts": before.Timestamp, "message_id": before.MessageID}
	}
	var out struct {
		Messages []model.Message `json:"messages"`
		More     bool            `json:"more"`
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	err := a.call(ctx, "chat.backfill", params, &out)
	return out.Messages, out.More, err
}

func (a *Adapter) UpdateSelf(ctx context.Context, id string, p adapter.SelfUpdate) (model.Contact, error) {
	params := map[string]any{"account_id": id, "name": p.Name, "bio": p.Bio}
	if p.AvatarMediaID != "" {
		params["avatar_media_id"] = p.AvatarMediaID
		params["avatar_url"] = a.hub.mediaGetURL(p.AvatarMediaID)
	}
	var c model.Contact
	err := a.call(ctx, "self.update", params, &c)
	return c, err
}

func (a *Adapter) Block(ctx context.Context, id, userID string, blocked bool) error {
	return a.call(ctx, "contact.block", map[string]any{"account_id": id, "user_id": userID, "blocked": blocked}, nil)
}
