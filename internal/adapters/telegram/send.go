package telegram

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

func (a *Adapter) online(accountID string) (*account, *tg.Client, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return nil, nil, err
	}
	cli := acc.client()
	if cli == nil || !acc.isOnline() {
		return nil, nil, adapter.Errorf(adapter.ErrNotConnected, "account is not logged in")
	}
	return acc, cli.API(), nil
}

// styled renders text either plain or, for format markdown, through Telegram HTML entities.
func (acc *account) styled(text, format string) []styling.StyledTextOption {
	if text == "" {
		return nil
	}
	if format != "markdown" {
		return []styling.StyledTextOption{styling.Plain(text)}
	}
	acc.mu.Lock()
	pm := acc.peers
	acc.mu.Unlock()
	resolver := func(id int64) (tg.InputUserClass, error) {
		u, err := pm.ResolveUserID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		return u.InputUser(), nil
	}
	return []styling.StyledTextOption{html.String(resolver, markdownToHTML(text))}
}

// SendMessage sends text, media, a location, or a contact card.
func (a *Adapter) SendMessage(ctx context.Context, accountID string, req adapter.SendRequest) (model.Message, error) {
	acc, api, err := a.online(accountID)
	if err != nil {
		return model.Message{}, err
	}
	peer, err := acc.resolvePeer(ctx, req.ChatID)
	if err != nil {
		return model.Message{}, base.PlatformErr("resolve", err)
	}
	b := message.NewSender(api).To(peer.InputPeer()).CloneBuilder()
	if req.ReplyTo != "" {
		if _, id, err := splitMessageID(req.ReplyTo); err == nil {
			b = b.Reply(id)
		}
	}
	c := req.Content
	var upd tg.UpdatesClass
	switch c.Type {
	case model.ContentText:
		upd, err = b.StyledText(ctx, acc.styled(c.Text, c.Format)...)
	case model.ContentLocation:
		l := c.Location
		if l.Name != "" || l.Address != "" {
			upd, err = b.Media(ctx, message.Venue(l.Lat, l.Lon, 0, l.Name, l.Address, acc.styled(c.Text, c.Format)...))
		} else {
			upd, err = b.Media(ctx, message.GeoPoint(l.Lat, l.Lon, 0, acc.styled(c.Text, c.Format)...))
		}
	case model.ContentContact:
		card := c.Contacts[0]
		first, last, _ := strings.Cut(card.Name, " ")
		phone := ""
		if len(card.Phones) > 0 {
			phone = card.Phones[0]
		}
		upd, err = b.Media(ctx, message.Contact(tg.InputMediaContact{PhoneNumber: phone, FirstName: first, LastName: last, Vcard: card.VCard}))
	case model.ContentImage, model.ContentVideo, model.ContentAudio, model.ContentVoice, model.ContentFile, model.ContentSticker:
		upd, err = acc.sendMedia(ctx, api, b, req)
	default:
		return model.Message{}, adapter.Errorf(adapter.ErrUnsupported, "cannot send %s", c.Type)
	}
	sent, err := unpack.Message(upd, err)
	if err != nil {
		return model.Message{}, base.PlatformErr("send", err)
	}
	acc.remember(sent.ID, req.ChatID)
	return model.Message{ID: messageID(req.ChatID, sent.ID), ChatID: req.ChatID, Timestamp: unixTime(sent.Date), Content: req.Content, Status: model.MsgSent}, nil
}

func (acc *account) sendMedia(ctx context.Context, api *tg.Client, b *message.Builder, req adapter.SendRequest) (tg.UpdatesClass, error) {
	att := req.Content.Attachments[0]
	rc, meta, err := req.Media.Open(ctx, att.MediaID)
	if err != nil {
		return nil, adapter.Errorf(adapter.ErrInvalidInput, "open media: %v", err)
	}
	defer func() { _ = rc.Close() }()
	name := meta.FileName
	if name == "" {
		name = req.Content.Type
	}
	f, err := uploader.NewUploader(api).FromReader(ctx, name, rc)
	if err != nil {
		return nil, base.PlatformErr("upload", err)
	}
	cap := acc.styled(req.Content.Text, req.Content.Format)
	switch req.Content.Type {
	case model.ContentImage:
		return b.Media(ctx, message.UploadedPhoto(f, cap...))
	case model.ContentVideo:
		return b.Media(ctx, message.UploadedDocument(f, cap...).MIME(meta.Mime).Filename(name).Video().Resolution(meta.Width, meta.Height).DurationSeconds(int(meta.DurationMs/1000)))
	case model.ContentAudio:
		return b.Media(ctx, message.UploadedDocument(f, cap...).MIME(meta.Mime).Filename(name).Audio().DurationSeconds(int(meta.DurationMs/1000)))
	case model.ContentVoice:
		return b.Media(ctx, message.UploadedDocument(f).MIME(meta.Mime).Voice().DurationSeconds(int(meta.DurationMs/1000)))
	case model.ContentSticker:
		return b.Media(ctx, message.UploadedDocument(f).MIME(meta.Mime).Filename(name))
	default:
		return b.Media(ctx, message.File(f, cap...).MIME(meta.Mime).Filename(name))
	}
}

