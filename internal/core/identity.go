package core

import (
	"context"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// mergedChat is the chat.updated payload after a direct chat moved to a new id.
type mergedChat struct {
	model.Chat
	MergedFrom string `json:"merged_from"`
}

// reID moves what the account stores under oldID to newID inside tx and emits the resulting
// chat and contact updates.
func (c *Core) reID(ctx context.Context, tx *store.Store, accountID, oldID, newID string) error {
	res, err := tx.ReID(ctx, accountID, oldID, newID)
	if err != nil || !res.Changed {
		return err
	}
	c.log.Info("identity changed", "account", accountID, "old", oldID, "new", newID)
	if res.Chat {
		ch, err := tx.GetChat(ctx, accountID, newID)
		if err != nil {
			return err
		}
		named := []model.Chat{ch}
		if err := tx.NameDirectChats(ctx, named); err != nil {
			return err
		}
		if err := emit(ctx, tx, accountID, model.EvChatUpdated, mergedChat{Chat: named[0], MergedFrom: oldID}); err != nil {
			return err
		}
	}
	if !res.Contact {
		return nil
	}
	ct, err := tx.GetContact(ctx, accountID, newID)
	if err != nil {
		return err
	}
	if err := emit(ctx, tx, accountID, model.EvContactUpdated, ct); err != nil {
		return err
	}
	return c.autoLink(ctx, tx, accountID, newID)
}

// reconcileIdentities asks the adapter for the current form of every stored user and direct-chat
// id and re-IDs the ones that changed (e.g. WhatsApp phone numbers that now have a LID).
func (c *Core) reconcileIdentities(ctx context.Context, accountID string, ad adapter.Adapter) {
	r, ok := ad.(adapter.IdentityResolver)
	if !ok {
		return
	}
	ids, err := c.st.IdentityCandidates(ctx, accountID)
	if err != nil || len(ids) == 0 {
		return
	}
	mapped, err := r.CanonicalIDs(ctx, accountID, ids)
	if err != nil {
		if adapter.CodeOf(err) != adapter.ErrUnsupported {
			c.log.Warn("resolve identities", "account", accountID, "err", err)
		}
		return
	}
	if len(mapped) == 0 {
		return
	}
	err = c.tx(ctx, func(tx *store.Store) error {
		for oldID, newID := range mapped {
			if err := c.reID(ctx, tx, accountID, oldID, newID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.log.Error("reconcile identities", "account", accountID, "err", err)
		return
	}
	c.log.Info("identities reconciled", "account", accountID, "changed", len(mapped), "at", time.Now().UTC())
}
