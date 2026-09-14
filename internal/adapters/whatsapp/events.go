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
		evs := acc.convertMessage(e)
		if r := inviteRequest(e, acc.userJID(e.Info.Sender, e.Info.SenderAlt)); r != nil {
			evs = append(evs, adapter.Event{Kind: adapter.EvRequest, Request: r})
		}
		acc.emit(evs...)
	case *events.Receipt:
		acc.emit(acc.convertReceipt(e)...)
	case *events.ChatPresence:
		state := "paused"
		if e.State == types.ChatPresenceComposing {
			state = "typing"
		}
		acc.emit(adapter.Event{Kind: adapter.EvTyping, ChatID: acc.chatJID(e.MessageSource).String(), UserID: acc.userJID(e.Sender, e.SenderAlt).String(), State: state})
	case *events.Presence:
		ev := adapter.Event{Kind: adapter.EvPresence, UserID: acc.canonID(e.From).String(), State: "online"}
		if e.Unavailable {
			ev.State = "offline"
			if !e.LastSeen.IsZero() {
				ls := e.LastSeen.UTC()
				ev.LastSeen = &ls
			}
		}
		acc.emit(ev)
	case *events.Contact:
		ctx, cancel := base.Timeout(lookupTimeout)
		c := acc.contactFor(ctx, e.JID)
		cancel()
		c.IsContact = true
		c.Names.Alias = firstNonEmpty(e.Action.GetFullName(), c.Names.Alias)
		c.Names.First = firstNonEmpty(e.Action.GetFirstName(), c.Names.First)
		acc.emit(adapter.Event{Kind: adapter.EvContact, Contact: &c})
	case *events.PushName:
		phone := phoneOf(acc.phoneJID(e.JID, e.JIDAlt))
		acc.emit(adapter.Event{Kind: adapter.EvContact, Contact: &model.Contact{ID: acc.userJID(e.JID, e.JIDAlt).String(), Handle: phone, Phone: phone,
			Names: model.Names{Profile: e.NewPushName}}})
	case *events.GroupInfo:
		acc.emit(acc.convertGroupInfo(e)...)
	case *events.JoinedGroup:
		acc.emit(acc.groupEvents(&e.GroupInfo)...)
	case *events.Mute:
		acc.emit(adapter.Event{Kind: adapter.EvChat, Chat: &model.Chat{ID: acc.canonID(e.JID).String(), Kind: chatKind(e.JID), Muted: e.Action.GetMuted()}})
	case *events.Archive:
		acc.emit(adapter.Event{Kind: adapter.EvChat, Chat: &model.Chat{ID: acc.canonID(e.JID).String(), Kind: chatKind(e.JID), Archived: e.Action.GetArchived()}})
	case *events.HistorySync:
		acc.convertHistory(e)
	case *events.AppStateSyncComplete:
		// The address book arrives as app-state patches after the first login; re-read it as one batch.
		if e.Name == appstate.WAPatchCriticalUnblockLow {
			acc.syncContacts()
		}
	case *events.CallOffer:
		chat, creator := acc.callParties(e.BasicCallMeta)
		m := model.Message{ID: e.CallID, ChatID: chat.String(), Sender: model.Sender{ID: creator.String()}, Timestamp: e.Timestamp.UTC(),
			Content: model.Content{Type: model.ContentCall, Call: &model.Call{Kind: "voice", State: "ringing"}}}
		acc.emit(adapter.Event{Kind: adapter.EvMessage, Message: &m, Chat: &model.Chat{ID: chat.String(), Kind: chatKind(chat)}},
			adapter.Event{Kind: adapter.EvRequest, Request: callRequest(e, chat, creator)})
	case *events.CallTerminate:
		state := "ended"
		if e.Reason == "timeout" {
			state = "missed"
		}
		chat, _ := acc.callParties(e.BasicCallMeta)
		acc.emit(adapter.Event{Kind: adapter.EvMessageUpdate, ChatID: chat.String(), MessageID: e.CallID,
			Content: &model.Content{Type: model.ContentCall, Call: &model.Call{Kind: "voice", State: state}}, At: e.Timestamp.UTC()},
			adapter.Event{Kind: adapter.EvRequest, Request: callEnded(e.CallID)})
	case *events.Picture:
		acc.emit(adapter.Event{Kind: adapter.EvPlatform, PlatformType: "picture", ChatID: acc.canonID(e.JID).String(), UserID: acc.canonID(e.Author).String(),
			Raw: []byte(fmt.Sprintf(`{"picture_id":%q,"removed":%t}`, e.PictureID, e.Remove))})
	case *events.UndecryptableMessage:
		acc.emit(adapter.Event{Kind: adapter.EvPlatform, PlatformType: "undecryptable", ChatID: acc.chatJID(e.Info.MessageSource).String(),
			UserID: acc.userJID(e.Info.Sender, e.Info.SenderAlt).String(), MessageID: e.Info.ID,
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

// callParties returns the chat of a call and its caller.
func (acc *account) callParties(meta types.BasicCallMeta) (chat, creator types.JID) {
	caller := meta.CallCreator
	if caller.IsEmpty() {
		caller = meta.From
	}
	creator = acc.userJID(caller, meta.CallCreatorAlt)
	if meta.From.ToNonAD() == caller.ToNonAD() {
		return creator, creator
	}
	return acc.canonID(meta.From), creator
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
		Kind: adapter.EvReceipt, ChatID: acc.chatJID(e.MessageSource).String(), UserID: acc.userJID(e.Sender, e.SenderAlt).String(),
		MessageIDs: e.MessageIDs, Receipt: kind, At: e.Timestamp.UTC(),
	}}
}

