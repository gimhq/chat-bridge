package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

func (a *Adapter) online(accountID string) (*account, *mautrix.Client, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return nil, nil, err
	}
	cli, err := acc.client()
	return acc, cli, err
}

// SendMessage posts one room event.
func (a *Adapter) SendMessage(ctx context.Context, accountID string, req adapter.SendRequest) (model.Message, error) {
	_, cli, err := a.online(accountID)
	if err != nil {
		return model.Message{}, err
	}
	room := id.RoomID(req.ChatID)
	content, evType, err := buildContent(ctx, cli, req)
	if err != nil {
		return model.Message{}, err
	}
	content.RelatesTo = relates(req)
	if len(req.Mentions) > 0 {
		content.Mentions = &event.Mentions{}
		for _, m := range req.Mentions {
			content.Mentions.UserIDs = append(content.Mentions.UserIDs, id.UserID(m))
		}
	}
	resp, err := cli.SendMessageEvent(ctx, room, evType, content)
	if err != nil {
		return model.Message{}, mapErr("send", err)
	}
	return model.Message{ID: resp.EventID.String(), ChatID: req.ChatID, Content: req.Content, Status: model.MsgSent}, nil
}

func relates(req adapter.SendRequest) *event.RelatesTo {
	if req.ReplyTo == "" && req.ThreadID == "" {
		return nil
	}
	rel := &event.RelatesTo{}
	if req.ThreadID != "" {
		rel.Type, rel.EventID = event.RelThread, id.EventID(req.ThreadID)
		rel.IsFallingBack = req.ReplyTo == ""
		if req.ReplyTo == "" {
			rel.InReplyTo = &event.InReplyTo{EventID: id.EventID(req.ThreadID)}
		}
	}
	if req.ReplyTo != "" {
		rel.InReplyTo = &event.InReplyTo{EventID: id.EventID(req.ReplyTo)}
	}
	return rel
}

func textContent(text, formatName string) *event.MessageEventContent {
	switch formatName {
	case "markdown":
		c := format.RenderMarkdown(text, true, false)
		return &c
	case "html":
		c := format.RenderMarkdown(text, false, true)
		return &c
	}
	return &event.MessageEventContent{MsgType: event.MsgText, Body: text}
}

func buildContent(ctx context.Context, cli *mautrix.Client, req adapter.SendRequest) (*event.MessageEventContent, event.Type, error) {
	c := req.Content
	switch c.Type {
	case model.ContentText:
		return textContent(c.Text, c.Format), event.EventMessage, nil
	case model.ContentLocation:
		l := c.Location
		body := c.Text
		if body == "" {
			body = l.Name
		}
		if body == "" {
			body = fmt.Sprintf("%f,%f", l.Lat, l.Lon)
		}
		return &event.MessageEventContent{MsgType: event.MsgLocation, Body: body, GeoURI: fmt.Sprintf("geo:%f,%f", l.Lat, l.Lon)}, event.EventMessage, nil
	case model.ContentImage, model.ContentVideo, model.ContentAudio, model.ContentVoice, model.ContentFile, model.ContentSticker:
		att := c.Attachments[0]
		rc, meta, err := req.Media.Open(ctx, att.MediaID)
		if err != nil {
			return nil, event.Type{}, adapter.Errorf(adapter.ErrInvalidInput, "open media: %v", err)
		}
		defer func() { _ = rc.Close() }()
		name := meta.FileName
		if name == "" {
			name = c.Type
		}
		mc := &event.MessageEventContent{Body: name,
			Info: &event.FileInfo{MimeType: meta.Mime, Size: int(meta.Size), Width: meta.Width, Height: meta.Height, Duration: int(meta.DurationMs)}}
		encrypted, _ := cli.StateStore.IsEncrypted(ctx, id.RoomID(req.ChatID))
		if encrypted && cli.Crypto != nil {
			// Attachments in encrypted rooms are encrypted client-side; only the key travels in the event.
			plain, err := io.ReadAll(rc)
			if err != nil {
				return nil, event.Type{}, err
			}
			ef := attachment.NewEncryptedFile()
			ef.EncryptInPlace(plain)
			up, err := cli.UploadMedia(ctx, mautrix.ReqUploadMedia{ContentBytes: plain, ContentType: "application/octet-stream"})
			if err != nil {
				return nil, event.Type{}, mapErr("upload", err)
			}
			mc.File = &event.EncryptedFileInfo{EncryptedFile: *ef, URL: up.ContentURI.CUString()}
		} else {
			up, err := cli.UploadMedia(ctx, mautrix.ReqUploadMedia{Content: rc, ContentLength: meta.Size, ContentType: meta.Mime, FileName: meta.FileName})
			if err != nil {
				return nil, event.Type{}, mapErr("upload", err)
			}
			mc.URL = up.ContentURI.CUString()
		}
		if c.Text != "" {
			mc.FileName, mc.Body = name, c.Text
		}
		evType := event.EventMessage
		switch c.Type {
		case model.ContentImage:
			mc.MsgType = event.MsgImage
		case model.ContentVideo:
			mc.MsgType = event.MsgVideo
		case model.ContentAudio:
			mc.MsgType = event.MsgAudio
		case model.ContentVoice:
			mc.MsgType = event.MsgAudio
			mc.MSC3245Voice = &event.MSC3245Voice{}
			mc.MSC1767Audio = &event.MSC1767Audio{Duration: int(meta.DurationMs)}
		case model.ContentSticker:
			evType = event.EventSticker
		default:
			mc.MsgType = event.MsgFile
		}
		return mc, evType, nil
	}
	return nil, event.Type{}, adapter.Errorf(adapter.ErrUnsupported, "cannot send %s", c.Type)
}

