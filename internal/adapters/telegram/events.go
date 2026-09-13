package telegram

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

// remoteRef identifies a photo or document for a later download.
type remoteRef struct {
	Kind          string `json:"kind"` // photo|document
	ID            int64  `json:"id"`
	AccessHash    int64  `json:"access_hash"`
	FileReference string `json:"file_reference"`
	ThumbSize     string `json:"thumb_size,omitempty"`
	Mime          string `json:"mime,omitempty"`
}

func (acc *account) wireHandlers() {
	d := acc.dispatcher
	d.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		acc.applyEntities(ctx, e)
		acc.rep.Events(acc.convertMessage(u.Message, e, false)...)
		return nil
	})
	d.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		acc.applyEntities(ctx, e)
		acc.rep.Events(acc.convertMessage(u.Message, e, false)...)
		return nil
	})
	d.OnEditMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditMessage) error {
		acc.applyEntities(ctx, e)
		acc.rep.Events(acc.convertMessage(u.Message, e, true)...)
		return nil
	})
	d.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		acc.applyEntities(ctx, e)
		acc.rep.Events(acc.convertMessage(u.Message, e, true)...)
		return nil
	})
	d.OnDeleteMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteMessages) error {
		var evs []adapter.Event
		acc.mu.Lock()
		for _, id := range u.Messages {
			if chat, ok := acc.seen[id]; ok {
				evs = append(evs, adapter.Event{Kind: adapter.EvMessageDelete, ChatID: chat, MessageID: messageID(chat, id), At: time.Now().UTC()})
			}
		}
		acc.mu.Unlock()
		acc.rep.Events(evs...)
		return nil
	})
	d.OnDeleteChannelMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteChannelMessages) error {
		chat := channelID(u.ChannelID)
		evs := make([]adapter.Event, 0, len(u.Messages))
		for _, id := range u.Messages {
			evs = append(evs, adapter.Event{Kind: adapter.EvMessageDelete, ChatID: chat, MessageID: messageID(chat, id), At: time.Now().UTC()})
		}
		acc.rep.Events(evs...)
		return nil
	})
	d.OnUserTyping(func(_ context.Context, _ tg.Entities, u *tg.UpdateUserTyping) error {
		acc.rep.Events(adapter.Event{Kind: adapter.EvTyping, ChatID: userID(u.UserID), UserID: userID(u.UserID), State: typingState(u.Action)})
		return nil
	})
	d.OnChatUserTyping(func(_ context.Context, _ tg.Entities, u *tg.UpdateChatUserTyping) error {
		acc.rep.Events(adapter.Event{Kind: adapter.EvTyping, ChatID: chatIDOf(u.ChatID), UserID: peerID(u.FromID), State: typingState(u.Action)})
		return nil
	})
	d.OnChannelUserTyping(func(_ context.Context, _ tg.Entities, u *tg.UpdateChannelUserTyping) error {
		acc.rep.Events(adapter.Event{Kind: adapter.EvTyping, ChatID: channelID(u.ChannelID), UserID: peerID(u.FromID), State: typingState(u.Action)})
		return nil
	})
	d.OnUserStatus(func(_ context.Context, _ tg.Entities, u *tg.UpdateUserStatus) error {
		ev := adapter.Event{Kind: adapter.EvPresence, UserID: userID(u.UserID), State: "offline"}
		switch st := u.Status.(type) {
		case *tg.UserStatusOnline:
			ev.State = "online"
		case *tg.UserStatusOffline:
			ls := time.Unix(int64(st.WasOnline), 0).UTC()
			ev.LastSeen = &ls
		}
		acc.rep.Events(ev)
		return nil
	})
	d.OnReadHistoryOutbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadHistoryOutbox) error {
		chat := peerID(u.Peer)
		acc.rep.Events(adapter.Event{Kind: adapter.EvReceipt, ChatID: chat, MessageIDs: []string{messageID(chat, u.MaxID)}, UserID: chat, Receipt: "read", At: time.Now().UTC()})
		return nil
	})
	d.OnReadChannelOutbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadChannelOutbox) error {
		chat := channelID(u.ChannelID)
		acc.rep.Events(adapter.Event{Kind: adapter.EvReceipt, ChatID: chat, MessageIDs: []string{messageID(chat, u.MaxID)}, UserID: chat, Receipt: "read", At: time.Now().UTC()})
		return nil
	})
	d.OnMessageReactions(func(ctx context.Context, e tg.Entities, u *tg.UpdateMessageReactions) error {
		acc.applyEntities(ctx, e)
		chat := peerID(u.Peer)
		var evs []adapter.Event
		for _, r := range u.Reactions.RecentReactions {
			emoji, ok := r.Reaction.(*tg.ReactionEmoji)
			if !ok {
				continue
			}
			evs = append(evs, adapter.Event{Kind: adapter.EvReaction, ChatID: chat, MessageID: messageID(chat, u.MsgID), UserID: peerID(r.PeerID),
				Emoji: emoji.Emoticon, At: time.Unix(int64(r.Date), 0).UTC()})
		}
		acc.rep.Events(evs...)
		return nil
	})
	d.OnUserName(func(_ context.Context, _ tg.Entities, u *tg.UpdateUserName) error {
		c := &model.Contact{ID: userID(u.UserID), Names: model.Names{First: u.FirstName, Last: u.LastName, Profile: strings.TrimSpace(u.FirstName + " " + u.LastName)}}
		for _, un := range u.Usernames {
			if un.Active {
				c.Names.Username, c.Handle = un.Username, "@"+un.Username
				break
			}
		}
		acc.rep.Events(adapter.Event{Kind: adapter.EvContact, Contact: c})
		return nil
	})
}

