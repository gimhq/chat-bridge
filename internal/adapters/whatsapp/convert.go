package whatsapp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

// remoteRef is what the core stores so the adapter can download later.
type remoteRef struct {
	Type  string          `json:"type"`
	Proto json.RawMessage `json:"proto"`
}

// pn prefers the phone-number form of a JID over its LID.
func pn(j, alt types.JID) types.JID {
	if j.Server == types.HiddenUserServer && !alt.IsEmpty() {
		return alt.ToNonAD()
	}
	return j.ToNonAD()
}

func chatOf(src types.MessageSource) types.JID {
	if src.IsGroup {
		return src.Chat
	}
	return pn(src.Chat, src.RecipientAlt)
}

func chatKind(jid types.JID) string {
	switch jid.Server {
	case types.GroupServer:
		return model.ChatGroup
	case types.NewsletterServer, types.BroadcastServer:
		return model.ChatChannel
	default:
		return model.ChatDirect
	}
}

func phoneOf(jid types.JID) string {
	if jid.Server == types.DefaultUserServer && jid.User != "" {
		return "+" + jid.User
	}
	return ""
}

// unwrap strips ephemeral / view-once / caption wrappers.
func unwrap(m *waE2E.Message) (*waE2E.Message, bool) {
	viewOnce := false
	for i := 0; i < 4 && m != nil; i++ {
		switch {
		case m.EphemeralMessage != nil:
			m = m.EphemeralMessage.GetMessage()
		case m.ViewOnceMessage != nil:
			m, viewOnce = m.ViewOnceMessage.GetMessage(), true
		case m.ViewOnceMessageV2 != nil:
			m, viewOnce = m.ViewOnceMessageV2.GetMessage(), true
		case m.ViewOnceMessageV2Extension != nil:
			m, viewOnce = m.ViewOnceMessageV2Extension.GetMessage(), true
		case m.DocumentWithCaptionMessage != nil:
			m = m.DocumentWithCaptionMessage.GetMessage()
		case m.DeviceSentMessage != nil:
			m = m.DeviceSentMessage.GetMessage()
		default:
			return m, viewOnce
		}
	}
	return m, viewOnce
}

// contextOf returns the ContextInfo of whichever sub-message is set.
func contextOf(m *waE2E.Message) *waE2E.ContextInfo {
	if m == nil {
		return nil
	}
	var ctx *waE2E.ContextInfo
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}
		sub := v.Message()
		if f := sub.Descriptor().Fields().ByName("contextInfo"); f != nil && sub.Has(f) {
			if ci, ok := sub.Get(f).Message().Interface().(*waE2E.ContextInfo); ok {
				ctx = ci
				return false
			}
		}
		return true
	})
	return ctx
}

// platformType names the set field of a message for unsupported content.
func platformType(m *waE2E.Message) string {
	name := "unknown"
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if fd.Name() == "messageContextInfo" {
			return true
		}
		name = string(fd.Name())
		return false
	})
	return name
}

// convertMessage maps one inbound WhatsApp message to adapter events.
func (acc *account) convertMessage(e *events.Message) []adapter.Event {
	info := e.Info
	chat := chatOf(info.MessageSource)
	if chat == types.StatusBroadcastJID {
		return nil
	}
	sender := pn(info.Sender, info.SenderAlt)
	msg, viewOnce := unwrap(e.Message)
	if msg == nil {
		return nil
	}
	at := info.Timestamp.UTC()

	if r := msg.ReactionMessage; r != nil {
		return []adapter.Event{{
			Kind: adapter.EvReaction, ChatID: chat.String(), MessageID: r.GetKey().GetID(), UserID: sender.String(),
			Emoji: r.GetText(), Removed: r.GetText() == "", At: at,
		}}
	}
	if p := msg.ProtocolMessage; p != nil {
		switch p.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			return []adapter.Event{{Kind: adapter.EvMessageDelete, ChatID: chat.String(), MessageID: p.GetKey().GetID(), At: at}}
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			edited, _ := unwrap(p.GetEditedMessage())
			content, _ := convertContent(edited, info.ID)
			return []adapter.Event{{Kind: adapter.EvMessageUpdate, ChatID: chat.String(), MessageID: p.GetKey().GetID(), Content: &content, At: at}}
		case waE2E.ProtocolMessage_EPHEMERAL_SETTING:
			ttl := int64(p.GetEphemeralExpiration())
			return acc.systemMessage(info, chat, sender, model.System{Kind: "ephemeral_changed", Actor: sender.String(), Value: fmt.Sprint(ttl)}, &ttl)
		default:
			return nil
		}
	}

	content, ok := convertContent(msg, info.ID)
	if !ok {
		content = model.Content{Type: model.ContentUnsupported, Unsupported: &model.Unsupported{PlatformType: platformType(msg)}}
	}
	m := model.Message{
		ID: info.ID, ChatID: chat.String(), Sender: model.Sender{ID: sender.String()}, FromMe: info.IsFromMe, Timestamp: at, Content: content,
	}
	if ci := contextOf(msg); ci != nil {
		m.ReplyTo = ci.GetStanzaID()
		m.Forwarded = ci.GetIsForwarded()
		for _, j := range ci.GetMentionedJID() {
			if jid, err := types.ParseJID(j); err == nil {
				m.Mentions = append(m.Mentions, jid.ToNonAD().String())
			}
		}
		if exp := ci.GetExpiration(); exp > 0 {
			m.Ephemeral = &model.Ephemeral{ExpiresAt: at.Add(time.Duration(exp) * time.Second)}
		}
	}
	if viewOnce {
		if m.Ephemeral == nil {
			m.Ephemeral = &model.Ephemeral{ExpiresAt: at.Add(14 * 24 * time.Hour)}
		}
		m.Ephemeral.ViewOnce = true
	}
	ev := adapter.Event{Kind: adapter.EvMessage, Message: &m, Raw: protoRaw(e.Message)}
	ev.Chat = &model.Chat{ID: chat.String(), Kind: chatKind(chat)}
	if !info.IsGroup && !info.IsFromMe && info.PushName != "" {
		ev.Chat.Name = info.PushName
	}
	ev.Sender = &model.Contact{ID: sender.String(), Handle: phoneOf(sender), Phone: phoneOf(sender), Names: model.Names{Profile: info.PushName}}
	return []adapter.Event{ev}
}