// EditMessage sends an m.replace event.
func (a *Adapter) EditMessage(ctx context.Context, accountID, chatID, msgID string, c model.Content) (model.Message, error) {
	_, cli, err := a.online(accountID)
	if err != nil {
		return model.Message{}, err
	}
	newContent := textContent(c.Text, c.Format)
	edit := &event.MessageEventContent{MsgType: event.MsgText, Body: "* " + newContent.Body, NewContent: newContent,
		RelatesTo: &event.RelatesTo{Type: event.RelReplace, EventID: id.EventID(msgID)}}
	if newContent.FormattedBody != "" {
		edit.Format, edit.FormattedBody = event.FormatHTML, "* "+newContent.FormattedBody
	}
	if _, err := cli.SendMessageEvent(ctx, id.RoomID(chatID), event.EventMessage, edit); err != nil {
		return model.Message{}, mapErr("edit", err)
	}
	return model.Message{ID: msgID, ChatID: chatID, Content: c}, nil
}

// DeleteMessage redacts an event.
func (a *Adapter) DeleteMessage(ctx context.Context, accountID, chatID, msgID, _ string) error {
	_, cli, err := a.online(accountID)
	if err != nil {
		return err
	}
	_, err = cli.RedactEvent(ctx, id.RoomID(chatID), id.EventID(msgID))
	return mapErr("redact", err)
}

// React sends an annotation, or redacts our earlier one.
func (a *Adapter) React(ctx context.Context, accountID, chatID, msgID, _, emoji string, remove bool) error {
	acc, cli, err := a.online(accountID)
	if err != nil {
		return err
	}
	key := chatID + "|" + msgID + "|" + emoji
	if remove {
		acc.mu.Lock()
		own, ok := acc.reactions[key]
		delete(acc.reactions, key)
		acc.mu.Unlock()
		if !ok {
			return adapter.Errorf(adapter.ErrUnsupported, "reaction was not sent by this bridge session")
		}
		_, err = cli.RedactEvent(ctx, id.RoomID(chatID), own)
		return mapErr("redact reaction", err)
	}
	resp, err := cli.SendReaction(ctx, id.RoomID(chatID), id.EventID(msgID), emoji)
	if err != nil {
		return mapErr("react", err)
	}
	acc.mu.Lock()
	acc.reactions[key] = resp.EventID
	acc.mu.Unlock()
	return nil
}

