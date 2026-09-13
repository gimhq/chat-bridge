package matrix

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

// recSink captures what an account reports.
type recSink struct {
	events []adapter.Event
}

func (r *recSink) Status(context.Context, string, adapter.Status) error     { return nil }
func (r *recSink) LoginStep(context.Context, string, model.LoginStep) error { return nil }
func (r *recSink) Events(_ context.Context, _ string, evs []adapter.Event) error {
	r.events = append(r.events, evs...)
	return nil
}
func (r *recSink) PutMedia(context.Context, string, string, adapter.MediaMeta, io.Reader) (model.Attachment, error) {
	return model.Attachment{}, nil
}

func testAccount(t *testing.T) (*account, *recSink) {
	t.Helper()
	sink := &recSink{}
	acc := &account{id: "mx", dir: t.TempDir(), cfg: config{Homeserver: "https://example.org"},
		rep:      base.Reporter{Sink: sink, ID: "mx", Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		roomKind: map[id.RoomID]string{}, reactions: map[string]id.EventID{}}
	cli, err := mautrix.NewClient("https://example.org", "@me:example.org", "tok")
	if err != nil {
		t.Fatal(err)
	}
	cli.Syncer = mautrix.NewDefaultSyncer()
	acc.cli = cli
	return acc, sink
}

func TestOpenCryptoOnModernc(t *testing.T) {
	acc, _ := testAccount(t)
	s := session{UserID: "@me:example.org", DeviceID: "DEV", AccessToken: "tok"}
	// First open generates and persists a pickle key; the crypto database is created without cgo.
	if err := acc.openCrypto(&s); err != nil {
		t.Fatal(err)
	}
	if len(s.PickleKey) != 32 {
		t.Fatalf("pickle key not generated: %d bytes", len(s.PickleKey))
	}
	if _, err := os.Stat(filepath.Join(acc.dir, cryptoFile)); err != nil {
		t.Fatalf("crypto db missing: %v", err)
	}
	if acc.cli.Crypto == nil {
		t.Fatal("client has no crypto helper")
	}
	key := append([]byte(nil), s.PickleKey...)
	acc.closeCrypto()
	// Reopening with the stored key reuses it.
	if err := acc.openCrypto(&s); err != nil {
		t.Fatal(err)
	}
	if string(s.PickleKey) != string(key) {
		t.Fatal("pickle key changed on reopen")
	}
	acc.closeCrypto()
	// Logout wipes the database.
	acc.removeCrypto()
	if _, err := os.Stat(filepath.Join(acc.dir, cryptoFile)); !os.IsNotExist(err) {
		t.Fatalf("crypto db not removed: %v", err)
	}
}

func TestEncryptedAttachmentRef(t *testing.T) {
	c := &event.MessageEventContent{MsgType: event.MsgImage, Body: "x.png", Info: &event.FileInfo{MimeType: "image/png", Size: 3},
		File: &event.EncryptedFileInfo{URL: "mxc://hs/enc"}}
	c.File.Key.Key = "k"
	c.File.InitVector = "iv"
	content := convertContent(c, event.EventMessage, "e1")
	att := content.Attachments[0]
	if att.State != model.MediaRemote {
		t.Fatalf("encrypted attachment must stay fetchable, got %s", att.State)
	}
	var ref remoteRef
	if err := json.Unmarshal(att.RemoteRef, &ref); err != nil || ref.URL != "mxc://hs/enc" || ref.File == nil || ref.File.InitVector != "iv" {
		t.Fatalf("ref: %+v %v", ref, err)
	}
}

func TestDirectChatNamedFromMember(t *testing.T) {
	acc, sink := testAccount(t)
	room := id.RoomID("!dm:example.org")
	acc.roomKind[room] = model.ChatDirect
	other := "@alice:example.org"
	evt := &event.Event{Type: event.StateMember, RoomID: room, Sender: id.UserID(other), StateKey: &other, Timestamp: 1}
	evt.Content.Parsed = &event.MemberEventContent{Membership: event.MembershipJoin, Displayname: "Alice"}
	acc.onMember(context.Background(), evt)
	named := false
	for _, ev := range sink.events {
		if ev.Kind == adapter.EvChat && ev.Chat != nil && ev.Chat.ID == room.String() && ev.Chat.Name == "Alice" && ev.Chat.Kind == model.ChatDirect {
			named = true
		}
	}
	if !named {
		t.Fatalf("no chat hint with the peer's name: %+v", sink.events)
	}
	// Our own membership in a DM does not name the chat after us.
	sink.events = nil
	me := "@me:example.org"
	evt = &event.Event{Type: event.StateMember, RoomID: room, Sender: id.UserID(me), StateKey: &me, Timestamp: 2}
	evt.Content.Parsed = &event.MemberEventContent{Membership: event.MembershipJoin, Displayname: "Me"}
	acc.onMember(context.Background(), evt)
	for _, ev := range sink.events {
		if ev.Kind == adapter.EvChat && ev.Chat != nil && ev.Chat.Name == "Me" {
			t.Fatal("chat named after self")
		}
	}
}
