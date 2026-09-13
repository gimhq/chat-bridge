package matrix

import (
	"encoding/json"
	"testing"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/model"
)

func TestInviteAndCallRequests(t *testing.T) {
	room := id.RoomID("!r:example.org")
	inviter := id.UserID("@alice:example.org")

	inv := inviteRequest(&event.Event{RoomID: room, Sender: inviter}, &event.MemberEventContent{Membership: event.MembershipInvite, IsDirect: true, Reason: "hi"}, "")
	if inv.Key != "invite:!r:example.org" || inv.Kind != model.RequestKindChatInvite || inv.ChatKind != model.ChatDirect || inv.FromID != inviter.String() ||
		inv.Message != "hi" || !inv.CreatedAt.IsZero() {
		t.Fatalf("invite: %+v", inv)
	}

	now := time.UnixMilli(1_700_000_000_000)
	evt := &event.Event{RoomID: room, Sender: inviter, Timestamp: now.Add(-10 * time.Second).UnixMilli()}
	call := &event.CallInviteEventContent{BaseCallEventContent: event.BaseCallEventContent{CallID: "c1", Version: "1"}, Lifetime: 60_000,
		Offer: event.CallData{SDP: "v=0\r\nm=audio 9 UDP\r\nm=video 9 UDP\r\n"}}
	r := callRequest(evt, call, model.ChatDirect, now)
	if r == nil || r.Key != "call:c1" || r.CallKind != "video" || r.ExpiresAt == nil || !r.ExpiresAt.Equal(msTime(evt.Timestamp).Add(time.Minute)) {
		t.Fatalf("call: %+v", r)
	}
	var ref callRef
	if err := json.Unmarshal(r.PlatformRef, &ref); err != nil || ref.Room != room.String() || ref.Version != "1" {
		t.Fatalf("call ref: %s", r.PlatformRef)
	}
	call.Offer.SDP = "m=audio 9 UDP"
	call.Lifetime = 0
	if r = callRequest(evt, call, model.ChatDirect, now); r == nil || r.CallKind != "voice" {
		t.Fatalf("voice call: %+v", r)
	}
	if callRequest(evt, call, model.ChatDirect, now.Add(2*time.Minute)) != nil {
		t.Fatal("a ring that is over is not a request")
	}

	hangup := &event.Event{Type: event.CallHangup, Sender: inviter, Content: event.Content{Parsed: &event.CallHangupEventContent{BaseCallEventContent: event.BaseCallEventContent{CallID: "c1"}}}}
	if end := callEnd(hangup, "@me:example.org"); end == nil || end.State != model.RequestExpired {
		t.Fatalf("hangup: %+v", end)
	}
	answer := &event.Event{Type: event.CallAnswer, Sender: "@me:example.org", Content: event.Content{Parsed: &event.CallAnswerEventContent{BaseCallEventContent: event.BaseCallEventContent{CallID: "c1"}}}}
	if end := callEnd(answer, "@me:example.org"); end == nil || end.State != model.RequestAccepted {
		t.Fatalf("answered elsewhere: %+v", end)
	}

	if typ, _ := rejectContent(callRef{CallID: "c1", Version: "1"}, "DEV"); typ != event.CallReject {
		t.Fatalf("v1 reject type: %v", typ)
	}
	if typ, c := rejectContent(callRef{CallID: "c1"}, "DEV"); typ != event.CallHangup || c.(*event.CallHangupEventContent).Reason != event.CallHangupUserHangup {
		t.Fatalf("legacy reject: %v %+v", typ, c)
	}
}
