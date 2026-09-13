package whatsapp

import (
	"context"
	"io"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

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
)

func (a *Adapter) loggedIn(id string) (*account, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return nil, err
	}
	if err := acc.requireLogin(); err != nil {
		return nil, err
	}
	return acc, nil
}

// CreateChat creates a group; WhatsApp adds the owner itself.
func (a *Adapter) CreateChat(ctx context.Context, id string, req adapter.CreateChatRequest) (model.Chat, error) {
	acc, err := a.loggedIn(id)
	if err != nil {
		return model.Chat{}, err
	}
	members := make([]types.JID, 0, len(req.Members))
	for _, m := range req.Members {
		jid, err := parseJID(m)
		if err != nil {
			return model.Chat{}, err
		}
		members = append(members, jid.ToNonAD())
	}
	g, err := acc.cli.CreateGroup(ctx, whatsmeow.ReqCreateGroup{Name: req.Name, Participants: members})
	if err != nil {
		return model.Chat{}, base.PlatformErr("create group", err)
	}
	return groupChat(g), nil
}

// UpdateChat renames a group.
func (a *Adapter) UpdateChat(ctx context.Context, id, chatID string, p adapter.ChatUpdate) (model.Chat, error) {
	acc, err := a.loggedIn(id)
	if err != nil {
		return model.Chat{}, err
	}
	jid, err := parseJID(chatID)
	if err != nil {
		return model.Chat{}, err
	}
	if jid.Server != types.GroupServer {
		return model.Chat{}, adapter.Errorf(adapter.ErrUnsupported, "only groups can be renamed")
	}
	if p.Name != nil {
		if err := acc.cli.SetGroupName(ctx, jid, *p.Name); err != nil {
			return model.Chat{}, base.PlatformErr("set group name", err)
		}
	}
	return a.GetChat(ctx, id, chatID)
}

// Block updates the account's blocklist.
func (a *Adapter) Block(ctx context.Context, id, userID string, blocked bool) error {
	acc, err := a.loggedIn(id)
	if err != nil {
		return err
	}
	jid, err := parseJID(userID)
	if err != nil {
		return err
	}
	action := events.BlocklistChangeActionUnblock
	if blocked {
		action = events.BlocklistChangeActionBlock
	}
	if _, err := acc.cli.UpdateBlocklist(ctx, jid.ToNonAD(), action); err != nil {
		return base.PlatformErr("blocklist", err)
	}
	return nil
}

// UpdateSelf sets the push name, the "about" text and the profile picture (JPEG).
func (a *Adapter) UpdateSelf(ctx context.Context, id string, p adapter.SelfUpdate) (model.Contact, error) {
	acc, err := a.loggedIn(id)
	if err != nil {
		return model.Contact{}, err
	}
	if p.Name != nil {
		if err := acc.cli.SendAppState(ctx, appstate.BuildSettingPushName(*p.Name)); err != nil {
			return model.Contact{}, base.PlatformErr("set push name", err)
		}
		acc.cli.Store.PushName = *p.Name
	}
	if p.Bio != nil {
		if err := acc.cli.SetStatusMessage(ctx, types.SetStatusInput{Text: p.Bio}); err != nil {
			return model.Contact{}, base.PlatformErr("set status", err)
		}
	}
	if p.AvatarMediaID != "" {
		rc, _, err := p.Media.Open(ctx, p.AvatarMediaID)
		if err != nil {
			return model.Contact{}, err
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return model.Contact{}, err
		}
		if _, err := acc.cli.SetGroupPhoto(ctx, types.EmptyJID, data); err != nil { // empty JID = own profile picture
			return model.Contact{}, base.PlatformErr("set profile photo", err)
		}
	}
	self := acc.selfContact()
	if self == nil {
		return model.Contact{}, adapter.Errorf(adapter.ErrNotConnected, "not logged in")
	}
	if p.Bio != nil {
		self.Bio = *p.Bio
	}
	if p.AvatarMediaID != "" {
		self.Avatar = &model.AvatarRef{MediaID: p.AvatarMediaID}
	}
	return *self, nil
}
