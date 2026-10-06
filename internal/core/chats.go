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
	sc, err := c.scopeOf(ctx)
	if err != nil {
		return nil, "", err
	}
	if sc != nil {
		if !sc.accounts[accountID] {
			return nil, "", errNotFound("account")
		}
		f.Only = sc.chatIDs(accountID)
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
	if err := c.allowChat(ctx, accountID, chatID); err != nil {
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
	named := []model.Chat{ch}
	if err := c.st.NameDirectChats(ctx, named); err != nil {
		return model.Chat{}, err
	}
	return named[0], nil
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

// ResolveChat maps a handle to a chat id. A scoped token may only resolve a contact in its scope;
// the direct chat it gets is stored, which makes it reachable for that token from then on.
func (c *Core) ResolveChat(ctx context.Context, accountID, handle string) (model.ResolvedChat, error) {
	if err := c.allowWrite(ctx); err != nil {
		return model.ResolvedChat{}, err
	}
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
	sc, err := c.scopeOf(ctx)
	if err != nil {
		return model.ResolvedChat{}, err
	}
	if sc == nil {
		return r.ResolveChat(ctx, accountID, handle)
	}
	userID, err := c.scopedHandle(ctx, sc, accountID, handle)
	if err != nil {
		return model.ResolvedChat{}, err
	}
	res, err := r.ResolveChat(ctx, accountID, handle)
	if err != nil {
		return model.ResolvedChat{}, err
	}
	if res.Kind != model.ChatDirect || res.UserID != userID {
		return model.ResolvedChat{}, errNotFound("chat")
	}
	direct := model.Chat{ID: res.ChatID, Kind: model.ChatDirect, Participants: []model.Participant{{ID: userID, Role: "member"}}}
	if err := c.storeChatInfo(ctx, accountID, direct, false); err != nil {
		return model.ResolvedChat{}, err
	}
	return res, nil
}

// MarkRead sends read receipts and clears the unread counter.
func (c *Core) MarkRead(ctx context.Context, accountID, chatID, upTo string) error {
	if err := c.allowWrite(ctx); err != nil {
		return err
	}
	if err := c.allowChat(ctx, accountID, chatID); err != nil {
		return err
	}
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
	if err := c.allowWrite(ctx); err != nil {
		return err
	}
	if err := c.allowChat(ctx, accountID, chatID); err != nil {
		return err
	}
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
	Name     *string  `json:"name"`
}

// PatchChat updates bridge-local flags; a name goes to the platform first (chat.update).
func (c *Core) PatchChat(ctx context.Context, accountID, chatID string, p ChatPatch) (model.Chat, error) {
	if p.Name != nil {
		_, ad, err := c.connected(ctx, accountID)
		if err != nil {
			return model.Chat{}, err
		}
		up, ok := ad.(adapter.ChatUpdater)
		if !ok {
			return model.Chat{}, errUnsupported("chat.update")
		}
		if *p.Name == "" {
			return model.Chat{}, errInvalid("name must not be empty")
		}
		if _, err := c.st.GetChat(ctx, accountID, chatID); errors.Is(err, store.ErrNotFound) {
			return model.Chat{}, errNotFound("chat")
		}
		if _, err := up.UpdateChat(ctx, accountID, chatID, adapter.ChatUpdate{Name: p.Name}); err != nil {
			return model.Chat{}, err
		}
	}
	var out model.Chat
	err := c.tx(ctx, func(tx *store.Store) error {
		ch, err := tx.SetChatFlags(ctx, accountID, chatID, p.Muted, p.Archived, p.Tags)
		if err == nil && p.Name != nil {
			ch, err = tx.SetChatName(ctx, accountID, chatID, *p.Name)
		}
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

// CreateChat creates a group on the platform (chat.create) and stores it.
func (c *Core) CreateChat(ctx context.Context, accountID string, req adapter.CreateChatRequest) (model.Chat, error) {
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return model.Chat{}, err
	}
	cr, ok := ad.(adapter.ChatCreator)
	if !ok || !ad.Info().Has(adapter.CapChatCreate) {
		return model.Chat{}, errUnsupported(adapter.CapChatCreate)
	}
	if req.Kind == "" {
		req.Kind = model.ChatGroup
	}
	if req.Kind != model.ChatGroup {
		return model.Chat{}, errInvalid("only group chats can be created")
	}
	if req.Name == "" {
		return model.Chat{}, errInvalid("name is required")
	}
	ch, err := cr.CreateChat(ctx, accountID, req)
	if err != nil {
		return model.Chat{}, err
	}
	if ch.Kind == "" {
		ch.Kind = model.ChatGroup
	}
	if err := c.storeChatInfo(ctx, accountID, ch, true); err != nil {
		return model.Chat{}, err
	}
	return c.GetChat(ctx, accountID, ch.ID)
}

// ListContacts pages the address book.
func (c *Core) ListContacts(ctx context.Context, accountID, q, cursor string, limit int) ([]model.Contact, string, error) {
	if _, err := c.st.GetAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		return nil, "", errNotFound("account")
	}
	sc, err := c.scopeOf(ctx)
	if err != nil {
		return nil, "", err
	}
	var only store.Only
	if sc != nil {
		if !sc.accounts[accountID] {
			return nil, "", errNotFound("account")
		}
		only = sc.contactIDs(accountID)
	}
	return c.st.ListContacts(ctx, accountID, q, cursor, limit, only)
}

// GetContact returns one contact.
func (c *Core) GetContact(ctx context.Context, accountID, userID string) (model.Contact, error) {
	if err := c.allowContact(ctx, accountID, userID); err != nil {
		return model.Contact{}, err
	}
	ct, err := c.st.GetContact(ctx, accountID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Contact{}, errNotFound("contact")
	}
	return ct, err
}

// ContactChats returns the direct chats with a contact and the other chats they are a member of.
// A scoped token gets the ones inside its scope.
func (c *Core) ContactChats(ctx context.Context, accountID, userID string) ([]model.Chat, error) {
	if err := c.allowContact(ctx, accountID, userID); err != nil {
		return nil, err
	}
	out, err := c.st.ContactChats(ctx, accountID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound("contact")
	}
	if err != nil {
		return nil, err
	}
	sc, err := c.scopeOf(ctx)
	if err != nil || sc == nil {
		return out, err
	}
	kept := out[:0]
	for _, ch := range out {
		if sc.chats[store.ChatRef{AccountID: accountID, ChatID: ch.ID}] {
			kept = append(kept, ch)
		}
	}
	return kept, nil
}

// ContactMessages pages a contact's messages; scope is "direct" (default, the conversation with
// them) or "all" (also their messages in other chats). A scoped token always gets "direct".
func (c *Core) ContactMessages(ctx context.Context, accountID, userID, scope, cursor string, limit int) ([]model.Message, string, error) {
	if scope == "" {
		scope = "direct"
	}
	if scope != "direct" && scope != "all" {
		return nil, "", errInvalid("scope must be direct or all")
	}
	if err := c.allowContact(ctx, accountID, userID); err != nil {
		return nil, "", err
	}
	if Scoped(ctx) {
		scope = "direct"
	}
	if cursor != "" {
		if _, _, err := store.DecodeMessageCursor(cursor); err != nil {
			return nil, "", errInvalid("bad cursor")
		}
	}
	rows, next, err := c.st.ContactMessages(ctx, accountID, userID, scope, cursor, limit)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", errNotFound("contact")
	}
	if err != nil {
		return nil, "", err
	}
	out := make([]model.Message, len(rows))
	for i := range rows {
		out[i] = rows[i].Message
	}
	return out, next, nil
}

// ContactPatch is the body of PATCH /contacts/{user}.
type ContactPatch struct {
	Alias   *string `json:"alias"`
	Blocked *bool   `json:"blocked"`
}

// PatchContact sets the owner alias (bridge-local unless the adapter supports contact.alias) and
// the block state (contact.block, always through the platform).
func (c *Core) PatchContact(ctx context.Context, accountID, userID string, p ContactPatch) (model.Contact, error) {
	if p.Alias == nil && p.Blocked == nil {
		return c.GetContact(ctx, accountID, userID)
	}
	if p.Blocked != nil {
		_, ad, err := c.connected(ctx, accountID)
		if err != nil {
			return model.Contact{}, err
		}
		bl, ok := ad.(adapter.Blocker)
		if !ok {
			return model.Contact{}, errUnsupported("contact.block")
		}
		if _, err := c.st.GetContact(ctx, accountID, userID); errors.Is(err, store.ErrNotFound) {
			return model.Contact{}, errNotFound("contact")
		}
		if err := bl.Block(ctx, accountID, userID, *p.Blocked); err != nil {
			return model.Contact{}, err
		}
	}
	var out model.Contact
	err := c.tx(ctx, func(tx *store.Store) error {
		var ct model.Contact
		var err error
		if p.Alias != nil {
			ct, err = tx.SetLocalAlias(ctx, accountID, userID, *p.Alias)
		}
		if err == nil && p.Blocked != nil {
			ct, err = tx.SetBlocked(ctx, accountID, userID, *p.Blocked)
		}
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
