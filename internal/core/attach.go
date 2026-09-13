package core

import (
	"context"
	"errors"
	"fmt"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// Attach registers an adapter after Start (out-of-process adapters connect at any time), starts
// it with the core sink, and hands it every account of its platform.
func (c *Core) Attach(ctx context.Context, a adapter.Adapter) error {
	key := registryKey(a.Info())
	c.amu.Lock()
	if _, dup := c.adapters[key]; dup {
		c.amu.Unlock()
		return fmt.Errorf("adapter %s already attached", key)
	}
	c.adapters[key] = a
	c.amu.Unlock()
	if err := a.Start(ctx, &sink{c: c}); err != nil {
		c.Detach(ctx, a.Info().Platform, instanceOf(a.Info()))
		return err
	}
	c.attachAccounts(ctx, a)
	return nil
}

// Detach removes an adapter instance; its accounts read disconnected / adapter_offline until it
// returns.
func (c *Core) Detach(ctx context.Context, platform, instance string) {
	c.amu.Lock()
	delete(c.adapters, platform+"/"+instance)
	c.amu.Unlock()
	for _, row := range c.PlatformAccounts(ctx, platform, instance) {
		if row.Status == model.StatusUnpaired {
			continue
		}
		_ = c.tx(ctx, func(tx *store.Store) error {
			if err := tx.SetAccountStatus(ctx, row.ID, model.StatusDisconnected, "", nil,
				&model.Error{Code: "adapter_offline", Message: "adapter " + platform + "/" + instance + " is not connected"}); err != nil {
				return err
			}
			return c.emitAccountStatus(ctx, tx, row.ID)
		})
	}
}

// attachAccounts hands the adapter its bound accounts. Accounts of the platform that are not
// bound yet are bound to this instance when it is the only one.
func (c *Core) attachAccounts(ctx context.Context, a adapter.Adapter) {
	info := a.Info()
	instance := instanceOf(info)
	rows := c.PlatformAccounts(ctx, info.Platform, instance)
	if len(c.instances(info.Platform)) == 1 {
		for _, row := range c.PlatformAccounts(ctx, info.Platform, "") {
			if err := c.st.SetAccountAdapter(ctx, row.ID, instance); err == nil {
				row.Adapter = instance
				rows = append(rows, row)
			}
		}
	}
	for _, row := range rows {
		if row.Status != model.StatusUnpaired {
			_ = c.st.SetAccountStatus(ctx, row.ID, model.StatusConnecting, "", nil, nil)
		}
		if err := a.AddAccount(ctx, row.ID, row.Config, c.accountDir(row.ID)); err != nil {
			c.log.Error("add account to adapter", "account", row.ID, "err", err)
			_ = c.st.SetAccountStatus(ctx, row.ID, model.StatusError, "", nil, &model.Error{Code: "adapter_error", Message: err.Error()})
		}
	}
}

// PlatformAccounts lists stored accounts of one platform bound to instance ("" = unbound).
func (c *Core) PlatformAccounts(ctx context.Context, platform, instance string) []store.AccountRow {
	rows, err := c.st.ListAccounts(ctx)
	if err != nil {
		c.log.Error("list accounts", "err", err)
		return nil
	}
	out := rows[:0]
	for _, r := range rows {
		if r.Platform == platform && r.Adapter == instance {
			out = append(out, r)
		}
	}
	return out
}

// Sink exposes the core's adapter sink to transports that host adapters outside the process.
func (c *Core) Sink() adapter.Sink { return &sink{c: c} }

// AccountDir returns the adapter-private directory of an account.
func (c *Core) AccountDir(id string) string { return c.accountDir(id) }

// MarkMediaFailed records a failed download reported by an adapter.
func (c *Core) MarkMediaFailed(ctx context.Context, mediaID string) error {
	return c.tx(ctx, func(tx *store.Store) error {
		md, err := tx.GetMedia(ctx, mediaID)
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("media")
		}
		if err != nil {
			return err
		}
		md.State = model.MediaFailed
		return tx.UpsertMedia(ctx, md)
	})
}