// MarkRead sets the read marker.
func (a *Adapter) MarkRead(ctx context.Context, accountID, chatID string, ids []string, _ string) error {
	_, cli, err := a.online(accountID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return mapErr("mark read", cli.MarkRead(ctx, id.RoomID(chatID), id.EventID(ids[len(ids)-1])))
}

// Typing sends a typing notification.
func (a *Adapter) Typing(ctx context.Context, accountID, chatID, state string) error {
	_, cli, err := a.online(accountID)
	if err != nil {
		return err
	}
	_, err = cli.UserTyping(ctx, id.RoomID(chatID), state == "typing", typingTimeout)
	return mapErr("typing", err)
}

// ResolveChat maps a user id, room alias, or room id to a room.
func (a *Adapter) ResolveChat(ctx context.Context, accountID, handle string) (model.ResolvedChat, error) {
	acc, cli, err := a.online(accountID)
	if err != nil {
		return model.ResolvedChat{}, err
	}
	switch {
	case strings.HasPrefix(handle, "!"):
		return model.ResolvedChat{ChatID: handle, Kind: acc.kindOf(id.RoomID(handle))}, nil
	case strings.HasPrefix(handle, "#"):
		resp, err := cli.ResolveAlias(ctx, id.RoomAlias(handle))
		if err != nil {
			return model.ResolvedChat{}, mapErr("resolve alias", err)
		}
		return model.ResolvedChat{ChatID: resp.RoomID.String(), Kind: model.ChatGroup}, nil
	case strings.HasPrefix(handle, "@"):
		user := id.UserID(handle)
		acc.mu.Lock()
		room, ok := acc.direct[user]
		acc.mu.Unlock()
		if !ok {
			resp, err := cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{IsDirect: true, Invite: []id.UserID{user}, Preset: "trusted_private_chat"})
			if err != nil {
				return model.ResolvedChat{}, mapErr("create dm", err)
			}
			room = resp.RoomID
			acc.mu.Lock()
			if acc.direct == nil {
				acc.direct = map[id.UserID]id.RoomID{}
			}
			acc.direct[user], acc.roomKind[room] = room, model.ChatDirect
			acc.mu.Unlock()
		}
		return model.ResolvedChat{ChatID: room.String(), Kind: model.ChatDirect, UserID: handle}, nil
	}
	return model.ResolvedChat{}, adapter.Errorf(adapter.ErrInvalidInput, "handle must be @user:server, #alias:server or !room:server")
}

// GetChat returns room name and members.
func (a *Adapter) GetChat(ctx context.Context, accountID, chatID string) (model.Chat, error) {
	acc, cli, err := a.online(accountID)
	if err != nil {
		return model.Chat{}, err
	}
	room := id.RoomID(chatID)
	ch := model.Chat{ID: chatID, Kind: acc.kindOf(room)}
	var name event.RoomNameEventContent
	if err := cli.StateEvent(ctx, room, event.StateRoomName, "", &name); err == nil {
		ch.Name = name.Name
	}
	members, err := cli.JoinedMembers(ctx, room)
	if err != nil {
		return model.Chat{}, mapErr("members", err)
	}
	for user, m := range members.Joined {
		ch.Participants = append(ch.Participants, model.Participant{ID: user.String(), ChatName: m.DisplayName, Role: "member"})
		if ch.Name == "" && ch.Kind == model.ChatDirect && user != cli.UserID {
			ch.Name = m.DisplayName
		}
	}
	return ch, nil
}

// ListContacts returns the users we have direct rooms with.
func (a *Adapter) ListContacts(ctx context.Context, accountID string) ([]model.Contact, error) {
	acc, cli, err := a.online(accountID)
	if err != nil {
		return nil, err
	}
	acc.loadDirect(ctx)
	acc.mu.Lock()
	users := make([]id.UserID, 0, len(acc.direct))
	for u := range acc.direct {
		users = append(users, u)
	}
	acc.mu.Unlock()
	out := make([]model.Contact, 0, len(users))
	for _, u := range users {
		c := userContact(u, "")
		c.IsContact = true
		if p, err := cli.GetProfile(ctx, u); err == nil {
			c.Names.Profile = p.DisplayName
		}
		out = append(out, *c)
	}
	return out, nil
}

// FetchMedia streams an mxc:// download.
func (a *Adapter) FetchMedia(ctx context.Context, accountID, _ string, ref json.RawMessage, w io.Writer) (adapter.MediaMeta, error) {
	_, cli, err := a.online(accountID)
	if err != nil {
		return adapter.MediaMeta{}, err
	}
	var r remoteRef
	if err := json.Unmarshal(ref, &r); err != nil || r.URL == "" {
		return adapter.MediaMeta{}, adapter.Errorf(adapter.ErrInvalidInput, "bad remote ref")
	}
	uri, err := id.ParseContentURI(r.URL)
	if err != nil {
		return adapter.MediaMeta{}, adapter.Errorf(adapter.ErrInvalidInput, "bad mxc url")
	}
	resp, err := cli.Download(ctx, uri)
	if err != nil {
		return adapter.MediaMeta{}, mapErr("download", err)
	}
	defer drain(resp.Body)
	if r.File == nil {
		if _, err := io.Copy(w, resp.Body); err != nil {
			return adapter.MediaMeta{}, base.PlatformErr("download", err)
		}
		return adapter.MediaMeta{Mime: resp.Header.Get("Content-Type")}, nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return adapter.MediaMeta{}, base.PlatformErr("download", err)
	}
	if err := r.File.DecryptInPlace(data); err != nil {
		return adapter.MediaMeta{}, base.PlatformErr("decrypt attachment", err)
	}
	if _, err := w.Write(data); err != nil {
		return adapter.MediaMeta{}, err
	}
	return adapter.MediaMeta{}, nil
}
