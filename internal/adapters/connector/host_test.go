package connector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

// --- a minimal bridgev2 network connector ---

type fakeNet struct {
	bridge *bridgev2.Bridge
	mu     sync.Mutex
	login  *bridgev2.UserLogin
	sent   []*event.MessageEventContent
}

func (f *fakeNet) Init(br *bridgev2.Bridge)           { f.bridge = br }
func (f *fakeNet) Start(context.Context) error        { return nil }
func (f *fakeNet) GetDBMetaTypes() database.MetaTypes { return database.MetaTypes{} }
func (f *fakeNet) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{DisplayName: "Fake Net", NetworkID: "fakenet", BeeperBridgeType: "fakenet"}
}
func (f *fakeNet) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}
func (f *fakeNet) GetConfig() (string, any, configupgrade.Upgrader) {
	return "", nil, configupgrade.NoopUpgrader
}
func (f *fakeNet) LoadUserLogin(_ context.Context, login *bridgev2.UserLogin) error {
	login.Client = &fakeAPI{net: f, login: login}
	f.mu.Lock()
	f.login = login
	f.mu.Unlock()
	return nil
}
func (f *fakeNet) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{Name: "Phone", Description: "Phone number", ID: "phone"}}
}
func (f *fakeNet) CreateLogin(_ context.Context, user *bridgev2.User, _ string) (bridgev2.LoginProcess, error) {
	return &fakeLogin{net: f, user: user}, nil
}
func (f *fakeNet) GetBridgeInfoVersion() (int, int) { return 1, 1 }

// push delivers an inbound text from "alice" through the bridge's remote event queue.
func (f *fakeNet) push(id, text string) {
	f.mu.Lock()
	login := f.login
	f.mu.Unlock()
	f.bridge.QueueRemoteEvent(login, &simplevent.Message[string]{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventMessage,
			PortalKey:    networkid.PortalKey{ID: "alice", Receiver: login.ID},
			Sender:       bridgev2.EventSender{Sender: "alice"},
			CreatePortal: true,
			Timestamp:    time.Now(),
		},
		ID:   networkid.MessageID(id),
		Data: text,
		ConvertMessageFunc: func(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, data string) (*bridgev2.ConvertedMessage, error) {
			return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{
				Type: event.EventMessage, Content: &event.MessageEventContent{MsgType: event.MsgText, Body: data},
			}}}, nil
		},
	})
}

type fakeLogin struct {
	net  *fakeNet
	user *bridgev2.User
}

func (l *fakeLogin) Start(context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeUserInput, StepID: "fake.phone", Instructions: "Enter your number",
		UserInputParams: &bridgev2.LoginUserInputParams{Fields: []bridgev2.LoginInputDataField{{Type: bridgev2.LoginInputFieldTypePhoneNumber, ID: "phone", Name: "Phone"}}}}, nil
}
func (l *fakeLogin) Cancel() {}
func (l *fakeLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	ul, err := l.user.NewLogin(ctx, &database.UserLogin{ID: "fake-1", RemoteName: input["phone"],
		RemoteProfile: status.RemoteProfile{Phone: input["phone"], Name: "Me"}}, nil)
	if err != nil {
		return nil, err
	}
	ul.Client.Connect(ctx)
	return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeComplete, StepID: "fake.complete",
		CompleteParams: &bridgev2.LoginCompleteParams{UserLoginID: ul.ID, UserLogin: ul}}, nil
}

type fakeAPI struct {
	net   *fakeNet
	login *bridgev2.UserLogin
}

