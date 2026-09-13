package telegram

import (
	"context"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
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

// CreateChat creates a basic group with the given users.
func (a *Adapter) CreateChat(ctx context.Context, accountID string, req adapter.CreateChatRequest) (model.Chat, error) {
	acc, api, err := a.online(accountID)
	if err != nil {
		return model.Chat{}, err
	}
	users := make([]tg.InputUserClass, 0, len(req.Members))
	for _, m := range req.Members {
		p, err := acc.resolvePeer(ctx, m)
		if err != nil {
			return model.Chat{}, err
		}
		u, ok := p.(peers.User)
		if !ok {
			return model.Chat{}, adapter.Errorf(adapter.ErrInvalidTarget, "%s is not a user", m)
		}
		users = append(users, u.InputUser())
	}
	res, err := api.MessagesCreateChat(ctx, &tg.MessagesCreateChatRequest{Users: users, Title: req.Name})
	if err != nil {
		return model.Chat{}, base.PlatformErr("create chat", err)
	}
	var chats []tg.ChatClass
	switch u := res.Updates.(type) {
	case *tg.Updates:
		chats = u.Chats
	case *tg.UpdatesCombined:
		chats = u.Chats
	}
	for _, c := range chats {
		if ch, ok := c.(*tg.Chat); ok {
			acc.mu.Lock()
			pm := acc.peers
			acc.mu.Unlock()
			_ = pm.Apply(ctx, nil, chats)
			return a.GetChat(ctx, accountID, chatIDOf(ch.ID))
		}
	}
	return model.Chat{}, adapter.Errorf(adapter.ErrPlatform, "create chat: no chat in response")
}

// UpdateChat renames a group or channel.
func (a *Adapter) UpdateChat(ctx context.Context, accountID, chatID string, p adapter.ChatUpdate) (model.Chat, error) {
	acc, api, err := a.online(accountID)
	if err != nil {
		return model.Chat{}, err
	}
	if p.Name != nil {
		peer, err := acc.resolvePeer(ctx, chatID)
		if err != nil {
			return model.Chat{}, err
		}
		switch pp := peer.(type) {
		case peers.Chat:
			_, err = api.MessagesEditChatTitle(ctx, &tg.MessagesEditChatTitleRequest{ChatID: pp.ID(), Title: *p.Name})
		case peers.Channel:
			_, err = api.ChannelsEditTitle(ctx, &tg.ChannelsEditTitleRequest{Channel: pp.InputChannel(), Title: *p.Name})
		default:
			return model.Chat{}, adapter.Errorf(adapter.ErrUnsupported, "only groups and channels can be renamed")
		}
		if err != nil {
			return model.Chat{}, base.PlatformErr("rename", err)
		}
	}
	return a.GetChat(ctx, accountID, chatID)
}

// Block adds or removes a user from the blocklist.
func (a *Adapter) Block(ctx context.Context, accountID, userID string, blocked bool) error {
	acc, api, err := a.online(accountID)
	if err != nil {
		return err
	}
	peer, err := acc.resolvePeer(ctx, userID)
	if err != nil {
		return err
	}
	if blocked {
		_, err = api.ContactsBlock(ctx, &tg.ContactsBlockRequest{ID: peer.InputPeer()})
	} else {
		_, err = api.ContactsUnblock(ctx, &tg.ContactsUnblockRequest{ID: peer.InputPeer()})
	}
	if err != nil {
		return base.PlatformErr("block", err)
	}
	return nil
}

// UpdateSelf changes first name, about text and profile photo.
func (a *Adapter) UpdateSelf(ctx context.Context, accountID string, p adapter.SelfUpdate) (model.Contact, error) {
	acc, api, err := a.online(accountID)
	if err != nil {
		return model.Contact{}, err
	}
	if p.Name != nil || p.Bio != nil {
		req := &tg.AccountUpdateProfileRequest{}
		if p.Name != nil {
			req.SetFirstName(*p.Name)
		}
		if p.Bio != nil {
			req.SetAbout(*p.Bio)
		}
		if _, err := api.AccountUpdateProfile(ctx, req); err != nil {
			return model.Contact{}, base.PlatformErr("update profile", err)
		}
	}
	if p.AvatarMediaID != "" {
		rc, att, err := p.Media.Open(ctx, p.AvatarMediaID)
		if err != nil {
			return model.Contact{}, err
		}
		name := att.FileName
		if name == "" {
			name = "avatar.jpg"
		}
		f, err := uploader.NewUploader(api).FromReader(ctx, name, rc)
		_ = rc.Close()
		if err != nil {
			return model.Contact{}, base.PlatformErr("upload avatar", err)
		}
		req := &tg.PhotosUploadProfilePhotoRequest{}
		req.SetFile(f)
		if _, err := api.PhotosUploadProfilePhoto(ctx, req); err != nil {
			return model.Contact{}, base.PlatformErr("set avatar", err)
		}
	}
	self, err := acc.client().Self(ctx)
	if err != nil {
		return model.Contact{}, base.PlatformErr("self", err)
	}
	acc.mu.Lock()
	acc.self = self
	acc.mu.Unlock()
	c := userContact(self)
	c.IsSelf, c.IsContact = true, true
	if p.Bio != nil {
		c.Bio = *p.Bio
	}
	if p.AvatarMediaID != "" {
		c.Avatar = &model.AvatarRef{MediaID: p.AvatarMediaID}
	}
	return *c, nil
}

// Backfill pages history older than the cursor through messages.getHistory.
func (a *Adapter) Backfill(ctx context.Context, accountID, chatID string, before adapter.BackfillCursor, limit int) ([]model.Message, bool, error) {
	acc, api, err := a.online(accountID)
	if err != nil {
		return nil, false, err
	}
	peer, err := acc.resolvePeer(ctx, chatID)
	if err != nil {
		return nil, false, err
	}
	req := &tg.MessagesGetHistoryRequest{Peer: peer.InputPeer(), Limit: limit}
	if before.MessageID != "" {
		if _, mid, err := splitMessageID(before.MessageID); err == nil {
			req.OffsetID = mid
		}
	} else if !before.Timestamp.IsZero() {
		req.OffsetDate = int(before.Timestamp.Unix())
	}
	res, err := api.MessagesGetHistory(ctx, req)
	if err != nil {
		return nil, false, base.PlatformErr("history", err)
	}
	var messages []tg.MessageClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch r := res.(type) {
	case *tg.MessagesMessages:
		messages, users, chats = r.Messages, r.Users, r.Chats
	case *tg.MessagesMessagesSlice:
		messages, users, chats = r.Messages, r.Users, r.Chats
	case *tg.MessagesChannelMessages:
		messages, users, chats = r.Messages, r.Users, r.Chats
	default:
		return nil, false, nil
	}
	acc.mu.Lock()
	pm := acc.peers
	acc.mu.Unlock()
	_ = pm.Apply(ctx, users, chats)
	ents := entitiesOf(users, chats)
	out := make([]model.Message, 0, len(messages))
	for _, m := range messages {
		for _, ev := range acc.convertMessage(m, ents, false) {
			if ev.Kind == adapter.EvMessage && ev.Message != nil {
				out = append(out, *ev.Message)
			}
		}
	}
	return out, len(messages) >= limit && limit > 0, nil
}

// entitiesOf indexes the users and chats a response carries so message conversion can name them.
func entitiesOf(users []tg.UserClass, chats []tg.ChatClass) tg.Entities {
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
	return ents
}
