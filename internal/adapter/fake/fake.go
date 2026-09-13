// Package fake is an in-memory adapter for core and server tests.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

// Platform is the fake's platform id.
const Platform = "fake"

// Adapter records every call and lets tests push events through the sink.
type Adapter struct {
	mu       sync.Mutex
	sink     adapter.Sink
	accounts map[string]json.RawMessage
	steps    map[string]model.LoginStep
	nextID   int

	Instance string
	Sent     []adapter.SendRequest
	Reacted  []string
	Deleted  []string
	Read     []string
	FailSend error
}

// New returns an adapter with no accounts.
func New() *Adapter {
	return &Adapter{accounts: map[string]json.RawMessage{}, steps: map[string]model.LoginStep{}}
}

// Info declares a broad capability set.
func (a *Adapter) Info() adapter.Info {
	return adapter.Info{
		Platform: Platform, Instance: a.Instance, Name: "Fake", Version: "test",
		Capabilities: []string{adapter.CapSendText, adapter.CapSendMedia, adapter.CapReply, adapter.CapEdit, adapter.CapDelete,
			adapter.CapReaction, adapter.CapChatRead, adapter.CapChatTyping, adapter.CapChatResolve, adapter.CapChatMembers, adapter.CapReceipts},
		LoginFlows:   []model.LoginFlow{{ID: "qr", Name: "QR"}, {ID: "phone", Name: "Phone"}},
		ConfigSchema: json.RawMessage(`{"type":"object","properties":{"secret":{"type":"string","x-secret":true},"name":{"type":"string"}}}`),
	}
}

// Sink exposes the core's sink so tests can push events.
func (a *Adapter) Sink() adapter.Sink { return a.sink }

// Self is the identity every fake account logs in as.
func Self(accountID string) *model.Contact {
	return &model.Contact{ID: "self@" + Platform, Handle: "+10000", Phone: "+10000", Names: model.Names{Profile: "Me " + accountID}, IsSelf: true, IsContact: true}
}

// Connect reports the account connected through the sink.
func (a *Adapter) Connect(ctx context.Context, accountID string) error {
	return a.sink.Status(ctx, accountID, adapter.Status{Status: model.StatusConnected, Self: Self(accountID), Device: json.RawMessage(`{"v":1}`)})
}

// Push delivers events as if they came from the platform.
func (a *Adapter) Push(ctx context.Context, accountID string, evs ...adapter.Event) error {
	return a.sink.Events(ctx, accountID, evs)
}

func (a *Adapter) Start(_ context.Context, sink adapter.Sink) error { a.sink = sink; return nil }
func (a *Adapter) Stop(context.Context) error                       { return nil }

func (a *Adapter) AddAccount(_ context.Context, id string, cfg json.RawMessage, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accounts[id] = cfg
	return nil
}

func (a *Adapter) RemoveAccount(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.accounts, id)
	return nil
}

func (a *Adapter) Reconnect(context.Context, string) error { return nil }

func (a *Adapter) LoginStart(_ context.Context, id, flow string) (model.LoginStep, error) {
	var step model.LoginStep
	switch flow {
	case "qr":
		step = model.LoginStep{Flow: flow, Step: model.StepDisplay, Display: &model.LoginDisplay{Type: "qr", Data: "QR1"}}
	default:
		step = model.LoginStep{Flow: flow, Step: model.StepInput, Input: &model.LoginInput{Fields: []model.LoginField{{Name: "phone", Type: "phone"}}}}
	}
	a.mu.Lock()
	a.steps[id] = step
	a.mu.Unlock()
	return step, nil
}

func (a *Adapter) LoginSubmit(_ context.Context, id string, fields map[string]string) (model.LoginStep, error) {
	if fields["phone"] == "bad" {
		return model.LoginStep{Flow: "phone", Step: model.StepFailed, Error: &model.Error{Code: "platform_error", Message: "rejected"}}, nil
	}
	step := model.LoginStep{Flow: "phone", Step: model.StepDone, Self: Self(id)}
	a.mu.Lock()
	a.steps[id] = step
	a.mu.Unlock()
	return step, nil
}

func (a *Adapter) LoginRefresh(_ context.Context, id string) (model.LoginStep, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.steps[id], nil
}

func (a *Adapter) LoginCancel(context.Context, string) error { return nil }
func (a *Adapter) Logout(context.Context, string) error      { return nil }

func (a *Adapter) GetChat(_ context.Context, _, chatID string) (model.Chat, error) {
	if strings.HasPrefix(chatID, "g") {
		return model.Chat{ID: chatID, Kind: model.ChatGroup, Name: "Group " + chatID,
			Participants: []model.Participant{{ID: "u1@fake", Role: "admin"}, {ID: "u2@fake", Role: "member"}}}, nil
	}
	if strings.HasPrefix(chatID, "missing") {
		return model.Chat{}, adapter.Errorf(adapter.ErrInvalidTarget, "no such chat")
	}
	return model.Chat{ID: chatID, Kind: model.ChatDirect, Name: "Direct " + chatID}, nil
}

func (a *Adapter) ListContacts(context.Context, string) ([]model.Contact, error) {
	return []model.Contact{{ID: "u1@fake", Handle: "+1", Phone: "+1", Names: model.Names{Alias: "Alice W", Profile: "alice"}, IsContact: true}}, nil
}

func (a *Adapter) SendMessage(ctx context.Context, _ string, req adapter.SendRequest) (model.Message, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.FailSend != nil {
		return model.Message{}, a.FailSend
	}
	for _, att := range req.Content.Attachments {
		rc, _, err := req.Media.Open(ctx, att.MediaID)
		if err != nil {
			return model.Message{}, err
		}
		_, _ = io.ReadAll(rc)
		_ = rc.Close()
	}
	a.Sent = append(a.Sent, req)
	a.nextID++
	return model.Message{ID: fmt.Sprintf("sent-%d", a.nextID), Timestamp: time.Now().UTC()}, nil
}

func (a *Adapter) FetchMedia(_ context.Context, _, _ string, ref json.RawMessage, w io.Writer) (adapter.MediaMeta, error) {
	if strings.Contains(string(ref), "fail") {
		return adapter.MediaMeta{}, adapter.Errorf(adapter.ErrPlatform, "download failed")
	}
	_, _ = w.Write([]byte("PNGBYTES"))
	return adapter.MediaMeta{Mime: "image/png"}, nil
}

func (a *Adapter) ResolveChat(_ context.Context, _, handle string) (model.ResolvedChat, error) {
	id := strings.TrimPrefix(handle, "+") + "@fake"
	return model.ResolvedChat{ChatID: id, Kind: model.ChatDirect, UserID: id}, nil
}

func (a *Adapter) EditMessage(_ context.Context, _, chatID, msgID string, c model.Content) (model.Message, error) {
	return model.Message{ID: msgID, ChatID: chatID, Content: c}, nil
}

func (a *Adapter) DeleteMessage(_ context.Context, _, _, msgID, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Deleted = append(a.Deleted, msgID)
	return nil
}

func (a *Adapter) React(_ context.Context, _, _, msgID, _, emoji string, remove bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Reacted = append(a.Reacted, fmt.Sprintf("%s:%s:%t", msgID, emoji, remove))
	return nil
}

func (a *Adapter) MarkRead(_ context.Context, _, _ string, ids []string, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Read = append(a.Read, ids...)
	return nil
}

func (a *Adapter) Typing(context.Context, string, string, string) error { return nil }