// EditMessage edits the text of a sent message.
func (a *Adapter) EditMessage(ctx context.Context, accountID, chatID, msgID string, c model.Content) (model.Message, error) {
	acc, api, err := a.online(accountID)
	if err != nil {
		return model.Message{}, err
	}
	peer, err := acc.resolvePeer(ctx, chatID)
	if err != nil {
		return model.Message{}, base.PlatformErr("resolve", err)
	}
	_, id, err := splitMessageID(msgID)
	if err != nil {
		return model.Message{}, err
	}
	if _, err := message.NewSender(api).To(peer.InputPeer()).Edit(id).StyledText(ctx, acc.styled(c.Text, c.Format)...); err != nil {
		return model.Message{}, base.PlatformErr("edit", err)
	}
	return model.Message{ID: msgID, ChatID: chatID, Content: c}, nil
}

// DeleteMessage deletes for everyone.
func (a *Adapter) DeleteMessage(ctx context.Context, accountID, chatID, msgID, _ string) error {
	acc, api, err := a.online(accountID)
	if err != nil {
		return err
	}
	peer, err := acc.resolvePeer(ctx, chatID)
	if err != nil {
		return base.PlatformErr("resolve", err)
	}
	_, id, err := splitMessageID(msgID)
	if err != nil {
		return err
	}
	_, err = message.NewSender(api).To(peer.InputPeer()).Revoke().Messages(ctx, id)
	return base.PlatformErr("delete", err)
}

// React sets or clears our reaction.
func (a *Adapter) React(ctx context.Context, accountID, chatID, msgID, _, emoji string, remove bool) error {
	acc, api, err := a.online(accountID)
	if err != nil {
		return err
	}
	peer, err := acc.resolvePeer(ctx, chatID)
	if err != nil {
		return base.PlatformErr("resolve", err)
	}
	_, id, err := splitMessageID(msgID)
	if err != nil {
		return err
	}
	var reactions []tg.ReactionClass
	if !remove {
		reactions = []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: emoji}}
	}
	_, err = message.NewSender(api).To(peer.InputPeer()).Reaction(ctx, id, reactions...)
	return base.PlatformErr("react", err)
}

// MarkRead reads history up to the newest given message.
func (a *Adapter) MarkRead(ctx context.Context, accountID, chatID string, ids []string, _ string) error {
	acc, api, err := a.online(accountID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	peer, err := acc.resolvePeer(ctx, chatID)
	if err != nil {
		return base.PlatformErr("resolve", err)
	}
	_, maxID, err := splitMessageID(ids[len(ids)-1])
	if err != nil {
		return err
	}
	if ch, ok := peer.(peers.Channel); ok {
		_, err = api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{Channel: ch.InputChannel(), MaxID: maxID})
	} else {
		_, err = api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: peer.InputPeer(), MaxID: maxID})
	}
	return base.PlatformErr("mark read", err)
}

// Typing sends or cancels the typing action.
func (a *Adapter) Typing(ctx context.Context, accountID, chatID, state string) error {
	acc, api, err := a.online(accountID)
	if err != nil {
		return err
	}
	peer, err := acc.resolvePeer(ctx, chatID)
	if err != nil {
		return base.PlatformErr("resolve", err)
	}
	t := message.NewSender(api).To(peer.InputPeer()).TypingAction()
	if state == "typing" {
		return base.PlatformErr("typing", t.Typing(ctx))
	}
	return base.PlatformErr("typing", t.Cancel(ctx))
}

// ResolveChat maps @username, +phone, or a t.me link to a peer.
func (a *Adapter) ResolveChat(ctx context.Context, accountID, handle string) (model.ResolvedChat, error) {
	acc, _, err := a.online(accountID)
	if err != nil {
		return model.ResolvedChat{}, err
	}
	acc.mu.Lock()
	pm := acc.peers
	acc.mu.Unlock()
	var peer peers.Peer
	switch {
	case strings.HasPrefix(handle, "+"):
		u, err := pm.ResolvePhone(ctx, strings.TrimPrefix(handle, "+"))
		if err != nil {
			return model.ResolvedChat{}, adapter.Errorf(adapter.ErrInvalidTarget, "%s: %v", handle, err)
		}
		peer = u
	case strings.Contains(handle, "t.me/"):
		peer, err = pm.ResolveDeeplink(ctx, handle)
	default:
		peer, err = pm.ResolveDomain(ctx, strings.TrimPrefix(handle, "@"))
	}
	if err != nil {
		return model.ResolvedChat{}, adapter.Errorf(adapter.ErrInvalidTarget, "%s: %v", handle, err)
	}
	id := peer.TDLibPeerID()
	r := model.ResolvedChat{ChatID: strconv.FormatInt(int64(id), 10), Kind: kindOf(peer)}
	if r.Kind == model.ChatDirect {
		r.UserID = r.ChatID
	}
	return r, nil
}

