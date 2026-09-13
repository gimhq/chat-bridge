package matrix

import (
	"context"
	"encoding/json"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/matrixcontent"
	"gimhq/chat-bridge/internal/model"
)

// remoteRef is the shared attachment reference (URL plus encryption info).
type remoteRef = matrixcontent.RemoteRef

func (acc *account) chatHint(room id.RoomID) *model.Chat {
	return &model.Chat{ID: room.String(), Kind: acc.kindOf(room)}
}

func msTime(ts int64) time.Time { return time.UnixMilli(ts).UTC() }

func (acc *account) onMessage(_ context.Context, evt *event.Event) {
	self := acc.selfID()
	c := evt.Content.AsMessage()
	if c == nil {
		return
	}
	at := msTime(evt.Timestamp)
	rel := c.RelatesTo
	// Edits arrive as new events that replace an earlier one.
	if rel != nil && rel.Type == event.RelReplace && c.NewContent != nil {
		content := convertContent(c.NewContent, evt.Type, evt.ID.String())
		acc.rep.Events(adapter.Event{Kind: adapter.EvMessageUpdate, ChatID: evt.RoomID.String(), MessageID: rel.EventID.String(), Content: &content, At: at})
		return
	}
	content := convertContent(c, evt.Type, evt.ID.String())
	m := model.Message{
		ID: evt.ID.String(), ChatID: evt.RoomID.String(), Sender: model.Sender{ID: evt.Sender.String()},
		FromMe: evt.Sender == self, Timestamp: at, Content: content,
	}
	if rel != nil {
		if rel.InReplyTo != nil {
			m.ReplyTo = rel.InReplyTo.EventID.String()
		}
		if rel.Type == event.RelThread {
			m.ThreadID = rel.EventID.String()
		}
	}
	if c.Mentions != nil {
		for _, u := range c.Mentions.UserIDs {
			m.Mentions = append(m.Mentions, u.String())
		}
	}
	raw, _ := json.Marshal(evt)
	// Everything before the first completed /sync is the initial timeline, not live traffic.
	acc.rep.Events(adapter.Event{Kind: adapter.EvMessage, Message: &m, Chat: acc.chatHint(evt.RoomID), Sender: userContact(evt.Sender, ""), Raw: raw, Backfill: !acc.isConnected()})
}

// convertContent maps Matrix content through the shared converter.
func convertContent(c *event.MessageEventContent, typ event.Type, mediaID string) model.Content {
	return matrixcontent.Convert(c, typ, mediaID)
}

func (acc *account) onReaction(_ context.Context, evt *event.Event) {
	r := evt.Content.AsReaction()
	if r == nil || r.RelatesTo.Type != event.RelAnnotation {
		return
	}
	acc.mu.Lock()
	acc.reactions[evt.RoomID.String()+"|"+evt.ID.String()] = r.RelatesTo.EventID // remember for redactions
	acc.mu.Unlock()
	acc.rep.Events(adapter.Event{Kind: adapter.EvReaction, ChatID: evt.RoomID.String(), MessageID: r.RelatesTo.EventID.String(),
		UserID: evt.Sender.String(), Emoji: r.RelatesTo.Key, At: msTime(evt.Timestamp)})
}

func (acc *account) onRedaction(_ context.Context, evt *event.Event) {
	acc.mu.Lock()
	target, wasReaction := acc.reactions[evt.RoomID.String()+"|"+evt.Redacts.String()]
	delete(acc.reactions, evt.RoomID.String()+"|"+evt.Redacts.String())
	acc.mu.Unlock()
	if wasReaction {
		acc.rep.Events(adapter.Event{Kind: adapter.EvReaction, ChatID: evt.RoomID.String(), MessageID: target.String(), UserID: evt.Sender.String(), Removed: true, At: msTime(evt.Timestamp)})
		return
	}
	acc.rep.Events(adapter.Event{Kind: adapter.EvMessageDelete, ChatID: evt.RoomID.String(), MessageID: evt.Redacts.String(), At: msTime(evt.Timestamp)})
}

