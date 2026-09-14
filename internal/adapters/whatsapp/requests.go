package whatsapp

import (
	"context"
	"encoding/json"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

var _ adapter.RequestAnswerer = (*Adapter)(nil)

// callRingTimeout bounds how long an unanswered offer stays pending when no terminate arrives.
const callRingTimeout = 2 * time.Minute

type callRef struct {
	Creator string `json:"creator"`
	CallID  string `json:"call_id"`
}

type inviteRef struct {
	Group      string `json:"group"`
	Inviter    string `json:"inviter"`
	Code       string `json:"code"`
	Expiration int64  `json:"expiration,omitempty"`
}

// callRequest turns an incoming call offer into a request; chat and creator are the resolved ids,
// the reference keeps the caller as WhatsApp sent it for RejectCall.
func callRequest(e *events.CallOffer, chat, creator types.JID) *adapter.Request {
	caller := e.CallCreator
	if caller.IsEmpty() {
		caller = e.From
	}
	ref, _ := json.Marshal(callRef{Creator: caller.ToNonAD().String(), CallID: e.CallID})
	at := e.Timestamp.UTC()
	exp := at.Add(callRingTimeout)
	return &adapter.Request{Key: "call:" + e.CallID, Kind: model.RequestKindCall, FromID: creator.String(), ChatID: chat.String(),
		ChatKind: chatKind(chat), CallKind: "voice", PlatformRef: ref, CreatedAt: at, ExpiresAt: &exp}
}

// callEnded resolves a call request when the caller hangs up or the ring times out.
func callEnded(callID string) *adapter.Request {
	return &adapter.Request{Key: "call:" + callID, Kind: model.RequestKindCall, State: model.RequestExpired}
}

// inviteRequest extracts a group invite from an inbound message; nil when the message is not one.
// from is the resolved sender id.
func inviteRequest(e *events.Message, from types.JID) *adapter.Request {
	if e.Info.IsFromMe {
		return nil
	}
	msg, _ := unwrap(e.Message)
	gi := msg.GetGroupInviteMessage()
	if gi == nil || gi.GetInviteCode() == "" || gi.GetGroupJID() == "" {
		return nil
	}
	inviter := e.Info.Sender.ToNonAD()
	ref, _ := json.Marshal(inviteRef{Group: gi.GetGroupJID(), Inviter: inviter.String(), Code: gi.GetInviteCode(), Expiration: gi.GetInviteExpiration()})
	r := &adapter.Request{Key: "invite:" + gi.GetGroupJID() + ":" + gi.GetInviteCode(), Kind: model.RequestKindChatInvite,
		FromID: from.String(), FromName: e.Info.PushName, ChatID: gi.GetGroupJID(),
		ChatName: gi.GetGroupName(), ChatKind: model.ChatGroup, Message: gi.GetCaption(), PlatformRef: ref, CreatedAt: e.Info.Timestamp.UTC()}
	if exp := gi.GetInviteExpiration(); exp > 0 {
		t := time.Unix(exp, 0).UTC()
		r.ExpiresAt = &t
	}
	return r
}

// AnswerRequest rejects calls and joins groups from invites. WhatsApp has no way to decline an
// invite, so rejecting one only dismisses it locally.
func (a *Adapter) AnswerRequest(ctx context.Context, id string, ans adapter.RequestAnswer) error {
	acc, err := a.loggedIn(id)
	if err != nil {
		return err
	}
	switch ans.Kind {
	case model.RequestKindCall:
		if ans.Action != model.ActionReject {
			return adapter.Errorf(adapter.ErrUnsupported, "calls can only be rejected")
		}
		var ref callRef
		if err := json.Unmarshal(ans.PlatformRef, &ref); err != nil || ref.CallID == "" {
			return adapter.Errorf(adapter.ErrInvalidInput, "call reference is missing")
		}
		creator, err := parseJID(ref.Creator)
		if err != nil {
			return err
		}
		return base.PlatformErr("reject call", acc.cli.RejectCall(ctx, creator, ref.CallID))
	case model.RequestKindChatInvite:
		if ans.Action == model.ActionReject {
			return nil
		}
		var ref inviteRef
		if err := json.Unmarshal(ans.PlatformRef, &ref); err != nil || ref.Code == "" {
			return adapter.Errorf(adapter.ErrInvalidInput, "invite reference is missing")
		}
		group, err := parseJID(ref.Group)
		if err != nil {
			return err
		}
		inviter, err := parseJID(ref.Inviter)
		if err != nil {
			return err
		}
		return base.PlatformErr("join group", acc.cli.JoinGroupWithInvite(ctx, group, inviter, ref.Code, ref.Expiration))
	}
	return adapter.Errorf(adapter.ErrUnsupported, "%s requests are not supported on WhatsApp", ans.Kind)
}
