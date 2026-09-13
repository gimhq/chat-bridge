package whatsapp

import (
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

func (acc *account) handleEvent(evt any) {
	switch e := evt.(type) {
	case *events.Message:
		acc.rep.Events(acc.convertMessage(e)...)
	case *events.Receipt:
		acc.rep.Events(acc.convertReceipt(e)...)
	case *events.ChatPresence:
		state := "paused"
		if e.State == types.ChatPresenceComposing {
			state = "typing"
		}
		acc.rep.Events(adapter.Event{Kind: adapter.EvTyping, ChatID: chatOf(e.MessageSource).String(), UserID: pn(e.Sender, e.SenderAlt).String(), State: state})
	case *events.Presence:
		ev := adapter.Event{Kind: adapter.EvPresence, UserID: e.From.ToNonAD().String(), State: "online"}
		if e.Unavailable {
			ev.State = "offline"
			if !e.LastSeen.IsZero() {
				ls := e.LastSeen.UTC()
				ev.LastSeen = &ls
			}
		}
		acc.rep.Events(ev)
	case *events.Contact:
		jid := e.JID.ToNonAD()
		acc.rep.Events(adapter.Event{Kind: adapter.EvContact, Contact: &model.Contact{
			ID: jid.String(), Handle: phoneOf(jid), Phone: phoneOf(jid), IsContact: true,
			Names: model.Names{Alias: e.Action.GetFullName(), First: e.Action.GetFirstName()},
		}})
	case *events.PushName:
		jid := pn(e.JID, e.JIDAlt)
		acc.rep.Events(adapter.Event{Kind: adapter.EvContact, Contact: &model.Contact{ID: jid.String(), Handle: phoneOf(jid), Phone: phoneOf(jid), Names: model.Names{Profile: e.NewPushName}}})
	case *events.GroupInfo:
		acc.rep.Events(acc.convertGroupInfo(e)...)
	case *events.JoinedGroup:
		acc.rep.Events(acc.groupEvents(&e.GroupInfo)...)
	case *events.Mute:
		acc.rep.Events(adapter.Event{Kind: adapter.EvChat, Chat: &model.Chat{ID: e.JID.ToNonAD().String(), Kind: chatKind(e.JID), Muted: e.Action.GetMuted()}})
	case *events.Archive:
		acc.rep.Events(adapter.Event{Kind: adapter.EvChat, Chat: &model.Chat{ID: e.JID.ToNonAD().String(), Kind: chatKind(e.JID), Archived: e.Action.GetArchived()}})
	case *events.HistorySync:
		acc.convertHistory(e)
	case *events.AppStateSyncComplete:
		// The address book arrives as app-state patches after the first login; re-read it as one batch.
		if e.Name == appstate.WAPatchCriticalUnblockLow {
			acc.syncContacts()
		}
	case *events.CallOffer:
		chat := e.From.ToNonAD()
		m := model.Message{ID: e.CallID, ChatID: chat.String(), Sender: model.Sender{ID: e.CallCreator.ToNonAD().String()}, Timestamp: e.Timestamp.UTC(),
			Content: model.Content{Type: model.ContentCall, Call: &model.Call{Kind: "voice", State: "ringing"}}}
		acc.rep.Events(adapter.Event{Kind: adapter.EvMessage, Message: &m, Chat: &model.Chat{ID: chat.String(), Kind: chatKind(chat)}})
	case *events.CallTerminate:
		state := "ended"
		if e.Reason == "timeout" {
			state = "missed"
		}
		acc.rep.Events(adapter.Event{Kind: adapter.EvMessageUpdate, ChatID: e.From.ToNonAD().String(), MessageID: e.CallID,
			Content: &model.Content{Type: model.ContentCall, Call: &model.Call{Kind: "voice", State: state}}, At: e.Timestamp.UTC()})
	case *events.Picture:
		acc.rep.Events(adapter.Event{Kind: adapter.EvPlatform, PlatformType: "picture", ChatID: e.JID.ToNonAD().String(), UserID: e.Author.ToNonAD().String(),
			Raw: []byte(fmt.Sprintf(`{"picture_id":%q,"removed":%t}`, e.PictureID, e.Remove))})
	case *events.UndecryptableMessage:
		acc.rep.Events(adapter.Event{Kind: adapter.EvPlatform, PlatformType: "undecryptable", ChatID: chatOf(e.Info.MessageSource).String(),
			UserID: pn(e.Info.Sender, e.Info.SenderAlt).String(), MessageID: e.Info.ID,
			Raw: []byte(fmt.Sprintf(`{"unavailable":%t,"type":%q}`, e.IsUnavailable, e.UnavailableType))})
	case *events.PairSuccess:
		acc.rep.Log.Info("paired", "jid", e.ID.String(), "platform", e.Platform)
		step := model.LoginStep{Step: model.StepDone, Self: acc.selfContact()}
		if lf := acc.login.Current(); lf != nil {
			step.Flow = lf.Name
		}
		acc.login.Cancel()
		acc.rep.Step(step)
	case *events.PairError:
		msg := "pairing failed"
		if e.Error != nil {
			msg = e.Error.Error()
		}
		step := model.LoginStep{Step: model.StepFailed, Error: &model.Error{Code: "platform_error", Message: msg}}
		if lf := acc.login.Current(); lf != nil {
			step.Flow = lf.Name
		}
		acc.login.Cancel()
		acc.rep.Step(step)
	case *events.Connected:
		if acc.cli.Store.ID != nil {
			acc.rep.Log.Info("connected", "jid", acc.cli.Store.ID.String())
			acc.setStatus(model.StatusConnected, nil)
		}
	case *events.Disconnected:
		acc.rep.Log.Warn("disconnected; whatsmeow will reconnect")
		if acc.cli.Store.ID != nil {
			acc.setStatus(model.StatusDisconnected, &model.Error{Code: "network", Message: "socket closed"})
		}
	case *events.LoggedOut:
		acc.rep.Log.Warn("logged out remotely", "reason", e.Reason.String())
		acc.resetClient(&model.Error{Code: "logged_out_remotely", Message: e.Reason.String()})
	case *events.StreamReplaced:
		acc.setStatus(model.StatusError, &model.Error{Code: "stream_replaced", Message: "another client connected with this device"})
	case *events.TemporaryBan:
		acc.setStatus(model.StatusError, &model.Error{Code: "banned", Message: e.String()})
	case *events.ClientOutdated:
		acc.setStatus(model.StatusError, &model.Error{Code: "adapter_error", Message: "whatsmeow is outdated; update the bridge"})
	}
}

func (acc *account) convertReceipt(e *events.Receipt) []adapter.Event {
	kind := ""
	switch e.Type {
	case types.ReceiptTypeDelivered:
		kind = "delivered"
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf, types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		kind = "read"
	default:
		return nil
	}
	return []adapter.Event{{
		Kind: adapter.EvReceipt, ChatID: chatOf(e.MessageSource).String(), UserID: pn(e.Sender, e.SenderAlt).String(),
		MessageIDs: e.MessageIDs, Receipt: kind, At: e.Timestamp.UTC(),
	}}
}

func memberID(p types.GroupParticipant) string {
	if !p.PhoneNumber.IsEmpty() {
		return p.PhoneNumber.ToNonAD().String()
	}
	return p.JID.ToNonAD().String()
}

func role(p types.GroupParticipant) string {
	switch {
	case p.IsSuperAdmin:
		return "owner"
	case p.IsAdmin:
		return "admin"
	default:
		return "member"
	}
}

// groupChat maps whatsmeow group metadata to a Chat with participants.
func groupChat(g *types.GroupInfo) model.Chat {
	ch := model.Chat{ID: g.JID.String(), Kind: model.ChatGroup, Name: g.GroupName.Name}
	if g.GroupEphemeral.IsEphemeral {
		ttl := int64(g.GroupEphemeral.DisappearingTimer)
		ch.EphemeralTTL = &ttl
	}
	for _, p := range g.Participants {
		ch.Participants = append(ch.Participants, model.Participant{ID: memberID(p), Role: role(p)})
	}
	return ch
}

func (acc *account) groupEvents(g *types.GroupInfo) []adapter.Event {
	ch := groupChat(g)
	out := []adapter.Event{{Kind: adapter.EvChat, Chat: &ch}}
	for _, p := range ch.Participants {
		out = append(out, adapter.Event{Kind: adapter.EvMember, Member: &adapter.Member{ChatID: ch.ID, UserID: p.ID, Role: p.Role}})
	}
	return out
}

func (acc *account) convertGroupInfo(e *events.GroupInfo) []adapter.Event {
	chat := e.JID.String()
	actor := ""
	if e.SenderPN != nil {
		actor = e.SenderPN.ToNonAD().String()
	} else if e.Sender != nil {
		actor = e.Sender.ToNonAD().String()
	}
	var out []adapter.Event
	ch := model.Chat{ID: chat, Kind: model.ChatGroup}
	changed := false
	if e.Name != nil {
		ch.Name, changed = e.Name.Name, true
		out = append(out, acc.groupSystem(e, "name_changed", actor, nil, e.Name.Name))
	}
	if e.Ephemeral != nil {
		ttl := int64(0)
		if e.Ephemeral.IsEphemeral {
			ttl = int64(e.Ephemeral.DisappearingTimer)
		}
		ch.EphemeralTTL, changed = &ttl, true
		out = append(out, acc.groupSystem(e, "ephemeral_changed", actor, nil, fmt.Sprint(ttl)))
	}
	if changed {
		out = append(out, adapter.Event{Kind: adapter.EvChat, Chat: &ch})
	}
	member := func(jids []types.JID, kind, r string, left bool) {
		if len(jids) == 0 {
			return
		}
		var targets []string
		for _, j := range jids {
			id := j.ToNonAD().String()
			targets = append(targets, id)
			out = append(out, adapter.Event{Kind: adapter.EvMember, Member: &adapter.Member{ChatID: chat, UserID: id, Role: r, Left: left}})
		}
		out = append(out, acc.groupSystem(e, kind, actor, targets, ""))
	}
	member(e.Join, "member_added", "member", false)
	member(e.Leave, "member_removed", "member", true)
	member(e.Promote, "role_changed", "admin", false)
	member(e.Demote, "role_changed", "member", false)
	return out
}

func (acc *account) groupSystem(e *events.GroupInfo, kind, actor string, targets []string, value string) adapter.Event {
	id := fmt.Sprintf("sys-%d-%s-%s", e.Timestamp.Unix(), kind, strings.Join(targets, ","))
	if len(id) > 120 {
		id = id[:120]
	}
	sender := actor
	if sender == "" {
		sender = e.JID.String()
	}
	m := model.Message{ID: id, ChatID: e.JID.String(), Sender: model.Sender{ID: sender}, Timestamp: e.Timestamp.UTC(),
		Content: model.Content{Type: model.ContentSystem, System: &model.System{Kind: kind, Actor: actor, Targets: targets, Value: value}}}
	return adapter.Event{Kind: adapter.EvMessage, Message: &m, Chat: &model.Chat{ID: e.JID.String(), Kind: model.ChatGroup}}
}

// convertHistory replays the initial history sync as ordinary events, chat by chat.
func (acc *account) convertHistory(e *events.HistorySync) {
	if e.Data == nil {
		return
	}
	acc.rep.Log.Info("history sync", "type", e.Data.GetSyncType().String(), "conversations", len(e.Data.GetConversations()))
	for _, conv := range e.Data.GetConversations() {
		jid, err := types.ParseJID(conv.GetID())
		if err != nil || jid.Server == types.BroadcastServer {
			continue
		}
		if jid.Server == types.HiddenUserServer && conv.GetPnJID() != "" {
			if p, err := types.ParseJID(conv.GetPnJID()); err == nil {
				jid = p
			}
		}
		ch := model.Chat{ID: jid.String(), Kind: chatKind(jid), Name: conv.GetName(), Archived: conv.GetArchived(), UnreadCount: int64(conv.GetUnreadCount())}
		if ch.Name == "" {
			ch.Name = conv.GetDisplayName()
		}
		if exp := conv.GetEphemeralExpiration(); exp > 0 {
			ttl := int64(exp)
			ch.EphemeralTTL = &ttl
		}
		batch := []adapter.Event{{Kind: adapter.EvChat, Chat: &ch}}
		for _, hm := range conv.GetMessages() {
			evt, err := acc.cli.ParseWebMessage(jid, hm.GetMessage())
			if err != nil {
				continue
			}
			batch = append(batch, base.MarkBackfill(acc.convertMessage(evt))...)
			if len(batch) >= 100 {
				acc.rep.Events(batch...)
				batch = batch[:0]
			}
		}
		acc.rep.Events(batch...)
	}
	if len(e.Data.GetPushnames()) > 0 {
		acc.syncContacts()
	}
}

// syncContacts re-reads whatsmeow's contact store and emits it in batches. Called after the
// app-state contact sync and after push-name history chunks, which arrive incrementally after
// the first login.
func (acc *account) syncContacts() {
	ctx, cancel := base.Timeout(time.Minute)
	defer cancel()
	all, err := acc.cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		acc.rep.Log.Warn("read contacts", "err", err)
		return
	}
	batch := make([]adapter.Event, 0, 100)
	for jid, ci := range all {
		if jid.Server != types.DefaultUserServer {
			continue
		}
		c := contactFromInfo(jid, ci)
		batch = append(batch, adapter.Event{Kind: adapter.EvContact, Contact: &c})
		if len(batch) == cap(batch) {
			acc.rep.Events(batch...)
			batch = batch[:0]
		}
	}
	acc.rep.Events(batch...)
	acc.rep.Log.Info("contacts synced", "count", len(all))
}

// contactFromInfo maps the device store's contact record.
func contactFromInfo(jid types.JID, ci types.ContactInfo) model.Contact {
	jid = jid.ToNonAD()
	return model.Contact{
		ID: jid.String(), Handle: phoneOf(jid), Phone: phoneOf(jid), IsContact: ci.FullName != "",
		Names: model.Names{Alias: ci.FullName, First: ci.FirstName, Profile: firstNonEmpty(ci.PushName, ci.BusinessName)},
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