func typingState(a tg.SendMessageActionClass) string {
	if _, cancel := a.(*tg.SendMessageCancelAction); cancel {
		return "paused"
	}
	return "typing"
}

// applyEntities feeds users/chats seen in updates to the peers manager.
func (acc *account) applyEntities(ctx context.Context, e tg.Entities) {
	users := make([]tg.UserClass, 0, len(e.Users))
	for _, u := range e.Users {
		users = append(users, u)
	}
	chats := make([]tg.ChatClass, 0, len(e.Chats)+len(e.Channels))
	for _, c := range e.Chats {
		chats = append(chats, c)
	}
	for _, c := range e.Channels {
		chats = append(chats, c)
	}
	acc.mu.Lock()
	pm := acc.peers
	acc.mu.Unlock()
	if pm != nil {
		_ = pm.Apply(ctx, users, chats)
	}
}

// convertMessage maps a message (new or edited) to adapter events with chat/sender hints.
func (acc *account) convertMessage(mc tg.MessageClass, e tg.Entities, edited bool) []adapter.Event {
	switch m := mc.(type) {
	case *tg.Message:
		return acc.convertPlain(m, e, edited)
	case *tg.MessageService:
		return acc.convertService(m, e)
	}
	return nil
}

func (acc *account) selfID() int64 {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if acc.self == nil {
		return 0
	}
	return acc.self.ID
}

// senderOf returns the author id: FromID when present, else the dialog peer (inbound DM) or self (outbound).
func (acc *account) senderOf(fromID, peer tg.PeerClass, out bool) string {
	if fromID != nil {
		return peerID(fromID)
	}
	if out {
		return userID(acc.selfID())
	}
	return peerID(peer)
}

func (acc *account) chatHint(peer tg.PeerClass, e tg.Entities) *model.Chat {
	ch := &model.Chat{ID: peerID(peer)}
	switch p := peer.(type) {
	case *tg.PeerUser:
		ch.Kind = model.ChatDirect
		if u, ok := e.Users[p.UserID]; ok {
			ch.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
		}
	case *tg.PeerChat:
		ch.Kind = model.ChatGroup
		if c, ok := e.Chats[p.ChatID]; ok {
			ch.Name = c.Title
		}
	case *tg.PeerChannel:
		ch.Kind = model.ChatGroup
		if c, ok := e.Channels[p.ChannelID]; ok {
			ch.Name = c.Title
			if c.Broadcast {
				ch.Kind = model.ChatChannel
			}
		}
	}
	return ch
}

