package whatsapp

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

var (
	ownPN    = types.NewJID("10000000000", types.DefaultUserServer)
	ownLID   = types.NewJID("90000000000001", types.HiddenUserServer)
	alicePN  = types.NewJID("8613800000000", types.DefaultUserServer)
	aliceLID = types.NewJID("235978975346820", types.HiddenUserServer)
	bobPN    = types.NewJID("4915100000000", types.DefaultUserServer)
)

// testAccount is a paired device store (no network) with Alice's phone ↔ LID mapping and her
// address-book name under the phone number and push name under the LID.
func testAccount(t *testing.T) *account {
	t.Helper()
	ctx := context.Background()
	container, err := sqlstore.New(ctx, "sqlite", "file:"+filepath.Join(t.TempDir(), "wa.db")+"?_pragma=foreign_keys(1)", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Close() })
	device := container.NewDevice()
	own := ownPN
	own.Device = 12
	device.ID, device.LID = &own, ownLID
	device.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{0}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32),
		DeviceSignature: make([]byte, 64)}
	if err := container.PutDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err := device.LIDs.PutLIDMapping(ctx, aliceLID, alicePN); err != nil {
		t.Fatal(err)
	}
	if err := device.Contacts.PutContactName(ctx, alicePN, "向日葵", "Sun"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := device.Contacts.PutPushName(ctx, aliceLID, "youli"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := device.Contacts.PutPushName(ctx, bobPN, "bob"); err != nil {
		t.Fatal(err)
	}
	return &account{id: "wa", container: container, cli: whatsmeow.NewClient(device, nil)}
}

func text(s string) *waE2E.Message { return &waE2E.Message{Conversation: proto.String(s)} }

func only(t *testing.T, evs []adapter.Event, kind string) adapter.Event {
	t.Helper()
	if len(evs) != 1 || evs[0].Kind != kind {
		t.Fatalf("want one %s event, got %+v", kind, evs)
	}
	return evs[0]
}

func TestLIDIsTheUserID(t *testing.T) {
	acc := testAccount(t)
	at := time.Unix(1_700_000_000, 0)

	// History sync gives the phone form without an alternate: the LID map decides.
	in := &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: alicePN, Sender: alicePN}, ID: "M1", PushName: "youli", Timestamp: at},
		Message: text("hi")}
	ev := only(t, acc.convertMessage(in), adapter.EvMessage)
	if ev.Message.ChatID != aliceLID.String() || ev.Message.Sender.ID != aliceLID.String() || ev.Sender.Phone != "+8613800000000" {
		t.Fatalf("inbound: chat=%s sender=%s phone=%s", ev.Message.ChatID, ev.Message.Sender.ID, ev.Sender.Phone)
	}
	// The first resolution queues one identity event; later ones do not.
	acc.convertMessage(in)
	if len(acc.pending) != 1 || acc.pending[0].Kind != adapter.EvIdentity || acc.pending[0].UserID != alicePN.String() || acc.pending[0].NewID != aliceLID.String() {
		t.Fatalf("pending identity: %+v", acc.pending)
	}

	// Own message sent from the phone into the DM: the account itself is its LID too.
	self := ownPN
	self.Device = 3
	out := &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: alicePN, Sender: self, IsFromMe: true}, ID: "M2", Timestamp: at},
		Message: text("yo")}
	ev = only(t, acc.convertMessage(out), adapter.EvMessage)
	if ev.Message.ChatID != aliceLID.String() || ev.Message.Sender.ID != ownLID.String() || !ev.Message.FromMe {
		t.Fatalf("outbound: %+v", ev.Message)
	}

	// A LID-addressed message with the phone as alternate still teaches the pair.
	fresh := testAccount(t)
	lidMsg := &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: aliceLID, Sender: aliceLID, SenderAlt: alicePN}, ID: "M3", Timestamp: at},
		Message: text("x")}
	if ev := only(t, fresh.convertMessage(lidMsg), adapter.EvMessage); ev.Message.ChatID != aliceLID.String() {
		t.Fatalf("lid chat: %s", ev.Message.ChatID)
	}
	if len(fresh.pending) != 1 || fresh.pending[0].UserID != alicePN.String() {
		t.Fatalf("pair from alt: %+v", fresh.pending)
	}

	// Unknown numbers stay phone JIDs.
	if got := acc.userJID(bobPN, types.EmptyJID); got != bobPN {
		t.Fatalf("unmapped: %s", got)
	}
	// Mentions resolve too.
	mention := &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("120363", types.GroupServer), Sender: bobPN, IsGroup: true},
		ID: "M4", Timestamp: at}, Message: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("@a"),
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{alicePN.String()}}}}}
	if ev := only(t, acc.convertMessage(mention), adapter.EvMessage); len(ev.Message.Mentions) != 1 || ev.Message.Mentions[0] != aliceLID.String() {
		t.Fatalf("mentions: %v", ev.Message.Mentions)
	}
}

