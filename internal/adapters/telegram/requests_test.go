package telegram

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/model"
)

func TestCallAndJoinRequests(t *testing.T) {
	ents := tg.Entities{Users: map[int64]*tg.User{7: {ID: 7, FirstName: "Bob"}, 8: {ID: 8, FirstName: "Carol", LastName: "K"}},
		Chats: map[int64]*tg.Chat{}, Channels: map[int64]*tg.Channel{55: {ID: 55, Title: "News", Broadcast: true}}}
	date := int(time.Unix(1_700_000_000, 0).Unix())

	r := callRequest(&tg.PhoneCallRequested{ID: 99, AccessHash: 5, Date: date, AdminID: 7, ParticipantID: 42, Video: true}, ents, 42)
	if r == nil || r.Key != "call:99" || r.Kind != model.RequestKindCall || r.CallKind != "video" || r.FromName != "Bob" || r.FromID != userID(7) ||
		r.ChatID != userID(7) || r.ExpiresAt == nil {
		t.Fatalf("incoming call: %+v", r)
	}
	var ref callRef
	if err := json.Unmarshal(r.PlatformRef, &ref); err != nil || ref.ID != 99 || ref.AccessHash != 5 {
		t.Fatalf("call ref: %s", r.PlatformRef)
	}
	if callRequest(&tg.PhoneCallRequested{ID: 1, AdminID: 42, ParticipantID: 7}, ents, 42) != nil {
		t.Fatal("outgoing call is not a request")
	}
	if end := callRequest(&tg.PhoneCallDiscarded{ID: 99}, ents, 42); end == nil || end.State != model.RequestExpired || end.Key != "call:99" {
		t.Fatalf("discarded: %+v", end)
	}
	if up := callRequest(&tg.PhoneCall{ID: 99}, ents, 42); up == nil || up.State != model.RequestAccepted {
		t.Fatalf("established: %+v", up)
	}

	evs := joinRequests(&tg.UpdatePendingJoinRequests{Peer: &tg.PeerChannel{ChannelID: 55}, RequestsPending: 2, RecentRequesters: []int64{7, 8}}, ents)
	if len(evs) != 2 {
		t.Fatalf("join requests: %+v", evs)
	}
	j := evs[1].Request
	if j.Kind != model.RequestKindJoin || j.ChatID != channelID(55) || j.ChatName != "News" || j.ChatKind != model.ChatChannel ||
		j.FromName != "Carol K" || j.Key != "join:"+channelID(55)+":"+userID(8) || !j.CreatedAt.IsZero() {
		t.Fatalf("join request: %+v", j)
	}
}