// systemMessage synthesises a timeline notice and, when ttl is given, a chat update.
func (acc *account) systemMessage(info types.MessageInfo, chat, sender types.JID, sys model.System, ttl *int64) []adapter.Event {
	m := model.Message{
		ID: info.ID, ChatID: chat.String(), Sender: model.Sender{ID: sender.String()}, FromMe: info.IsFromMe, Timestamp: info.Timestamp.UTC(),
		Content: model.Content{Type: model.ContentSystem, System: &sys},
	}
	out := []adapter.Event{{Kind: adapter.EvMessage, Message: &m, Chat: &model.Chat{ID: chat.String(), Kind: chatKind(chat)}}}
	if ttl != nil {
		out = append(out, adapter.Event{Kind: adapter.EvChat, Chat: &model.Chat{ID: chat.String(), Kind: chatKind(chat), EphemeralTTL: ttl}})
	}
	return out
}

// convertContent maps a message body. ok is false when nothing matched.
func convertContent(m *waE2E.Message, mediaID string) (model.Content, bool) {
	switch {
	case m == nil:
		return model.Content{}, false
	case m.Conversation != nil:
		return model.Content{Type: model.ContentText, Text: m.GetConversation()}, true
	case m.ExtendedTextMessage != nil:
		return model.Content{Type: model.ContentText, Text: m.GetExtendedTextMessage().GetText()}, true
	case m.ImageMessage != nil:
		im := m.ImageMessage
		return mediaContent(model.ContentImage, im.GetCaption(), mediaID, im, im.GetMimetype(), "", int64(im.GetFileLength()), int(im.GetWidth()), int(im.GetHeight()), 0), true
	case m.VideoMessage != nil:
		vm := m.VideoMessage
		return mediaContent(model.ContentVideo, vm.GetCaption(), mediaID, vm, vm.GetMimetype(), "", int64(vm.GetFileLength()), int(vm.GetWidth()), int(vm.GetHeight()), int64(vm.GetSeconds())*1000), true
	case m.AudioMessage != nil:
		am := m.AudioMessage
		t := model.ContentAudio
		if am.GetPTT() {
			t = model.ContentVoice
		}
		return mediaContent(t, "", mediaID, am, am.GetMimetype(), "", int64(am.GetFileLength()), 0, 0, int64(am.GetSeconds())*1000), true
	case m.DocumentMessage != nil:
		dm := m.DocumentMessage
		return mediaContent(model.ContentFile, dm.GetCaption(), mediaID, dm, dm.GetMimetype(), dm.GetFileName(), int64(dm.GetFileLength()), 0, 0, 0), true
	case m.StickerMessage != nil:
		sm := m.StickerMessage
		return mediaContent(model.ContentSticker, "", mediaID, sm, sm.GetMimetype(), "", int64(sm.GetFileLength()), int(sm.GetWidth()), int(sm.GetHeight()), 0), true
	case m.LocationMessage != nil:
		l := m.LocationMessage
		return model.Content{Type: model.ContentLocation, Text: l.GetComment(), Location: &model.Location{
			Lat: l.GetDegreesLatitude(), Lon: l.GetDegreesLongitude(), Name: l.GetName(), Address: l.GetAddress()}}, true
	case m.LiveLocationMessage != nil:
		l := m.LiveLocationMessage
		return model.Content{Type: model.ContentLocation, Text: l.GetCaption(), Location: &model.Location{Lat: l.GetDegreesLatitude(), Lon: l.GetDegreesLongitude()}}, true
	case m.ContactMessage != nil:
		c := m.ContactMessage
		return model.Content{Type: model.ContentContact, Contacts: []model.ContactCard{vcardCard(c.GetDisplayName(), c.GetVcard())}}, true
	case m.ContactsArrayMessage != nil:
		var cards []model.ContactCard
		for _, c := range m.ContactsArrayMessage.GetContacts() {
			cards = append(cards, vcardCard(c.GetDisplayName(), c.GetVcard()))
		}
		return model.Content{Type: model.ContentContact, Contacts: cards}, true
	case m.PollCreationMessage != nil, m.PollCreationMessageV2 != nil, m.PollCreationMessageV3 != nil:
		p := m.PollCreationMessage
		if p == nil {
			p = m.PollCreationMessageV2
		}
		if p == nil {
			p = m.PollCreationMessageV3
		}
		poll := &model.Poll{Question: p.GetName(), Multi: p.GetSelectableOptionsCount() != 1}
		for _, o := range p.GetOptions() {
			poll.Options = append(poll.Options, model.PollOption{Text: o.GetOptionName()})
		}
		return model.Content{Type: model.ContentPoll, Text: p.GetName(), Poll: poll}, true
	case m.SenderKeyDistributionMessage != nil && len(m.ProtoReflect().GetUnknown()) == 0 && onlySenderKey(m):
		return model.Content{}, false
	}
	return model.Content{}, false
}