func TestContactsMergeBothForms(t *testing.T) {
	acc := testAccount(t)
	all, err := acc.allContacts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]model.Contact{}
	for _, c := range all {
		byID[c.ID] = c
	}
	if len(all) != 2 {
		t.Fatalf("one entry per user, got %+v", all)
	}
	alice := byID[aliceLID.String()]
	if alice.Phone != "+8613800000000" || alice.Names.Alias != "Sun" || alice.Names.First != "向日葵" || alice.Names.Profile != "youli" || !alice.IsContact {
		t.Fatalf("alice: %+v", alice)
	}
	if bob := byID[bobPN.String()]; bob.Phone != "+4915100000000" || bob.Names.Profile != "bob" {
		t.Fatalf("bob: %+v", bob)
	}
	if self := acc.selfContact(); self.ID != ownLID.String() || self.Phone != "+10000000000" {
		t.Fatalf("self: %+v", self)
	}
}

// recordSink keeps the events an account reports.
type recordSink struct{ events []adapter.Event }

func (r *recordSink) Status(context.Context, string, adapter.Status) error     { return nil }
func (r *recordSink) LoginStep(context.Context, string, model.LoginStep) error { return nil }
func (r *recordSink) Events(_ context.Context, _ string, evs []adapter.Event) error {
	r.events = append(r.events, evs...)
	return nil
}
func (r *recordSink) PutMedia(context.Context, string, string, adapter.MediaMeta, io.Reader) (model.Attachment, error) {
	return model.Attachment{}, nil
}