func (a *fakeAPI) Connect(context.Context) {
	a.login.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
}
func (a *fakeAPI) Disconnect()                                            {}
func (a *fakeAPI) GetUserID() networkid.UserID                            { return "me" }
func (a *fakeAPI) IsLoggedIn() bool                                       { return true }
func (a *fakeAPI) LogoutRemote(context.Context)                           {}
func (a *fakeAPI) IsThisUser(_ context.Context, id networkid.UserID) bool { return id == "me" }
func (a *fakeAPI) GetChatInfo(context.Context, *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	name := "Alice"
	typ := database.RoomTypeDM
	return &bridgev2.ChatInfo{Name: &name, Type: &typ, Members: &bridgev2.ChatMemberList{IsFull: true, OtherUserID: "alice",
		MemberMap: bridgev2.ChatMemberMap{
			"alice": {EventSender: bridgev2.EventSender{Sender: "alice"}, Membership: event.MembershipJoin},
			"me":    {EventSender: bridgev2.EventSender{IsFromMe: true, Sender: "me"}, Membership: event.MembershipJoin},
		}}}, nil
}
func (a *fakeAPI) GetUserInfo(_ context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	name := "Alice"
	if ghost.ID == "me" {
		name = "Me"
	}
	return &bridgev2.UserInfo{Name: &name, Identifiers: []string{"tel:+1"}}, nil
}
func (a *fakeAPI) GetCapabilities(context.Context, *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{}
}
func (a *fakeAPI) HandleMatrixMessage(_ context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	a.net.mu.Lock()
	a.net.sent = append(a.net.sent, msg.Content)
	a.net.mu.Unlock()
	return &bridgev2.MatrixMessageResponse{DB: &database.Message{ID: "sent-1", MXID: msg.Event.ID, Room: msg.Portal.PortalKey, SenderID: "me", Timestamp: time.Now()}}, nil
}

// --- recording sink ---

type recSink struct {
	mu     sync.Mutex
	status []adapter.Status
	steps  []model.LoginStep
	events []adapter.Event
}

func (r *recSink) Status(_ context.Context, _ string, st adapter.Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = append(r.status, st)
	return nil
}
func (r *recSink) LoginStep(_ context.Context, _ string, step model.LoginStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, step)
	return nil
}
func (r *recSink) Events(_ context.Context, _ string, evs []adapter.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evs...)
	return nil
}
func (r *recSink) PutMedia(_ context.Context, _, mediaID string, meta adapter.MediaMeta, rd io.Reader) (model.Attachment, error) {
	b, _ := io.ReadAll(rd)
	return model.Attachment{MediaID: mediaID, Mime: meta.Mime, Size: int64(len(b)), SHA256: "sha", State: model.MediaReady}, nil
}

func (r *recSink) lastStatus() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.status) == 0 {
		return ""
	}
	return r.status[len(r.status)-1].Status
}

func (r *recSink) find(kind string, match func(adapter.Event) bool) *adapter.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.events {
		if r.events[i].Kind == kind && match(r.events[i]) {
			return &r.events[i]
		}
	}
	return nil
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

