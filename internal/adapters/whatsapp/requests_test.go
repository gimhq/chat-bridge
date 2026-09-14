package whatsapp

import (
	"encoding/json"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"gimhq/chat-bridge/internal/model"
)

func TestCallAndInviteRequests(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	alice := types.NewJID("8613800000000", types.DefaultUserServer)

	r := callRequest(&events.CallOffer{BasicCallMeta: types.BasicCallMeta{From: alice, CallCreator: alice, CallID: "C1", Timestamp: at}}, alice, alice)
	if r.Key != "call:C1" || r.Kind != model.RequestKindCall || r.FromID != alice.String() || r.ChatID != alice.String() ||
		r.ChatKind != model.ChatDirect || r.ExpiresAt == nil || !r.ExpiresAt.Equal(at.Add(callRingTimeout)) {
		t.Fatalf("call: %+v", r)
	}
	var ref callRef
	if err := json.Unmarshal(r.PlatformRef, &ref); err != nil || ref.Creator != alice.String() || ref.CallID != "C1" {
		t.Fatalf("call ref: %s", r.PlatformRef)
	}
	if end := callEnded("C1"); end.Key != "call:C1" || end.State != model.RequestExpired {
		t.Fatalf("call end: %+v", end)
	}

	msg := func(fromMe bool, m *waE2E.Message) *events.Message {
		return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: alice, Sender: alice, IsFromMe: fromMe},
			ID: "M1", PushName: "Alice", Timestamp: at}, Message: m}
	}
	invite := &waE2E.Message{GroupInviteMessage: &waE2E.GroupInviteMessage{GroupJID: proto.String("120363@g.us"), InviteCode: proto.String("CODE"),
		InviteExpiration: proto.Int64(at.Add(72 * time.Hour).Unix()), GroupName: proto.String("Team"), Caption: proto.String("join us")}}
	r = inviteRequest(msg(false, invite), alice)
	if r == nil || r.Key != "invite:120363@g.us:CODE" || r.Kind != model.RequestKindChatInvite || r.FromName != "Alice" || r.ChatName != "Team" ||
		r.Message != "join us" || r.ExpiresAt == nil || !r.CreatedAt.Equal(at) {
		t.Fatalf("invite: %+v", r)
	}
	var iref inviteRef
	if err := json.Unmarshal(r.PlatformRef, &iref); err != nil || iref.Group != "120363@g.us" || iref.Inviter != alice.String() || iref.Code != "CODE" {
		t.Fatalf("invite ref: %s", r.PlatformRef)
	}
	if inviteRequest(msg(true, invite), alice) != nil {
		t.Fatal("own invites are not requests")
	}
	if inviteRequest(msg(false, &waE2E.Message{Conversation: proto.String("hi")}), alice) != nil {
		t.Fatal("plain text is not an invite")
	}
}