func senderHint(sender string, e tg.Entities) *model.Contact {
	for _, u := range e.Users {
		if userID(u.ID) == sender {
			return userContact(u)
		}
	}
	return nil
}

func (acc *account) convertPlain(m *tg.Message, e tg.Entities, edited bool) []adapter.Event {
	chat := peerID(m.PeerID)
	if chat == "" {
		return nil
	}
	id := messageID(chat, m.ID)
	acc.remember(m.ID, chat)
	content := convertContent(m, id)
	at := time.Unix(int64(m.Date), 0).UTC()
	if edited {
		editedAt := at
		if m.EditDate > 0 {
			editedAt = time.Unix(int64(m.EditDate), 0).UTC()
		}
		return []adapter.Event{{Kind: adapter.EvMessageUpdate, ChatID: chat, MessageID: id, Content: &content, At: editedAt}}
	}
	sender := acc.senderOf(m.FromID, m.PeerID, m.Out)
	msg := model.Message{ID: id, ChatID: chat, Sender: model.Sender{ID: sender}, FromMe: m.Out, Timestamp: at, Content: content}
	if rh, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok && rh.ReplyToMsgID != 0 {
		msg.ReplyTo = messageID(chat, rh.ReplyToMsgID)
	}
	if m.FwdFrom.Date != 0 {
		msg.Forwarded = true
	}
	if m.TTLPeriod > 0 {
		msg.Ephemeral = &model.Ephemeral{ExpiresAt: at.Add(time.Duration(m.TTLPeriod) * time.Second)}
	}
	if m.EditDate > 0 {
		t := time.Unix(int64(m.EditDate), 0).UTC()
		msg.EditedAt = &t
	}
	for _, ent := range m.Entities {
		if mention, ok := ent.(*tg.MessageEntityMentionName); ok {
			msg.Mentions = append(msg.Mentions, userID(mention.UserID))
		}
	}
	raw, _ := json.Marshal(m)
	return []adapter.Event{{Kind: adapter.EvMessage, Message: &msg, Chat: acc.chatHint(m.PeerID, e), Sender: senderHint(sender, e), Raw: raw}}
}

func (acc *account) convertService(m *tg.MessageService, e tg.Entities) []adapter.Event {
	chat := peerID(m.PeerID)
	if chat == "" {
		return nil
	}
	sys := &model.System{Kind: "other"}
	actor := acc.senderOf(m.FromID, m.PeerID, m.Out)
	sys.Actor = actor
	var content model.Content
	switch a := m.Action.(type) {
	case *tg.MessageActionChatAddUser:
		sys.Kind = "member_added"
		for _, u := range a.Users {
			sys.Targets = append(sys.Targets, userID(u))
		}
	case *tg.MessageActionChatDeleteUser:
		sys.Kind, sys.Targets = "member_removed", []string{userID(a.UserID)}
	case *tg.MessageActionChatJoinedByLink, *tg.MessageActionChatJoinedByRequest:
		sys.Kind, sys.Targets = "member_joined", []string{actor}
	case *tg.MessageActionChatEditTitle:
		sys.Kind, sys.Value = "name_changed", a.Title
	case *tg.MessageActionChatEditPhoto, *tg.MessageActionChatDeletePhoto:
		sys.Kind = "avatar_changed"
	case *tg.MessageActionChatCreate, *tg.MessageActionChannelCreate:
		sys.Kind = "created"
	case *tg.MessageActionPinMessage:
		sys.Kind = "pinned"
	case *tg.MessageActionSetMessagesTTL:
		sys.Kind, sys.Value = "ephemeral_changed", strconv.Itoa(a.Period)
	case *tg.MessageActionPhoneCall:
		state := "ended"
		if _, missed := a.Reason.(*tg.PhoneCallDiscardReasonMissed); missed {
			state = "missed"
		}
		call := &model.Call{Kind: "voice", State: state, DurationS: int64(a.Duration)}
		if a.Video {
			call.Kind = "video"
		}
		content = model.Content{Type: model.ContentCall, Call: call}
	default:
		sys.Value = m.Action.TypeName()
	}
	if content.Type == "" {
		content = model.Content{Type: model.ContentSystem, System: sys}
	}
	id := messageID(chat, m.ID)
	acc.remember(m.ID, chat)
	msg := model.Message{ID: id, ChatID: chat, Sender: model.Sender{ID: actor}, FromMe: m.Out, Timestamp: time.Unix(int64(m.Date), 0).UTC(), Content: content}
	evs := []adapter.Event{{Kind: adapter.EvMessage, Message: &msg, Chat: acc.chatHint(m.PeerID, e), Sender: senderHint(actor, e)}}
	for _, t := range sys.Targets {
		evs = append(evs, adapter.Event{Kind: adapter.EvMember, Member: &adapter.Member{ChatID: chat, UserID: t, Left: sys.Kind == "member_removed"}})
	}
	if sys.Kind == "name_changed" {
		evs = append(evs, adapter.Event{Kind: adapter.EvChat, Chat: &model.Chat{ID: chat, Kind: model.ChatGroup, Name: sys.Value}})
	}
	return evs
}

