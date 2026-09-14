package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

const maxSendMedia = 100 << 20

// SendMessage builds and sends one message.
func (a *Adapter) SendMessage(ctx context.Context, id string, req adapter.SendRequest) (model.Message, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return model.Message{}, err
	}
	if err := acc.requireLogin(); err != nil {
		return model.Message{}, err
	}
	to, err := parseJID(req.ChatID)
	if err != nil {
		return model.Message{}, err
	}
	msg, err := acc.buildMessage(ctx, req)
	if err != nil {
		return model.Message{}, err
	}
	resp, err := acc.cli.SendMessage(ctx, to, msg)
	if err != nil {
		return model.Message{}, base.PlatformErr("send", err)
	}
	chat := to
	if !resp.Chat.IsEmpty() {
		chat = resp.Chat
	}
	return model.Message{ID: resp.ID, ChatID: acc.canonID(chat).String(), Timestamp: resp.Timestamp.UTC(), Content: req.Content, Status: model.MsgSent}, nil
}

func (acc *account) buildMessage(ctx context.Context, req adapter.SendRequest) (*waE2E.Message, error) {
	ci := contextInfo(req)
	c := req.Content
	text := c.Text
	if c.Format == "markdown" {
		text = waMarkdown(text)
	}
	switch c.Type {
	case model.ContentText:
		if ci == nil {
			return &waE2E.Message{Conversation: proto.String(text)}, nil
		}
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text), ContextInfo: ci}}, nil
	case model.ContentLocation:
		l := c.Location
		return &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude: proto.Float64(l.Lat), DegreesLongitude: proto.Float64(l.Lon),
			Name: optStr(l.Name), Address: optStr(l.Address), Comment: optStr(text), ContextInfo: ci,
		}}, nil
	case model.ContentContact:
		card := c.Contacts[0]
		vcard := card.VCard
		if vcard == "" {
			var b strings.Builder
			b.WriteString("BEGIN:VCARD\nVERSION:3.0\nFN:" + card.Name + "\n")
			for _, p := range card.Phones {
				b.WriteString("TEL;type=CELL:" + p + "\n")
			}
			for _, e := range card.Emails {
				b.WriteString("EMAIL:" + e + "\n")
			}
			b.WriteString("END:VCARD")
			vcard = b.String()
		}
		return &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String(card.Name), Vcard: proto.String(vcard), ContextInfo: ci}}, nil
	case model.ContentImage, model.ContentVideo, model.ContentAudio, model.ContentVoice, model.ContentFile, model.ContentSticker:
		return acc.buildMedia(ctx, req, text, ci)
	}
	return nil, adapter.Errorf(adapter.ErrUnsupported, "cannot send %s", c.Type)
}

func (acc *account) buildMedia(ctx context.Context, req adapter.SendRequest, caption string, ci *waE2E.ContextInfo) (*waE2E.Message, error) {
	att := req.Content.Attachments[0]
	rc, meta, err := req.Media.Open(ctx, att.MediaID)
	if err != nil {
		return nil, adapter.Errorf(adapter.ErrInvalidInput, "open media: %v", err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxSendMedia+1))
	if err != nil {
		return nil, fmt.Errorf("read media: %w", err)
	}
	if len(data) > maxSendMedia {
		return nil, adapter.Errorf(adapter.ErrInvalidInput, "media larger than %d bytes", maxSendMedia)
	}
	mime := meta.Mime
	if mime == "" || mime == "application/octet-stream" {
		mime = http.DetectContentType(data)
	}
	kind := req.Content.Type
	up, err := acc.cli.Upload(ctx, data, uploadType(kind))
	if err != nil {
		return nil, base.PlatformErr("upload media", err)
	}
	cap := optStr(caption)
	base := func() (*string, *string, []byte, []byte, []byte, *uint64) {
		return proto.String(up.URL), proto.String(up.DirectPath), up.MediaKey, up.FileEncSHA256, up.FileSHA256, proto.Uint64(up.FileLength)
	}
	switch kind {
	case model.ContentImage:
		u, d, k, e, s, l := base()
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{URL: u, DirectPath: d, MediaKey: k, FileEncSHA256: e, FileSHA256: s, FileLength: l,
			Mimetype: proto.String(mime), Caption: cap, ContextInfo: ci}}, nil
	case model.ContentVideo:
		u, d, k, e, s, l := base()
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{URL: u, DirectPath: d, MediaKey: k, FileEncSHA256: e, FileSHA256: s, FileLength: l,
			Mimetype: proto.String(mime), Caption: cap, ContextInfo: ci}}, nil
	case model.ContentAudio, model.ContentVoice:
		u, d, k, e, s, l := base()
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{URL: u, DirectPath: d, MediaKey: k, FileEncSHA256: e, FileSHA256: s, FileLength: l,
			Mimetype: proto.String(mime), PTT: proto.Bool(kind == model.ContentVoice), ContextInfo: ci}}, nil
	case model.ContentSticker:
		u, d, k, e, s, l := base()
		return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{URL: u, DirectPath: d, MediaKey: k, FileEncSHA256: e, FileSHA256: s, FileLength: l,
			Mimetype: proto.String(mime), ContextInfo: ci}}, nil
	default:
		u, d, k, e, s, l := base()
		name := meta.FileName
		if name == "" {
			name = "file"
		}
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{URL: u, DirectPath: d, MediaKey: k, FileEncSHA256: e, FileSHA256: s, FileLength: l,
			Mimetype: proto.String(mime), FileName: proto.String(name), Title: proto.String(name), Caption: cap, ContextInfo: ci}}, nil
	}
}