func kindOf(p peers.Peer) string {
	switch v := p.(type) {
	case peers.User:
		return model.ChatDirect
	case peers.Channel:
		if v.IsBroadcast() {
			return model.ChatChannel
		}
	}
	return model.ChatGroup
}

// GetChat returns the peer's name and, for groups, its members.
func (a *Adapter) GetChat(ctx context.Context, accountID, chatID string) (model.Chat, error) {
	acc, api, err := a.online(accountID)
	if err != nil {
		return model.Chat{}, err
	}
	peer, err := acc.resolvePeer(ctx, chatID)
	if err != nil {
		return model.Chat{}, adapter.Errorf(adapter.ErrInvalidTarget, "%s: %v", chatID, err)
	}
	ch := model.Chat{ID: chatID, Kind: kindOf(peer), Name: peer.VisibleName()}
	switch p := peer.(type) {
	case peers.Chat:
		full, err := api.MessagesGetFullChat(ctx, p.ID())
		if err != nil {
			return ch, nil
		}
		if cf, ok := full.FullChat.(*tg.ChatFull); ok {
			if parts, ok := cf.Participants.(*tg.ChatParticipants); ok {
				for _, part := range parts.Participants {
					ch.Participants = append(ch.Participants, model.Participant{ID: userID(part.GetUserID()), Role: chatRole(part)})
				}
			}
		}
	case peers.Channel:
		res, err := api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{Channel: p.InputChannel(), Filter: &tg.ChannelParticipantsRecent{}, Limit: 200})
		if err != nil {
			return ch, nil
		}
		if cp, ok := res.(*tg.ChannelsChannelParticipants); ok {
			for _, part := range cp.Participants {
				if u, ok := part.(interface{ GetUserID() int64 }); ok {
					ch.Participants = append(ch.Participants, model.Participant{ID: userID(u.GetUserID()), Role: channelRole(part)})
				}
			}
		}
	}
	return ch, nil
}

func chatRole(p tg.ChatParticipantClass) string {
	switch p.(type) {
	case *tg.ChatParticipantCreator:
		return "owner"
	case *tg.ChatParticipantAdmin:
		return "admin"
	}
	return "member"
}

func channelRole(p tg.ChannelParticipantClass) string {
	switch p.(type) {
	case *tg.ChannelParticipantCreator:
		return "owner"
	case *tg.ChannelParticipantAdmin:
		return "admin"
	}
	return "member"
}

// ListContacts returns the address book.
func (a *Adapter) ListContacts(ctx context.Context, accountID string) ([]model.Contact, error) {
	acc, api, err := a.online(accountID)
	if err != nil {
		return nil, err
	}
	res, err := api.ContactsGetContacts(ctx, 0)
	if err != nil {
		return nil, base.PlatformErr("contacts", err)
	}
	cc, ok := res.(*tg.ContactsContacts)
	if !ok {
		return nil, nil
	}
	acc.mu.Lock()
	pm := acc.peers
	acc.mu.Unlock()
	_ = pm.Apply(ctx, cc.Users, nil)
	out := make([]model.Contact, 0, len(cc.Users))
	for _, u := range cc.Users {
		if uu, ok := u.(*tg.User); ok {
			c := userContact(uu)
			c.IsContact = true
			out = append(out, *c)
		}
	}
	return out, nil
}

// FetchMedia streams a photo or document.
func (a *Adapter) FetchMedia(ctx context.Context, accountID, _ string, ref json.RawMessage, w io.Writer) (adapter.MediaMeta, error) {
	_, api, err := a.online(accountID)
	if err != nil {
		return adapter.MediaMeta{}, err
	}
	var r remoteRef
	if err := json.Unmarshal(ref, &r); err != nil || r.ID == 0 {
		return adapter.MediaMeta{}, adapter.Errorf(adapter.ErrInvalidInput, "bad remote ref")
	}
	fileRef, _ := base64.StdEncoding.DecodeString(r.FileReference)
	var loc tg.InputFileLocationClass
	mime := r.Mime
	switch r.Kind {
	case "photo":
		loc = &tg.InputPhotoFileLocation{ID: r.ID, AccessHash: r.AccessHash, FileReference: fileRef, ThumbSize: r.ThumbSize}
		if mime == "" {
			mime = "image/jpeg"
		}
	default:
		loc = &tg.InputDocumentFileLocation{ID: r.ID, AccessHash: r.AccessHash, FileReference: fileRef}
	}
	if _, err := downloader.NewDownloader().Download(api, loc).Stream(ctx, w); err != nil {
		return adapter.MediaMeta{}, base.PlatformErr("download", err)
	}
	return adapter.MediaMeta{Mime: mime}, nil
}

func unixTime(sec int) time.Time { return time.Unix(int64(sec), 0).UTC() }