// memberID prefers the participant's LID.
func (acc *account) memberID(p types.GroupParticipant) string {
	if lid := p.LID.ToNonAD(); !lid.IsEmpty() {
		if pn := p.PhoneNumber.ToNonAD(); !pn.IsEmpty() {
			acc.learn(pn, lid)
		}
		return lid.String()
	}
	return acc.userJID(p.JID, p.PhoneNumber).String()
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
func (acc *account) groupChat(g *types.GroupInfo) model.Chat {
	ch := model.Chat{ID: g.JID.String(), Kind: model.ChatGroup, Name: g.GroupName.Name}
	if g.GroupEphemeral.IsEphemeral {
		ttl := int64(g.GroupEphemeral.DisappearingTimer)
		ch.EphemeralTTL = &ttl
	}
	for _, p := range g.Participants {
		ch.Participants = append(ch.Participants, model.Participant{ID: acc.memberID(p), Role: role(p)})
	}
	return ch
}

func (acc *account) groupEvents(g *types.GroupInfo) []adapter.Event {
	ch := acc.groupChat(g)
	out := []adapter.Event{{Kind: adapter.EvChat, Chat: &ch}}
	for _, p := range ch.Participants {
		out = append(out, adapter.Event{Kind: adapter.EvMember, Member: &adapter.Member{ChatID: ch.ID, UserID: p.ID, Role: p.Role}})
	}
	return out
}

func (acc *account) convertGroupInfo(e *events.GroupInfo) []adapter.Event {
	chat := e.JID.String()
	actor := ""
	switch {
	case e.Sender != nil:
		alt := types.EmptyJID
		if e.SenderPN != nil {
			alt = *e.SenderPN
		}
		actor = acc.userJID(*e.Sender, alt).String()
	case e.SenderPN != nil:
		actor = acc.userJID(*e.SenderPN, types.EmptyJID).String()
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
			id := acc.canonID(j).String()
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
		chatID := jid
		if isUser(jid) {
			alt := types.EmptyJID
			for _, s := range []string{conv.GetLidJID(), conv.GetPnJID()} {
				if p, err := types.ParseJID(s); s != "" && err == nil && p.Server != jid.Server {
					alt = p
				}
			}
			chatID = acc.userJID(jid, alt)
		}
		ch := model.Chat{ID: chatID.String(), Kind: chatKind(jid), Name: conv.GetName(), Archived: conv.GetArchived(), UnreadCount: int64(conv.GetUnreadCount())}
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
				acc.emit(batch...)
				batch = batch[:0]
			}
		}
		acc.emit(batch...)
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
	all, err := acc.allContacts(ctx)
	if err != nil {
		acc.rep.Log.Warn("read contacts", "err", err)
		return
	}
	for start := 0; start < len(all); start += 100 {
		batch := make([]adapter.Event, 0, 100)
		for i := start; i < len(all) && i < start+100; i++ {
			batch = append(batch, adapter.Event{Kind: adapter.EvContact, Contact: &all[i]})
		}
		acc.emit(batch...)
	}
	acc.rep.Log.Info("contacts synced", "count", len(all))
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
