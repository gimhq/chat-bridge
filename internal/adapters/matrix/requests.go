package matrix

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

var _ adapter.RequestAnswerer = (*Adapter)(nil)

// defaultCallLifetime applies when an invite omits lifetime.
const defaultCallLifetime = 60 * time.Second

type callRef struct {
	Room    string `json:"room"`
	CallID  string `json:"call_id"`
	Version string `json:"version,omitempty"`
}

func inviteKey(room id.RoomID) string { return "invite:" + room.String() }

// inviteRequest builds a room invite. Stripped invite state carries no timestamp; CreatedAt then
// stays zero and the store stamps the first sighting.
func inviteRequest(evt *event.Event, m *event.MemberEventContent, name string) *adapter.Request {
	kind := model.ChatGroup
	if m.IsDirect {
		kind = model.ChatDirect
	}
	r := &adapter.Request{Key: inviteKey(evt.RoomID), Kind: model.RequestKindChatInvite, FromID: evt.Sender.String(), ChatID: evt.RoomID.String(),
		ChatName: name, ChatKind: kind, Message: m.Reason}
	if evt.Timestamp > 0 {
		r.CreatedAt = msTime(evt.Timestamp)
	}
	return r
}

// rememberInvite reports an invite for the own user and keeps it until the room name arrives.
func (acc *account) rememberInvite(evt *event.Event, m *event.MemberEventContent) {
	acc.mu.Lock()
	if acc.invites == nil {
		acc.invites = map[id.RoomID]*adapter.Request{}
	}
	r := inviteRequest(evt, m, acc.inviteNames[evt.RoomID])
	acc.invites[evt.RoomID] = r
	cp := *r
	acc.mu.Unlock()
	acc.rep.Events(adapter.Event{Kind: adapter.EvRequest, Request: &cp})
}

// inviteName records a room name from invite state and refreshes an already reported invite.
func (acc *account) inviteName(room id.RoomID, name string) {
	acc.mu.Lock()
	if acc.inviteNames == nil {
		acc.inviteNames = map[id.RoomID]string{}
	}
	acc.inviteNames[room] = name
	var cp *adapter.Request
	if r := acc.invites[room]; r != nil && name != "" {
		r.ChatName = name
		c := *r
		cp = &c
	}
	acc.mu.Unlock()
	if cp != nil {
		acc.rep.Events(adapter.Event{Kind: adapter.EvRequest, Request: cp})
	}
}

// inviteResolution maps the own membership after an invite: joined accepts, left rejects.
func (acc *account) inviteResolution(evt *event.Event, m *event.MemberEventContent) *adapter.Request {
	if prev := evt.Unsigned.PrevContent; prev != nil {
		if pm := prev.AsMember(); pm == nil || pm.Membership != event.MembershipInvite {
			return nil
		}
	}
	state := ""
	switch m.Membership {
	case event.MembershipJoin:
		state = model.RequestAccepted
	case event.MembershipLeave, event.MembershipBan:
		state = model.RequestRejected
	default:
		return nil
	}
	acc.mu.Lock()
	delete(acc.invites, evt.RoomID)
	delete(acc.inviteNames, evt.RoomID)
	acc.mu.Unlock()
	return &adapter.Request{Key: inviteKey(evt.RoomID), Kind: model.RequestKindChatInvite, State: state}
}

// callRequest turns m.call.invite into a request; nil when the ring is already over (initial
// sync replays old invites).
func callRequest(evt *event.Event, c *event.CallInviteEventContent, chatKind string, now time.Time) *adapter.Request {
	if c == nil || c.CallID == "" {
		return nil
	}
	created := msTime(evt.Timestamp)
	life := time.Duration(c.Lifetime) * time.Millisecond
	if life <= 0 {
		life = defaultCallLifetime
	}
	exp := created.Add(life)
	if !exp.After(now) {
		return nil
	}
	kind := "voice"
	if strings.Contains(c.Offer.SDP, "m=video") {
		kind = "video"
	}
	ref, _ := json.Marshal(callRef{Room: evt.RoomID.String(), CallID: c.CallID, Version: string(c.Version)})
	return &adapter.Request{Key: "call:" + c.CallID, Kind: model.RequestKindCall, FromID: evt.Sender.String(), ChatID: evt.RoomID.String(),
		ChatKind: chatKind, CallKind: kind, PlatformRef: ref, CreatedAt: created, ExpiresAt: &exp}
}