func TestHostedConnectorRoundTrip(t *testing.T) {
	ctx := context.Background()
	fake := &fakeNet{}
	host := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "fakenet", func() bridgev2.NetworkConnector { return fake })
	sink := &recSink{}
	if err := host.Start(ctx, sink); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := host.AddAccount(ctx, "acc1", nil, dir); err != nil {
		t.Fatal(err)
	}
	info := host.Info()
	if info.Platform != "fakenet" || len(info.LoginFlows) != 1 || info.LoginFlows[0].ID != "phone" || !info.Has(adapter.CapSendText) || info.Has(adapter.CapEdit) {
		t.Fatalf("info: %+v", info)
	}
	eventually(t, func() bool { return sink.lastStatus() == model.StatusUnpaired })

	// Login: bridgev2 user_input → our input; complete → done with the remote profile.
	step, err := host.LoginStart(ctx, "acc1", "phone")
	if err != nil || step.Step != model.StepInput || len(step.Input.Fields) != 1 || step.Input.Fields[0].Name != "phone" || step.Input.Fields[0].Type != "phone" {
		t.Fatalf("login start: %+v %v", step, err)
	}
	step, err = host.LoginSubmit(ctx, "acc1", map[string]string{"phone": "+1"})
	if err != nil || step.Step != model.StepDone || step.Self == nil || step.Self.Phone != "+1" || step.Self.Name != "Me" || step.Self.ID != "me" {
		t.Fatalf("login submit: %+v %v", step, err)
	}
	eventually(t, func() bool { return sink.lastStatus() == model.StatusConnected })

	// Inbound: a remote message becomes a chat hint (DM, named after the peer) plus a message.
	fake.push("m1", "hello")
	eventually(t, func() bool {
		return sink.find(adapter.EvMessage, func(e adapter.Event) bool {
			return e.Message != nil && e.Message.ID == "m1" && e.Message.ChatID == "alice" && e.Message.Sender.ID == "alice" &&
				e.Message.Content.Text == "hello" && !e.Message.FromMe
		}) != nil
	})
	eventually(t, func() bool {
		return sink.find(adapter.EvChat, func(e adapter.Event) bool {
			return e.Chat != nil && e.Chat.ID == "alice" && e.Chat.Kind == model.ChatDirect && e.Chat.Name == "Alice"
		}) != nil
	})
	if sink.find(adapter.EvContact, func(e adapter.Event) bool {
		return e.Contact != nil && e.Contact.ID == "alice" && e.Contact.Name == "Alice"
	}) == nil {
		t.Fatal("no contact for alice")
	}
	// Duplicate delivery is idempotent on the bridgev2 side: the same id is not re-emitted.
	before := len(sink.events)
	fake.push("m1", "hello again")
	time.Sleep(200 * time.Millisecond)
	if sink.find(adapter.EvMessage, func(e adapter.Event) bool { return e.Message != nil && e.Message.Content.Text == "hello again" }) != nil {
		t.Fatalf("duplicate message re-emitted (%d → %d events)", before, len(sink.events))
	}

	// Outbound: goes through the portal to HandleMatrixMessage and returns the network id.
	sent, err := host.SendMessage(ctx, "acc1", adapter.SendRequest{ChatID: "alice", Content: model.Content{Type: model.ContentText, Text: "hi"}})
	if err != nil || sent.ID != "sent-1" || sent.ChatID != "alice" {
		t.Fatalf("send: %+v %v", sent, err)
	}
	fake.mu.Lock()
	n := len(fake.sent)
	fake.mu.Unlock()
	if n != 1 || fake.sent[0].Body != "hi" {
		t.Fatalf("network did not receive the message: %+v", fake.sent)
	}
	// Chat info comes from the connector.
	ch, err := host.GetChat(ctx, "acc1", "alice")
	if err != nil || ch.Name != "Alice" || ch.Kind != model.ChatDirect || len(ch.Participants) != 2 {
		t.Fatalf("chat: %+v %v", ch, err)
	}
	// Restart: the login is loaded from the bridgev2 database and reconnects on its own.
	if err := host.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	fake2 := &fakeNet{}
	host2 := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "fakenet", func() bridgev2.NetworkConnector { return fake2 })
	sink2 := &recSink{}
	_ = host2.Start(ctx, sink2)
	if err := host2.AddAccount(ctx, "acc1", nil, dir); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return sink2.lastStatus() == model.StatusConnected })
	if err := host2.RemoveAccount(ctx, "acc1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("account dir not removed")
	}
}

// remote queues any remote event from "alice" in the DM portal.
func (f *fakeNet) remote(evt bridgev2.RemoteEvent) {
	f.mu.Lock()
	login := f.login
	f.mu.Unlock()
	f.bridge.QueueRemoteEvent(login, evt)
}

func (f *fakeNet) meta(typ bridgev2.RemoteEventType) simplevent.EventMeta {
	f.mu.Lock()
	login := f.login
	f.mu.Unlock()
	return simplevent.EventMeta{Type: typ, PortalKey: networkid.PortalKey{ID: "alice", Receiver: login.ID},
		Sender: bridgev2.EventSender{Sender: "alice"}, CreatePortal: true, Timestamp: time.Now()}
}