func (acc *account) onMember(_ context.Context, evt *event.Event) {
	m := evt.Content.AsMember()
	if m == nil || evt.StateKey == nil {
		return
	}
	user := id.UserID(*evt.StateKey)
	if m.IsDirect {
		acc.mu.Lock()
		acc.roomKind[evt.RoomID] = model.ChatDirect
		acc.mu.Unlock()
	}
	ct := userContact(user, m.Displayname)
	left := m.Membership == event.MembershipLeave || m.Membership == event.MembershipBan
	evs := []adapter.Event{
		{Kind: adapter.EvContact, Contact: ct},
		{Kind: adapter.EvMember, Member: &adapter.Member{ChatID: evt.RoomID.String(), UserID: user.String(), ChatName: m.Displayname, Left: left}},
	}
	// A direct chat is named after the other member.
	if acc.kindOf(evt.RoomID) == model.ChatDirect && user != acc.selfID() && m.Displayname != "" && !left {
		evs = append(evs, adapter.Event{Kind: adapter.EvChat, Chat: &model.Chat{ID: evt.RoomID.String(), Kind: model.ChatDirect, Name: m.Displayname}})
	}
	if evt.Unsigned.PrevContent != nil {
		prev := evt.Unsigned.PrevContent.AsMember()
		kind := ""
		switch {
		case prev != nil && prev.Membership == event.MembershipJoin && left:
			kind = "member_left"
		case m.Membership == event.MembershipJoin && (prev == nil || prev.Membership != event.MembershipJoin):
			kind = "member_joined"
		}
		if kind != "" {
			msg := model.Message{ID: evt.ID.String(), ChatID: evt.RoomID.String(), Sender: model.Sender{ID: evt.Sender.String()}, Timestamp: msTime(evt.Timestamp),
				Content: model.Content{Type: model.ContentSystem, System: &model.System{Kind: kind, Actor: evt.Sender.String(), Targets: []string{user.String()}}}}
			evs = append(evs, adapter.Event{Kind: adapter.EvMessage, Message: &msg, Chat: acc.chatHint(evt.RoomID)})
		}
	}
	acc.rep.Events(evs...)
}

func (acc *account) onRoomName(_ context.Context, evt *event.Event) {
	n := evt.Content.AsRoomName()
	if n == nil {
		return
	}
	acc.rep.Events(adapter.Event{Kind: adapter.EvChat, Chat: &model.Chat{ID: evt.RoomID.String(), Kind: acc.kindOf(evt.RoomID), Name: n.Name}})
}

func (acc *account) onTyping(_ context.Context, evt *event.Event) {
	t := evt.Content.AsTyping()
	if t == nil {
		return
	}
	var evs []adapter.Event
	for _, u := range t.UserIDs {
		if u == acc.selfID() {
			continue
		}
		evs = append(evs, adapter.Event{Kind: adapter.EvTyping, ChatID: evt.RoomID.String(), UserID: u.String(), State: "typing"})
	}
	acc.rep.Events(evs...)
}

func (acc *account) onReceipt(_ context.Context, evt *event.Event) {
	rc := evt.Content.AsReceipt()
	if rc == nil {
		return
	}
	var evs []adapter.Event
	for eventID, byType := range *rc {
		for _, user := range []map[id.UserID]event.ReadReceipt{byType[event.ReceiptTypeRead]} {
			for u, r := range user {
				if u == acc.selfID() {
					continue
				}
				evs = append(evs, adapter.Event{Kind: adapter.EvReceipt, ChatID: evt.RoomID.String(), MessageIDs: []string{eventID.String()},
					UserID: u.String(), Receipt: "read", At: r.Timestamp.UTC()})
			}
		}
	}
	acc.rep.Events(evs...)
}

func (acc *account) onPresence(_ context.Context, evt *event.Event) {
	p := evt.Content.AsPresence()
	if p == nil {
		return
	}
	ev := adapter.Event{Kind: adapter.EvPresence, UserID: evt.Sender.String(), State: "online"}
	if p.Presence != event.PresenceOnline {
		ev.State = "offline"
		if p.LastActiveAgo > 0 {
			ls := time.Now().Add(-time.Duration(p.LastActiveAgo) * time.Millisecond).UTC()
			ev.LastSeen = &ls
		}
	}
	acc.rep.Events(ev)
}

func (acc *account) isConnected() bool {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	return acc.connected
}

func (acc *account) selfID() id.UserID {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if acc.cli == nil {
		return ""
	}
	return acc.cli.UserID
}