func (acc *account) onCallInvite(_ context.Context, evt *event.Event) {
	if evt.Sender == acc.selfID() {
		return
	}
	if r := callRequest(evt, evt.Content.AsCallInvite(), acc.kindOf(evt.RoomID), time.Now()); r != nil {
		acc.rep.Events(adapter.Event{Kind: adapter.EvRequest, Request: r})
	}
}

// onCallEnd resolves a ringing call: answered or rejected by the own user elsewhere, else ended.
func (acc *account) onCallEnd(_ context.Context, evt *event.Event) {
	if r := callEnd(evt, acc.selfID()); r != nil {
		acc.rep.Events(adapter.Event{Kind: adapter.EvRequest, Request: r})
	}
}

func callEnd(evt *event.Event, self id.UserID) *adapter.Request {
	var callID string
	switch evt.Type {
	case event.CallHangup:
		callID = evt.Content.AsCallHangup().CallID
	case event.CallReject:
		callID = evt.Content.AsCallReject().CallID
	case event.CallAnswer:
		callID = evt.Content.AsCallAnswer().CallID
	}
	if callID == "" {
		return nil
	}
	state := model.RequestExpired
	if evt.Sender == self {
		if evt.Type == event.CallAnswer {
			state = model.RequestAccepted
		} else {
			state = model.RequestRejected
		}
	}
	return &adapter.Request{Key: "call:" + callID, Kind: model.RequestKindCall, State: state}
}

// AnswerRequest joins or leaves invited rooms and rejects calls (m.call.reject for VoIP v1,
// m.call.hangup for legacy v0 calls).
func (a *Adapter) AnswerRequest(ctx context.Context, accountID string, ans adapter.RequestAnswer) error {
	_, cli, err := a.online(accountID)
	if err != nil {
		return err
	}
	switch ans.Kind {
	case model.RequestKindChatInvite:
		room := id.RoomID(ans.ChatID)
		if room == "" {
			return adapter.Errorf(adapter.ErrInvalidInput, "invite has no room")
		}
		if ans.Action == model.ActionAccept {
			if _, err := cli.JoinRoomByID(ctx, room); err != nil {
				return mapErr("join room", err)
			}
			return nil
		}
		if _, err := cli.LeaveRoom(ctx, room, &mautrix.ReqLeave{Reason: ans.Reason}); err != nil {
			return mapErr("decline invite", err)
		}
		return nil
	case model.RequestKindCall:
		if ans.Action != model.ActionReject {
			return adapter.Errorf(adapter.ErrUnsupported, "calls can only be rejected")
		}
		var ref callRef
		if err := json.Unmarshal(ans.PlatformRef, &ref); err != nil || ref.CallID == "" || ref.Room == "" {
			return adapter.Errorf(adapter.ErrInvalidInput, "call reference is missing")
		}
		evType, content := rejectContent(ref, string(cli.DeviceID))
		if _, err := cli.SendMessageEvent(ctx, id.RoomID(ref.Room), evType, content); err != nil {
			return mapErr("reject call", err)
		}
		return nil
	}
	return adapter.Errorf(adapter.ErrUnsupported, "%s requests are not supported on Matrix", ans.Kind)
}

func rejectContent(ref callRef, partyID string) (event.Type, any) {
	base := event.BaseCallEventContent{CallID: ref.CallID, PartyID: partyID, Version: event.CallVersion(ref.Version)}
	if ref.Version == "" || ref.Version == "0" {
		return event.CallHangup, &event.CallHangupEventContent{BaseCallEventContent: base, Reason: event.CallHangupUserHangup}
	}
	return event.CallReject, &event.CallRejectEventContent{BaseCallEventContent: base}
}
