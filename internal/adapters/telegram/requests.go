package telegram

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

var _ adapter.RequestAnswerer = (*Adapter)(nil)

// callRingTimeout bounds how long an unanswered call stays pending when no discard arrives.
const callRingTimeout = 2 * time.Minute

type callRef struct {
	ID         int64 `json:"id"`
	AccessHash int64 `json:"access_hash"`
}

func callKey(id int64) string { return "call:" + strconv.FormatInt(id, 10) }

// wireRequestHandlers maps incoming calls and join requests onto requests.
func (acc *account) wireRequestHandlers(d tg.UpdateDispatcher) {
	d.OnPhoneCall(func(_ context.Context, e tg.Entities, u *tg.UpdatePhoneCall) error {
		if r := callRequest(u.PhoneCall, e, acc.selfID()); r != nil {
			acc.rep.Events(adapter.Event{Kind: adapter.EvRequest, Request: r})
		}
		return nil
	})
	d.OnPendingJoinRequests(func(ctx context.Context, e tg.Entities, u *tg.UpdatePendingJoinRequests) error {
		acc.applyEntities(ctx, e)
		acc.rep.Events(joinRequests(u, e)...)
		return nil
	})
}

// callRequest maps phone call states: a call ringing for us is a request; established means
// answered on another device; discarded means it ended.
func callRequest(pc tg.PhoneCallClass, e tg.Entities, self int64) *adapter.Request {
	switch c := pc.(type) {
	case *tg.PhoneCallRequested:
		if self != 0 && c.ParticipantID != self {
			return nil // a call we placed
		}
		ref, _ := json.Marshal(callRef{ID: c.ID, AccessHash: c.AccessHash})
		at := time.Unix(int64(c.Date), 0).UTC()
		exp := at.Add(callRingTimeout)
		kind := "voice"
		if c.Video {
			kind = "video"
		}
		from := userID(c.AdminID)
		r := &adapter.Request{Key: callKey(c.ID), Kind: model.RequestKindCall, FromID: from, ChatID: from, ChatKind: model.ChatDirect,
			CallKind: kind, PlatformRef: ref, CreatedAt: at, ExpiresAt: &exp}
		if u, ok := e.Users[c.AdminID]; ok {
			r.FromName = userContact(u).Names.Profile
		}
		return r
	case *tg.PhoneCall:
		return &adapter.Request{Key: callKey(c.ID), Kind: model.RequestKindCall, State: model.RequestAccepted}
	case *tg.PhoneCallDiscarded:
		return &adapter.Request{Key: callKey(c.ID), Kind: model.RequestKindCall, State: model.RequestExpired}
	}
	return nil
}

// joinRequests lists the recent requesters of an owned group or channel. CreatedAt stays zero so
// a repeated update never reopens a request that was already answered.
func joinRequests(u *tg.UpdatePendingJoinRequests, e tg.Entities) []adapter.Event {
	chat := peerID(u.Peer)
	if chat == "" {
		return nil
	}
	name, kind := "", model.ChatGroup
	switch p := u.Peer.(type) {
	case *tg.PeerChat:
		if c, ok := e.Chats[p.ChatID]; ok {
			name = c.Title
		}
	case *tg.PeerChannel:
		if c, ok := e.Channels[p.ChannelID]; ok {
			name = c.Title
			if c.Broadcast {
				kind = model.ChatChannel
			}
		}
	}
	evs := make([]adapter.Event, 0, len(u.RecentRequesters))
	for _, uid := range u.RecentRequesters {
		r := &adapter.Request{Key: "join:" + chat + ":" + userID(uid), Kind: model.RequestKindJoin, FromID: userID(uid), ChatID: chat, ChatName: name, ChatKind: kind}
		if usr, ok := e.Users[uid]; ok {
			r.FromName = userContact(usr).Names.Profile
		}
		evs = append(evs, adapter.Event{Kind: adapter.EvRequest, Request: r})
	}
	return evs
}

// AnswerRequest rejects calls (busy) and approves or dismisses join requests.
func (a *Adapter) AnswerRequest(ctx context.Context, accountID string, ans adapter.RequestAnswer) error {
	acc, api, err := a.online(accountID)
	if err != nil {
		return err
	}
	switch ans.Kind {
	case model.RequestKindCall:
		if ans.Action != model.ActionReject {
			return adapter.Errorf(adapter.ErrUnsupported, "calls can only be rejected")
		}
		var ref callRef
		if err := json.Unmarshal(ans.PlatformRef, &ref); err != nil || ref.ID == 0 {
			return adapter.Errorf(adapter.ErrInvalidInput, "call reference is missing")
		}
		_, err := api.PhoneDiscardCall(ctx, &tg.PhoneDiscardCallRequest{Peer: tg.InputPhoneCall{ID: ref.ID, AccessHash: ref.AccessHash},
			Reason: &tg.PhoneCallDiscardReasonBusy{}})
		return base.PlatformErr("reject call", err)
	case model.RequestKindJoin:
		chat, err := acc.resolvePeer(ctx, ans.ChatID)
		if err != nil {
			return err
		}
		p, err := acc.resolvePeer(ctx, ans.FromID)
		if err != nil {
			return err
		}
		user, ok := p.(peers.User)
		if !ok {
			return adapter.Errorf(adapter.ErrInvalidTarget, "%s is not a user", ans.FromID)
		}
		_, err = api.MessagesHideChatJoinRequest(ctx, &tg.MessagesHideChatJoinRequestRequest{Approved: ans.Action == model.ActionAccept,
			Peer: chat.InputPeer(), UserID: user.InputUser()})
		return base.PlatformErr("join request", err)
	}
	return adapter.Errorf(adapter.ErrUnsupported, "%s requests are not supported on Telegram", ans.Kind)
}
