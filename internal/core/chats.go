package core

import (
	"context"
	"errors"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// ListChats pages chats by recent activity.
func (c *Core) ListChats(ctx context.Context, accountID string, f store.ChatFilter, cursor string, limit int) ([]model.Chat, string, error) {
	if _, err := c.st.GetAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		return nil, "", errNotFound("account")
	}
	chats, next, err := c.st.ListChats(ctx, accountID, f, cursor, limit)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", errInvalid("bad cursor")
	}
	return chats, next, err
}

// GetChat returns a chat with participants, asking the adapter when the store has none.
func (c *Core) GetChat(ctx context.Context, accountID, chatID string) (model.Chat, error) {
	row, ad, err := c.adapterFor(ctx, accountID)
	if err != nil {
		return model.Chat{}, err
	}
	ch, err := c.st.GetChat(ctx, accountID, chatID)
	if errors.Is(err, store.ErrNotFound) {
		if row.Status != model.StatusConnected {
			return model.Chat{}, errNotFound("chat")
		}
		fresh, aerr := ad.GetChat(ctx, accountID, chatID)
		if aerr != nil {
			return model.Chat{}, aerr
		}
		if err := c.storeChatInfo(ctx, accountID, fresh, true); err != nil {
			return model.Chat{}, err
		}
		ch, err = c.st.GetChat(ctx, accountID, chatID)
	}
	if err != nil {
		return model.Chat{}, err
	}
	if ad.Info().Has(adapter.CapChatMembers) && ch.Kind == model.ChatGroup {
		members, err := c.st.ListMembers(ctx, accountID, chatID)
		if err != nil {
			return model.Chat{}, err
		}
		if len(members) == 0 && row.Status == model.StatusConnected {
			if fresh, aerr := ad.GetChat(ctx, accountID, chatID); aerr == nil {
				if err := c.storeChatInfo(ctx, accountID, fresh, false); err != nil {
					return model.Chat{}, err
				}
				members, _ = c.st.ListMembers(ctx, accountID, chatID)
			}
		}
		ch.Participants = members
	}
	return ch, nil
}

func (c *Core) storeChatInfo(ctx context.Context, accountID string, fresh model.Chat, isNew bool) error {
	fresh.AccountID = accountID
	return c.tx(ctx, func(tx *store.Store) error {
		created, err := tx.UpsertChat(ctx, fresh)
		if err != nil {
			return err
		}
		for _, p := range fresh.Participants {
			if err := tx.UpsertMember(ctx, accountID, fresh.ID, p.ID, p.ChatName, p.Role, false); err != nil {
				return err
			}
		}
		if created || isNew {
			return c.emitChat(ctx, tx, accountID, fresh.ID, created)
		}
		return nil
	})
}

// ResolveChat maps a handle to a chat id.
func (c *Core) ResolveChat(ctx context.Context, accountID, handle string) (model.ResolvedChat, error) {
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return model.ResolvedChat{}, err
	}
	r, ok := ad.(adapter.Resolver)
	if !ok || !ad.Info().Has(adapter.CapChatResolve) {
		return model.ResolvedChat{}, errUnsupported(adapter.CapChatResolve)
	}
	if handle == "" {
		return model.ResolvedChat{}, errInvalid("handle is required")
	}
	return r.ResolveChat(ctx, accountID, handle)
}

// MarkRead sends read receipts and clears the unread counter.
func (c *Core) MarkRead(ctx context.Context, accountID, chatID, upTo string) error {
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return err
	}
	rd, ok := ad.(adapter.Reader)
	if !ok || !ad.Info().Has(adapter.CapChatRead) {
		return errUnsupported(adapter.CapChatRead)
	}
	var target store.Stored
	if upTo != "" {
		target, err = c.st.GetMessage(ctx, accountID, chatID, upTo)
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("message")
		}
	} else {
		recent, _, lerr := c.st.ListMessages(ctx, accountID, chatID, "", time.Time{}, time.Time{}, 20)
		err = lerr
		for _, m := range recent {
			if !m.FromMe {
				target = m
				break
			}
		}
	}
	if err != nil {
		return err
	}
	if target.ID != "" && !target.FromMe {
		if err := rd.MarkRead(ctx, accountID, chatID, []string{target.ID}, target.Sender.ID); err != nil {
			return err
		}
	}
	return c.tx(ctx, func(tx *store.Store) error {
		if err := tx.ClearUnread(ctx, accountID, chatID); err != nil {
			return err
		}
		return c.emitChat(ctx, tx, accountID, chatID, false)
	})
}

// Typing sends a typing indicator.
func (c *Core) Typing(ctx context.Context, accountID, chatID, state string) error {
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return err
	}
	t, ok := ad.(adapter.Typer)
	if !ok || !ad.Info().Has(adapter.CapChatTyping) {
		return errUnsupported(adapter.CapChatTyping)
	}
	if state != "typing" && state != "paused" {
		return errInvalid("state must be typing or paused")
	}
	return t.Typing(ctx, accountID, chatID, state)
}

// ChatPatch is the body of PATCH /chats/{chat}.
type ChatPatch struct {
	Muted    *bool    `json:"muted"`
	Archived *bool    `json:"archived"`
	Tags     []string `json:"tags"`
}

// PatchChat updates bridge-local flags.
func (c *Core) PatchChat(ctx context.Context, accountID, chatID string, p ChatPatch) (model.Chat, error) {
	var out model.Chat
	err := c.tx(ctx, func(tx *store.Store) error {
		ch, err := tx.SetChatFlags(ctx, accountID, chatID, p.Muted, p.Archived, p.Tags)
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("chat")
		}
		if err != nil {
			return err
		}
		out = ch
		return emit(ctx, tx, accountID, model.EvChatUpdated, ch)
	})
	return out, err
}

// ListContacts pages the address book.
func (c *Core) ListContacts(ctx context.Context, accountID, q, cursor string, limit int) ([]model.Contact, string, error) {
	if _, err := c.st.GetAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		return nil, "", errNotFound("account")
	}
	return c.st.ListContacts(ctx, accountID, q, cursor, limit)
}

// GetContact returns one contact.
func (c *Core) GetContact(ctx context.Context, accountID, userID string) (model.Contact, error) {
	ct, err := c.st.GetContact(ctx, accountID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Contact{}, errNotFound("contact")
	}
	return ct, err
}

// ContactPatch is the body of PATCH /contacts/{user}.
type ContactPatch struct {
	Alias *string `json:"alias"`
}

// PatchContact sets the owner alias (bridge-local unless the adapter supports contact.alias).
func (c *Core) PatchContact(ctx context.Context, accountID, userID string, p ContactPatch) (model.Contact, error) {
	if p.Alias == nil {
		return c.GetContact(ctx, accountID, userID)
	}
	var out model.Contact
	err := c.tx(ctx, func(tx *store.Store) error {
		ct, err := tx.SetLocalAlias(ctx, accountID, userID, *p.Alias)
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("contact")
		}
		if err != nil {
			return err
		}
		out = ct
		return emit(ctx, tx, accountID, model.EvContactUpdated, ct)
	})
	return out, err
}