// convertContent maps text and media of a message.
func convertContent(m *tg.Message, mediaID string) model.Content {
	text, formatted := entitiesToMarkdown(m.Message, m.Entities)
	format := ""
	if formatted {
		format = "markdown"
	}
	c := convertMedia(m, text, mediaID)
	c.Format = format
	return c
}

func convertMedia(m *tg.Message, text, mediaID string) model.Content {
	if m.Media == nil {
		return model.Content{Type: model.ContentText, Text: text}
	}
	switch md := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		p, ok := md.Photo.(*tg.Photo)
		if !ok {
			return model.Content{Type: model.ContentImage, Text: text}
		}
		att := model.Attachment{MediaID: mediaID, Mime: "image/jpeg", State: model.MediaRemote}
		thumb := ""
		for _, s := range p.Sizes {
			if ps, ok := s.(*tg.PhotoSize); ok {
				att.Width, att.Height, att.Size, thumb = ps.W, ps.H, int64(ps.Size), ps.Type
			}
			if ps, ok := s.(*tg.PhotoSizeProgressive); ok {
				att.Width, att.Height, thumb = ps.W, ps.H, ps.Type
				if n := len(ps.Sizes); n > 0 {
					att.Size = int64(ps.Sizes[n-1])
				}
			}
		}
		att.RemoteRef = rawJSON(remoteRef{Kind: "photo", ID: p.ID, AccessHash: p.AccessHash, FileReference: base64.StdEncoding.EncodeToString(p.FileReference), ThumbSize: thumb})
		return model.Content{Type: model.ContentImage, Text: text, Attachments: []model.Attachment{att}}
	case *tg.MessageMediaDocument:
		d, ok := md.Document.(*tg.Document)
		if !ok {
			return model.Content{Type: model.ContentFile, Text: text}
		}
		t := model.ContentFile
		att := model.Attachment{MediaID: mediaID, Mime: d.MimeType, Size: d.Size, State: model.MediaRemote}
		for _, a := range d.Attributes {
			switch attr := a.(type) {
			case *tg.DocumentAttributeFilename:
				att.FileName = attr.FileName
			case *tg.DocumentAttributeAudio:
				t = model.ContentAudio
				if attr.Voice {
					t = model.ContentVoice
				}
				att.DurationMs = int64(attr.Duration) * 1000
			case *tg.DocumentAttributeVideo:
				t = model.ContentVideo
				att.Width, att.Height, att.DurationMs = attr.W, attr.H, int64(attr.Duration*1000)
			case *tg.DocumentAttributeSticker:
				t = model.ContentSticker
			case *tg.DocumentAttributeImageSize:
				att.Width, att.Height = attr.W, attr.H
			case *tg.DocumentAttributeAnimated:
				t = model.ContentVideo
			}
		}
		if strings.HasPrefix(d.MimeType, "image/") && t == model.ContentFile {
			t = model.ContentImage
		}
		att.RemoteRef = rawJSON(remoteRef{Kind: "document", ID: d.ID, AccessHash: d.AccessHash, FileReference: base64.StdEncoding.EncodeToString(d.FileReference), Mime: d.MimeType})
		return model.Content{Type: t, Text: text, Attachments: []model.Attachment{att}}
	case *tg.MessageMediaGeo:
		if g, ok := md.Geo.(*tg.GeoPoint); ok {
			return model.Content{Type: model.ContentLocation, Text: text, Location: &model.Location{Lat: g.Lat, Lon: g.Long}}
		}
	case *tg.MessageMediaGeoLive:
		if g, ok := md.Geo.(*tg.GeoPoint); ok {
			until := time.Unix(int64(m.Date+md.Period), 0).UTC()
			return model.Content{Type: model.ContentLocation, Text: text, Location: &model.Location{Lat: g.Lat, Lon: g.Long, LiveUntil: &until}}
		}
	case *tg.MessageMediaVenue:
		if g, ok := md.Geo.(*tg.GeoPoint); ok {
			return model.Content{Type: model.ContentLocation, Text: text, Location: &model.Location{Lat: g.Lat, Lon: g.Long, Name: md.Title, Address: md.Address}}
		}
	case *tg.MessageMediaContact:
		return model.Content{Type: model.ContentContact, Text: text, Contacts: []model.ContactCard{{
			Name: strings.TrimSpace(md.FirstName + " " + md.LastName), Phones: []string{md.PhoneNumber}, VCard: md.Vcard}}}
	case *tg.MessageMediaPoll:
		poll := &model.Poll{Question: md.Poll.Question.Text, Multi: md.Poll.MultipleChoice, Closed: md.Poll.Closed}
		for _, a := range md.Poll.Answers {
			if pa, ok := a.(*tg.PollAnswer); ok {
				poll.Options = append(poll.Options, model.PollOption{Text: pa.Text.Text})
			}
		}
		return model.Content{Type: model.ContentPoll, Text: poll.Question, Poll: poll}
	case *tg.MessageMediaWebPage:
		return model.Content{Type: model.ContentText, Text: text}
	}
	return model.Content{Type: model.ContentUnsupported, Text: text, Unsupported: &model.Unsupported{PlatformType: m.Media.TypeName()}}
}