func TestCanonicalIDs(t *testing.T) {
	acc := testAccount(t)
	rec := &recordSink{}
	acc.rep = base.Reporter{Sink: rec, ID: "wa", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a := &Adapter{}
	a.accounts.Put("wa", acc)
	got, err := a.CanonicalIDs(context.Background(), "wa", []string{alicePN.String(), bobPN.String(), ownPN.String(), aliceLID.String(), ownLID.String(), "120363@g.us"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[alicePN.String()] != aliceLID.String() || got[ownPN.String()] != ownLID.String() {
		t.Fatalf("canonical ids: %v", got)
	}
	// Stored LID users get their phone number (and names) re-emitted; the account itself does not.
	if len(rec.events) != 1 || rec.events[0].Kind != adapter.EvContact || rec.events[0].Contact.ID != aliceLID.String() ||
		rec.events[0].Contact.Phone != "+8613800000000" || rec.events[0].Contact.Names.Alias != "Sun" {
		t.Fatalf("phone refresh: %+v", rec.events)
	}
	// Already reported pairs are not announced again as events.
	acc.userJID(alicePN, types.EmptyJID)
	if len(acc.pending) != 0 {
		t.Fatalf("pending after resolve: %+v", acc.pending)
	}
}

func TestGroupParticipantsUseLID(t *testing.T) {
	acc := testAccount(t)
	bobLID := types.NewJID("555", types.HiddenUserServer)
	ch := acc.groupChat(&types.GroupInfo{JID: types.NewJID("120363", types.GroupServer), Participants: []types.GroupParticipant{
		{JID: bobLID, LID: bobLID, PhoneNumber: bobPN, IsAdmin: true},
		{JID: alicePN, PhoneNumber: alicePN},
	}})
	if len(ch.Participants) != 2 || ch.Participants[0].ID != bobLID.String() || ch.Participants[1].ID != aliceLID.String() {
		t.Fatalf("participants: %+v", ch.Participants)
	}
	if len(acc.pending) != 2 {
		t.Fatalf("identities from participants: %+v", acc.pending)
	}
}

func TestNewerMessageTypes(t *testing.T) {
	acc := testAccount(t)
	at := time.Unix(1_700_000_000, 0)
	group := types.NewJID("120363", types.GroupServer)
	info := func(id string) types.MessageInfo {
		return types.MessageInfo{MessageSource: types.MessageSource{Chat: group, Sender: aliceLID, IsGroup: true}, ID: id, Timestamp: at}
	}
	image := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(10)}}
	child := func(kind waE2E.MessageAssociation_AssociationType) *waE2E.Message {
		return &waE2E.Message{
			MessageContextInfo: &waE2E.MessageContextInfo{MessageAssociation: &waE2E.MessageAssociation{AssociationType: kind.Enum(),
				ParentMessageKey: &waCommon.MessageKey{ID: proto.String("PARENT")}}},
			AssociatedChildMessage: &waE2E.FutureProofMessage{Message: image},
		}
	}

	hd := only(t, acc.convertMessage(&events.Message{Info: info("CHILD"), Message: child(waE2E.MessageAssociation_HD_IMAGE_DUAL_UPLOAD)}), adapter.EvMessage)
	if hd.Message.ID != "PARENT" || hd.Message.Content.Type != model.ContentImage || hd.Message.Content.Attachments[0].MediaID != "PARENT" {
		t.Fatalf("hd child: %+v", hd.Message)
	}
	if evs := acc.convertMessage(&events.Message{Info: info("MOTION"), Message: child(waE2E.MessageAssociation_MOTION_PHOTO)}); evs != nil {
		t.Fatalf("motion photo: %+v", evs)
	}
	if evs := acc.convertMessage(&events.Message{Info: info("ALBUM"), Message: &waE2E.Message{AlbumMessage: &waE2E.AlbumMessage{}}}); evs != nil {
		t.Fatalf("album header: %+v", evs)
	}

	tpl := &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{Format: &waE2E.TemplateMessage_HydratedFourRowTemplate_{
		HydratedFourRowTemplate: &waE2E.TemplateMessage_HydratedFourRowTemplate{
			HydratedContentText: proto.String("ㅤ Would tomorrow work? ㅤ"), HydratedFooterText: proto.String("ㅤㅤ"),
			HydratedButtons: []*waE2E.HydratedTemplateButton{{HydratedButton: &waE2E.HydratedTemplateButton_QuickReplyButton{
				QuickReplyButton: &waE2E.HydratedTemplateButton_HydratedQuickReplyButton{DisplayText: proto.String("Yes")}}}},
		}}}}
	if ev := only(t, acc.convertMessage(&events.Message{Info: info("TPL"), Message: tpl}), adapter.EvMessage); ev.Message.Content.Type != model.ContentText ||
		ev.Message.Content.Text != "Would tomorrow work?\n\n[Yes]" {
		t.Fatalf("template: %q", ev.Message.Content.Text)
	}

	notice := &waE2E.Message{MessageHistoryNotice: &waE2E.MessageHistoryNotice{MessageHistoryMetadata: &waE2E.MessageHistoryMetadata{
		HistoryReceivers: []string{alicePN.String()}, MessageCount: proto.Int64(30)}}}
	ev := only(t, acc.convertMessage(&events.Message{Info: info("HIST"), Message: notice}), adapter.EvMessage)
	if s := ev.Message.Content.System; s == nil || s.Kind != "history_shared" || s.Value != "30" || len(s.Targets) != 1 || s.Targets[0] != aliceLID.String() {
		t.Fatalf("history notice: %+v", ev.Message.Content)
	}

	masked := &waE2E.Message{PlaceholderMessage: &waE2E.PlaceholderMessage{Type: waE2E.PlaceholderMessage_MASK_LINKED_DEVICES.Enum()}}
	ev = only(t, acc.convertMessage(&events.Message{Info: info("OTP"), Message: masked}), adapter.EvMessage)
	if s := ev.Message.Content.System; s == nil || s.Kind != "primary_device_only" {
		t.Fatalf("placeholder: %+v", ev.Message.Content)
	}
}