func uploadType(kind string) whatsmeow.MediaType {
	switch kind {
	case model.ContentImage, model.ContentSticker:
		return whatsmeow.MediaImage
	case model.ContentVideo:
		return whatsmeow.MediaVideo
	case model.ContentAudio, model.ContentVoice:
		return whatsmeow.MediaAudio
	default:
		return whatsmeow.MediaDocument
	}
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}

// contextInfo builds reply and mention metadata, or nil when neither applies.
func contextInfo(req adapter.SendRequest) *waE2E.ContextInfo {
	if req.ReplyTo == "" && len(req.Mentions) == 0 {
		return nil
	}
	ci := &waE2E.ContextInfo{}
	if req.ReplyTo != "" {
		ci.StanzaID = proto.String(req.ReplyTo)
		quoted := &waE2E.Message{Conversation: proto.String("")}
		if req.ReplyTarget != nil {
			ci.Participant = proto.String(req.ReplyTarget.Sender.ID)
			quoted.Conversation = proto.String(req.ReplyTarget.Content.Text)
		}
		ci.QuotedMessage = quoted
	}
	for _, m := range req.Mentions {
		if jid, err := parseJID(m); err == nil {
			ci.MentionedJID = append(ci.MentionedJID, jid.String())
		}
	}
	return ci
}

// EditMessage replaces the text of a sent message.
func (a *Adapter) EditMessage(ctx context.Context, id, chatID, msgID string, c model.Content) (model.Message, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return model.Message{}, err
	}
	if err := acc.requireLogin(); err != nil {
		return model.Message{}, err
	}
	chat, err := parseJID(chatID)
	if err != nil {
		return model.Message{}, err
	}
	text := c.Text
	if c.Format == "markdown" {
		text = waMarkdown(text)
	}
	edit := acc.cli.BuildEdit(chat, msgID, &waE2E.Message{Conversation: proto.String(text)})
	if _, err := acc.cli.SendMessage(ctx, chat, edit); err != nil {
		return model.Message{}, base.PlatformErr("edit", err)
	}
	return model.Message{ID: msgID, ChatID: chatID, Content: c}, nil
}

// DeleteMessage revokes a message for everyone.
func (a *Adapter) DeleteMessage(ctx context.Context, id, chatID, msgID, senderID string) error {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return err
	}
	if err := acc.requireLogin(); err != nil {
		return err
	}
	chat, err := parseJID(chatID)
	if err != nil {
		return err
	}
	sender := types.EmptyJID
	if senderID != "" {
		s, err := parseJID(senderID)
		if err != nil {
			return err
		}
		if !acc.isOwn(s) {
			sender = s
		}
	}
	_, err = acc.cli.SendMessage(ctx, chat, acc.cli.BuildRevoke(chat, sender, msgID))
	return base.PlatformErr("revoke", err)
}

// React sets or clears the account's reaction.
func (a *Adapter) React(ctx context.Context, id, chatID, msgID, senderID, emoji string, remove bool) error {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return err
	}
	if err := acc.requireLogin(); err != nil {
		return err
	}
	chat, err := parseJID(chatID)
	if err != nil {
		return err
	}
	sender, err := parseJID(senderID)
	if err != nil {
		return err
	}
	if remove {
		emoji = ""
	}
	_, err = acc.cli.SendMessage(ctx, chat, acc.cli.BuildReaction(chat, sender, msgID, emoji))
	return base.PlatformErr("react", err)
}