func onlySenderKey(m *waE2E.Message) bool {
	n := 0
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if fd.Name() != "messageContextInfo" {
			n++
		}
		return true
	})
	return n == 1
}

func mediaContent(t, caption, mediaID string, dl proto.Message, mime, fileName string, size int64, w, h int, durMs int64) model.Content {
	ref, _ := json.Marshal(remoteRef{Type: t, Proto: protoRaw(dl)})
	return model.Content{Type: t, Text: caption, Attachments: []model.Attachment{{
		MediaID: mediaID, Mime: mime, Size: size, FileName: fileName, Width: w, Height: h, DurationMs: durMs,
		State: model.MediaRemote, RemoteRef: ref,
	}}}
}

func protoRaw(m proto.Message) json.RawMessage {
	if m == nil {
		return nil
	}
	b, err := protojson.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

// vcardCard extracts a name and phone numbers from a vCard.
func vcardCard(name, vcard string) model.ContactCard {
	card := model.ContactCard{Name: name, VCard: vcard}
	for _, line := range strings.Split(vcard, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "TEL"):
			if i := strings.LastIndex(line, ":"); i >= 0 {
				card.Phones = append(card.Phones, strings.TrimSpace(line[i+1:]))
			}
		case strings.HasPrefix(line, "EMAIL"):
			if i := strings.LastIndex(line, ":"); i >= 0 {
				card.Emails = append(card.Emails, strings.TrimSpace(line[i+1:]))
			}
		case strings.HasPrefix(line, "FN:") && card.Name == "":
			card.Name = strings.TrimPrefix(line, "FN:")
		}
	}
	return card
}

// downloadable rebuilds the proto stored in a remote ref.
func downloadable(ref json.RawMessage) (whatsmeow.DownloadableMessage, string, error) {
	var r remoteRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return nil, "", adapter.Errorf(adapter.ErrInvalidInput, "bad remote ref")
	}
	var msg proto.Message
	switch r.Type {
	case model.ContentImage:
		msg = &waE2E.ImageMessage{}
	case model.ContentVideo:
		msg = &waE2E.VideoMessage{}
	case model.ContentAudio, model.ContentVoice:
		msg = &waE2E.AudioMessage{}
	case model.ContentFile:
		msg = &waE2E.DocumentMessage{}
	case model.ContentSticker:
		msg = &waE2E.StickerMessage{}
	default:
		return nil, "", adapter.Errorf(adapter.ErrInvalidInput, "unknown media type %q", r.Type)
	}
	if err := protojson.Unmarshal(r.Proto, msg); err != nil {
		return nil, "", adapter.Errorf(adapter.ErrInvalidInput, "bad remote ref proto: %v", err)
	}
	dl, ok := msg.(whatsmeow.DownloadableMessage)
	if !ok {
		return nil, "", adapter.Errorf(adapter.ErrInvalidInput, "not downloadable")
	}
	mime := ""
	if mm, ok := msg.(interface{ GetMimetype() string }); ok {
		mime = mm.GetMimetype()
	}
	return dl, mime, nil
}

// waMarkdown converts the common markdown subset to WhatsApp markup.
func waMarkdown(s string) string {
	s = strings.ReplaceAll(s, "**", "*")
	s = strings.ReplaceAll(s, "~~", "~")
	return s
}