// loadDialogs primes peers with access hashes and emits the recent chats with their last message.
func (acc *account) loadDialogs(ctx context.Context) {
	cli := acc.client()
	if cli == nil {
		return
	}
	res, err := cli.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: 100})
	if err != nil {
		acc.rep.Log.Warn("load dialogs", "err", err)
		return
	}
	var dialogs []tg.DialogClass
	var messages []tg.MessageClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch r := res.(type) {
	case *tg.MessagesDialogs:
		dialogs, messages, users, chats = r.Dialogs, r.Messages, r.Users, r.Chats
	case *tg.MessagesDialogsSlice:
		dialogs, messages, users, chats = r.Dialogs, r.Messages, r.Users, r.Chats
	default:
		return
	}
	acc.mu.Lock()
	pm := acc.peers
	acc.mu.Unlock()
	_ = pm.Apply(ctx, users, chats)
	ents := tg.Entities{Users: map[int64]*tg.User{}, Chats: map[int64]*tg.Chat{}, Channels: map[int64]*tg.Channel{}}
	for _, u := range users {
		if uu, ok := u.(*tg.User); ok {
			ents.Users[uu.ID] = uu
		}
	}
	for _, c := range chats {
		switch cc := c.(type) {
		case *tg.Chat:
			ents.Chats[cc.ID] = cc
		case *tg.Channel:
			ents.Channels[cc.ID] = cc
		}
	}
	var evs []adapter.Event
	for _, d := range dialogs {
		dlg, ok := d.(*tg.Dialog)
		if !ok {
			continue
		}
		ch := acc.chatHint(dlg.Peer, ents)
		ch.UnreadCount = int64(dlg.UnreadCount)
		evs = append(evs, adapter.Event{Kind: adapter.EvChat, Chat: ch})
	}
	for _, m := range messages {
		evs = append(evs, base.MarkBackfill(acc.convertMessage(m, ents, false))...)
	}
	acc.rep.Events(evs...)
	acc.rep.Log.Info("dialogs loaded", "count", len(dialogs))
}
