package matrix

import (
	"context"
	"io"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

// Compile-time checks for the platform-management interfaces.
var (
	_ adapter.ChatCreator = (*Adapter)(nil)
	_ adapter.ChatUpdater = (*Adapter)(nil)
	_ adapter.Blocker     = (*Adapter)(nil)
	_ adapter.SelfUpdater = (*Adapter)(nil)
	_ adapter.Backfiller  = (*Adapter)(nil)
)

// CreateChat creates a private room and invites the members.
func (a *Adapter) CreateChat(ctx context.Context, accountID string, req adapter.CreateChatRequest) (model.Chat, error) {
	acc, cli, err := a.online(accountID)
	if err != nil {
		return model.Chat{}, err
	}
	invite := make([]id.UserID, 0, len(req.Members))
	for _, m := range req.Members {
		invite = append(invite, id.UserID(m))
	}
	resp, err := cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{Name: req.Name, Invite: invite, Preset: "private_chat"})
	if err != nil {
		return model.Chat{}, mapErr("create room", err)
	}
	acc.mu.Lock()
	acc.roomKind[resp.RoomID] = model.ChatGroup
	acc.mu.Unlock()
	return a.GetChat(ctx, accountID, resp.RoomID.String())
}

// UpdateChat sets the room name.
func (a *Adapter) UpdateChat(ctx context.Context, accountID, chatID string, p adapter.ChatUpdate) (model.Chat, error) {
	_, cli, err := a.online(accountID)
	if err != nil {
		return model.Chat{}, err
	}
	if p.Name != nil {
		if _, err := cli.SendStateEvent(ctx, id.RoomID(chatID), event.StateRoomName, "", &event.RoomNameEventContent{Name: *p.Name}); err != nil {
			return model.Chat{}, mapErr("set room name", err)
		}
	}
	return a.GetChat(ctx, accountID, chatID)
}

// Block maintains m.ignored_user_list, the Matrix equivalent of blocking.
func (a *Adapter) Block(ctx context.Context, accountID, userID string, blocked bool) error {
	_, cli, err := a.online(accountID)
	if err != nil {
		return err
	}
	var list event.IgnoredUserListEventContent
	if err := cli.GetAccountData(ctx, event.AccountDataIgnoredUserList.Type, &list); err != nil && httpStatus(err) != 404 {
		return mapErr("ignored users", err)
	}
	if list.IgnoredUsers == nil {
		list.IgnoredUsers = map[id.UserID]event.IgnoredUser{}
	}
	if blocked {
		list.IgnoredUsers[id.UserID(userID)] = event.IgnoredUser{}
	} else {
		delete(list.IgnoredUsers, id.UserID(userID))
	}
	if err := cli.SetAccountData(ctx, event.AccountDataIgnoredUserList.Type, &list); err != nil {
		return mapErr("ignored users", err)
	}
	return nil
}

// UpdateSelf sets the display name and avatar; Matrix has no profile bio.
func (a *Adapter) UpdateSelf(ctx context.Context, accountID string, p adapter.SelfUpdate) (model.Contact, error) {
	acc, cli, err := a.online(accountID)
	if err != nil {
		return model.Contact{}, err
	}
	if p.Name != nil {
		if err := cli.SetDisplayName(ctx, *p.Name); err != nil {
			return model.Contact{}, mapErr("set display name", err)
		}
	}
	if p.Bio != nil {
		return model.Contact{}, adapter.Errorf(adapter.ErrUnsupported, "Matrix profiles have no bio")
	}
	if p.AvatarMediaID != "" {
		rc, att, err := p.Media.Open(ctx, p.AvatarMediaID)
		if err != nil {
			return model.Contact{}, err
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return model.Contact{}, err
		}
		up, err := cli.UploadMedia(ctx, mautrix.ReqUploadMedia{ContentBytes: data, ContentType: att.Mime, FileName: att.FileName})
		if err != nil {
			return model.Contact{}, mapErr("upload avatar", err)
		}
		if err := cli.SetAvatarURL(ctx, up.ContentURI); err != nil {
			return model.Contact{}, mapErr("set avatar", err)
		}
	}
	self := acc.selfContact(ctx)
	if self == nil {
		return model.Contact{}, adapter.Errorf(adapter.ErrNotConnected, "not logged in")
	}
	if p.AvatarMediaID != "" {
		self.Avatar = &model.AvatarRef{MediaID: p.AvatarMediaID}
	}
	return *self, nil
}

// Backfill pages the room timeline backwards from the cursor event (or from the end).
func (a *Adapter) Backfill(ctx context.Context, accountID, chatID string, before adapter.BackfillCursor, limit int) ([]model.Message, bool, error) {
	acc, cli, err := a.online(accountID)
	if err != nil {
		return nil, false, err
	}
	room := id.RoomID(chatID)
	from := ""
	if before.MessageID != "" {
		c, err := cli.Context(ctx, room, id.EventID(before.MessageID), nil, 1)
		if err != nil {
			return nil, false, mapErr("context", err)
		}
		from = c.Start
	}
	filter := &mautrix.FilterPart{Types: []event.Type{event.EventMessage, event.EventSticker, event.EventEncrypted}}
	resp, err := cli.Messages(ctx, room, from, "", mautrix.DirectionBackward, filter, limit)
	if err != nil {
		return nil, false, mapErr("messages", err)
	}
	out := make([]model.Message, 0, len(resp.Chunk))
	for _, evt := range resp.Chunk {
		if evt.Type == event.EventEncrypted {
			acc.mu.Lock()
			helper := acc.crypto
			acc.mu.Unlock()
			if helper == nil {
				continue
			}
			dec, err := helper.Decrypt(ctx, evt)
			if err != nil {
				continue // undecryptable history is not worth a placeholder row
			}
			evt = dec
		} else if err := evt.Content.ParseRaw(evt.Type); err != nil {
			continue
		}
		if m, ok := acc.messageOf(evt); ok {
			out = append(out, m)
		}
	}
	return out, resp.End != "", nil
}