// MarkRead sends read receipts.
func (a *Adapter) MarkRead(ctx context.Context, id, chatID string, messageIDs []string, senderID string) error {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return err
	}
	if err := acc.requireLogin(); err != nil {
		return err
	}
	chat, err := parseJID(chatID)
	if err != nil {
		return err
	}
	sender, err := parseJID(senderID)
	if err != nil {
		return err
	}
	return base.PlatformErr("mark read", acc.cli.MarkRead(ctx, messageIDs, time.Now(), chat, sender))
}

// Typing sends composing/paused.
func (a *Adapter) Typing(ctx context.Context, id, chatID, state string) error {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return err
	}
	if err := acc.requireLogin(); err != nil {
		return err
	}
	chat, err := parseJID(chatID)
	if err != nil {
		return err
	}
	st := types.ChatPresencePaused
	if state == "typing" {
		st = types.ChatPresenceComposing
	}
	return base.PlatformErr("typing", acc.cli.SendChatPresence(ctx, chat, st, types.ChatPresenceMediaText))
}

// ResolveChat maps a phone number or JID to a chat.
func (a *Adapter) ResolveChat(ctx context.Context, id, handle string) (model.ResolvedChat, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return model.ResolvedChat{}, err
	}
	if err := acc.requireLogin(); err != nil {
		return model.ResolvedChat{}, err
	}
	if strings.Contains(handle, "@") {
		jid, err := parseJID(handle)
		if err != nil {
			return model.ResolvedChat{}, err
		}
		r := model.ResolvedChat{ChatID: acc.canonID(jid).String(), Kind: chatKind(jid)}
		if r.Kind == model.ChatDirect {
			r.UserID = r.ChatID
		}
		return r, nil
	}
	digits := strings.TrimLeft(strings.TrimSpace(handle), "+")
	if strings.Trim(digits, "0123456789") != "" {
		return model.ResolvedChat{}, adapter.Errorf(adapter.ErrInvalidInput, "handle must be a phone number or jid")
	}
	resp, err := acc.cli.IsOnWhatsApp(ctx, []string{"+" + digits})
	if err != nil {
		return model.ResolvedChat{}, base.PlatformErr("lookup", err)
	}
	if len(resp) == 0 || !resp[0].IsIn {
		return model.ResolvedChat{}, adapter.Errorf(adapter.ErrInvalidTarget, "%s is not on WhatsApp", handle)
	}
	jid := acc.userJID(resp[0].JID, resp[0].PhoneNumber)
	return model.ResolvedChat{ChatID: jid.String(), Kind: model.ChatDirect, UserID: jid.String()}, nil
}

// GetChat fetches group metadata or a direct chat's contact name.
func (a *Adapter) GetChat(ctx context.Context, id, chatID string) (model.Chat, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return model.Chat{}, err
	}
	if err := acc.requireLogin(); err != nil {
		return model.Chat{}, err
	}
	jid, err := parseJID(chatID)
	if err != nil {
		return model.Chat{}, err
	}
	if jid.Server == types.GroupServer {
		g, err := acc.cli.GetGroupInfo(ctx, jid)
		if err != nil {
			return model.Chat{}, base.PlatformErr("group info", err)
		}
		return acc.groupChat(g), nil
	}
	if !isUser(jid) {
		return model.Chat{ID: jid.ToNonAD().String(), Kind: chatKind(jid)}, nil
	}
	c := acc.contactFor(ctx, jid)
	return model.Chat{ID: c.ID, Kind: model.ChatDirect, Name: firstNonEmpty(c.Names.Alias, c.Names.Profile, c.Names.First)}, nil
}

// ListContacts returns the synced address book.
func (a *Adapter) ListContacts(ctx context.Context, id string) ([]model.Contact, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return nil, err
	}
	out, err := acc.allContacts(ctx)
	if err != nil {
		return nil, base.PlatformErr("contacts", err)
	}
	return out, nil
}

// FetchMedia downloads an attachment described by a remote ref.
func (a *Adapter) FetchMedia(ctx context.Context, id, _ string, ref json.RawMessage, w io.Writer) (adapter.MediaMeta, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return adapter.MediaMeta{}, err
	}
	if err := acc.requireLogin(); err != nil {
		return adapter.MediaMeta{}, err
	}
	dl, mime, err := downloadable(ref)
	if err != nil {
		return adapter.MediaMeta{}, err
	}
	data, err := acc.cli.Download(ctx, dl)
	if err != nil {
		return adapter.MediaMeta{}, base.PlatformErr("download", err)
	}
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	if _, err := w.Write(data); err != nil {
		return adapter.MediaMeta{}, err
	}
	return adapter.MediaMeta{Mime: mime}, nil
}