func TestHostedConnectorEventConversions(t *testing.T) {
	ctx := context.Background()
	fake := &fakeNet{}
	host := New(slog.New(slog.NewTextHandler(io.Discard, nil)), "fakenet", func() bridgev2.NetworkConnector { return fake })
	sink := &recSink{}
	if err := host.Start(ctx, sink); err != nil {
		t.Fatal(err)
	}
	if err := host.AddAccount(ctx, "acc1", nil, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := host.LoginStart(ctx, "acc1", "phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := host.LoginSubmit(ctx, "acc1", map[string]string{"phone": "+1"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return sink.lastStatus() == model.StatusConnected })
	fake.push("m1", "hello")
	eventually(t, func() bool {
		return sink.find(adapter.EvMessage, func(e adapter.Event) bool { return e.Message != nil && e.Message.ID == "m1" }) != nil
	})

	// Media: the connector uploads during conversion; the message arrives with the attachment ready.
	fake.remote(&simplevent.Message[[]byte]{EventMeta: fake.meta(bridgev2.RemoteEventMessage), ID: "m2", Data: []byte("png"),
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data []byte) (*bridgev2.ConvertedMessage, error) {
			url, _, err := intent.UploadMedia(ctx, portal.MXID, data, "a.png", "image/png")
			if err != nil {
				return nil, err
			}
			return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{Type: event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgImage, Body: "a.png", URL: url, Info: &event.FileInfo{MimeType: "image/png", Size: 3}}}}}, nil
		}})
	eventually(t, func() bool {
		return sink.find(adapter.EvMessage, func(e adapter.Event) bool {
			m := e.Message
			return m != nil && m.ID == "m2" && m.Content.Type == model.ContentImage && len(m.Content.Attachments) == 1 &&
				m.Content.Attachments[0].State == model.MediaReady && m.Content.Attachments[0].Mime == "image/png" && m.Content.Attachments[0].Size == 3
		}) != nil
	})

	// Reaction add and remove.
	fake.remote(&simplevent.Reaction{EventMeta: fake.meta(bridgev2.RemoteEventReaction), TargetMessage: "m1", EmojiID: "👍", Emoji: "👍"})
	eventually(t, func() bool {
		return sink.find(adapter.EvReaction, func(e adapter.Event) bool {
			return e.ChatID == "alice" && e.MessageID == "m1" && e.UserID == "alice" && e.Emoji == "👍" && !e.Removed
		}) != nil
	})
	fake.remote(&simplevent.Reaction{EventMeta: fake.meta(bridgev2.RemoteEventReactionRemove), TargetMessage: "m1", EmojiID: "👍", Emoji: "👍"})
	eventually(t, func() bool {
		return sink.find(adapter.EvReaction, func(e adapter.Event) bool { return e.MessageID == "m1" && e.Emoji == "👍" && e.Removed }) != nil
	})

	// Read receipt and typing.
	fake.remote(&simplevent.Receipt{EventMeta: fake.meta(bridgev2.RemoteEventReadReceipt), LastTarget: "m1"})
	eventually(t, func() bool {
		return sink.find(adapter.EvReceipt, func(e adapter.Event) bool {
			return e.ChatID == "alice" && e.UserID == "alice" && e.Receipt == "read" && len(e.MessageIDs) == 1 && e.MessageIDs[0] == "m1"
		}) != nil
	})
	fake.remote(&simplevent.Typing{EventMeta: fake.meta(bridgev2.RemoteEventTyping), Timeout: 5 * time.Second})
	eventually(t, func() bool {
		return sink.find(adapter.EvTyping, func(e adapter.Event) bool { return e.ChatID == "alice" && e.UserID == "alice" && e.State == "typing" }) != nil
	})

	// Edit and delete.
	fake.remote(&simplevent.Message[string]{EventMeta: fake.meta(bridgev2.RemoteEventEdit), ID: "e1", TargetMessage: "m1", Data: "hello!",
		ConvertEditFunc: func(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, existing []*database.Message, data string) (*bridgev2.ConvertedEdit, error) {
			return &bridgev2.ConvertedEdit{ModifiedParts: []*bridgev2.ConvertedEditPart{{Part: existing[0], Type: event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgText, Body: data}}}}, nil
		}})
	eventually(t, func() bool {
		return sink.find(adapter.EvMessageUpdate, func(e adapter.Event) bool {
			return e.ChatID == "alice" && e.MessageID == "m1" && e.Content != nil && e.Content.Text == "hello!"
		}) != nil
	})
	fake.remote(&simplevent.MessageRemove{EventMeta: fake.meta(bridgev2.RemoteEventMessageRemove), TargetMessage: "m2"})
	eventually(t, func() bool {
		return sink.find(adapter.EvMessageDelete, func(e adapter.Event) bool { return e.ChatID == "alice" && e.MessageID == "m2" }) != nil
	})
	if err := host.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHostInstance(t *testing.T) {
	newFake := func() bridgev2.NetworkConnector { return &fakeNet{} }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if info := New(log, "fakenet", newFake).Info(); info.Instance != "" {
		t.Fatalf("default instance: %q", info.Instance)
	}
	if info := New(log, "fakenet", newFake, WithInstance("bridgev2")).Info(); info.Instance != "bridgev2" {
		t.Fatalf("instance: %q", info.Instance)
	}
}

// cfgNet is a connector with a YAML config, like mautrix's own.
type cfgNet struct {
	fakeNet
	cfg struct {
		APIID   int    `yaml:"api_id"`
		APIHash string `yaml:"api_hash"`
		Device  string `yaml:"device"`
	}
}

func (c *cfgNet) GetConfig() (string, any, configupgrade.Upgrader) {
	return "api_id: 1\napi_hash: example\ndevice: example\n", &c.cfg, configupgrade.NoopUpgrader
}

func TestNetworkConfigPrecedence(t *testing.T) {
	// Example < host defaults < the account's `network`.
	net := &cfgNet{}
	if err := loadNetworkConfig(net, "api_id: 2\napi_hash: server\n", json.RawMessage(`{"network":{"api_hash":"account"}}`)); err != nil {
		t.Fatal(err)
	}
	if net.cfg.APIID != 2 || net.cfg.APIHash != "account" || net.cfg.Device != "example" {
		t.Fatalf("config: %+v", net.cfg)
	}
	// Defaults apply without an account overlay.
	net = &cfgNet{}
	if err := loadNetworkConfig(net, "api_hash: server\n", nil); err != nil || net.cfg.APIID != 1 || net.cfg.APIHash != "server" {
		t.Fatalf("defaults only: %+v %v", net.cfg, err)
	}
}

func TestUploadLimitAndStreaming(t *testing.T) {
	ctx := context.Background()
	sink := &recSink{}
	acc := &account{host: &Adapter{maxFileSize: 4}, rep: base.Reporter{Sink: sink, ID: "acc1"}, stored: map[string]model.Attachment{}}
	in := &virtualIntent{vm: &virtualMatrix{acc: acc}}

	if _, _, err := in.UploadMedia(ctx, "", []byte("12345"), "a.bin", "application/octet-stream"); !errors.Is(err, bridgev2.ErrMediaTooLarge) {
		t.Fatalf("oversized upload: %v", err)
	}
	called := false
	_, _, err := in.UploadMediaStream(ctx, "", 5, false, func(io.Writer) (*bridgev2.FileStreamResult, error) {
		called = true
		return nil, nil
	})
	if !errors.Is(err, bridgev2.ErrMediaTooLarge) || called {
		t.Fatalf("oversized declared stream: %v (callback called: %v)", err, called)
	}
	// An undeclared size is checked after the callback wrote the file.
	_, _, err = in.UploadMediaStream(ctx, "", 0, false, func(w io.Writer) (*bridgev2.FileStreamResult, error) {
		_, err := w.Write([]byte("12345"))
		return nil, err
	})
	if !errors.Is(err, bridgev2.ErrMediaTooLarge) {
		t.Fatalf("oversized undeclared stream: %v", err)
	}
	// Within the limit the stream reaches the sink with the callback's name and type.
	uri, _, err := in.UploadMediaStream(ctx, "", 4, false, func(w io.Writer) (*bridgev2.FileStreamResult, error) {
		_, err := w.Write([]byte("1234"))
		return &bridgev2.FileStreamResult{FileName: "b.png", MimeType: "image/png"}, err
	})
	if err != nil || !strings.HasPrefix(string(uri), "mxc://"+serverName+"/") {
		t.Fatalf("stream: %s %v", uri, err)
	}
	if att := acc.stored[mediaIDOf(uri)]; att.Size != 4 || att.Mime != "image/png" {
		t.Fatalf("stored: %+v", att)
	}
	// Without WithMaxFileSize the host rejects nothing.
	acc.host.maxFileSize = 0
	if _, _, err := in.UploadMedia(ctx, "", []byte("12345"), "a.bin", ""); err != nil {
		t.Fatalf("unlimited upload: %v", err)
	}
}
