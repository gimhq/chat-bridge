package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

var accountIDRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// loginState is the in-memory side of one account's login machine.
type loginState struct {
	flow       string
	identifier string
	step       model.LoginStep
}

// ListAccounts returns the list view (no config/device/stats). A scoped token gets the accounts
// its scope touches.
func (c *Core) ListAccounts(ctx context.Context) ([]model.Account, error) {
	rows, err := c.st.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	sc, err := c.scopeOf(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Account, 0, len(rows))
	for _, r := range rows {
		if sc != nil && !sc.accounts[r.ID] {
			continue
		}
		a, err := c.accountView(ctx, r, false)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// GetAccount returns the full view; a scoped token gets the list view of an account in its scope.
func (c *Core) GetAccount(ctx context.Context, id string) (model.Account, error) {
	if err := c.allowAccount(ctx, id); err != nil {
		return model.Account{}, err
	}
	row, err := c.st.GetAccount(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Account{}, errNotFound("account")
	}
	if err != nil {
		return model.Account{}, err
	}
	return c.accountView(ctx, row, !Scoped(ctx))
}

func (c *Core) accountView(ctx context.Context, row store.AccountRow, full bool) (model.Account, error) {
	a := model.Account{
		ID: row.ID, Platform: row.Platform, Adapter: row.Adapter, Status: row.Status, Login: row.Login, Error: row.Error,
		CreatedAt: row.CreatedAt, ConnectedAt: row.ConnectedAt, Capabilities: []string{},
	}
	var schema json.RawMessage
	if ad, err := c.resolve(row); err == nil {
		info := ad.Info()
		a.Capabilities = info.Capabilities
		schema = info.ConfigSchema
		if a.Adapter == "" {
			a.Adapter = instanceOf(info)
		}
	}
	if row.SelfID != "" {
		if self, err := c.st.GetContact(ctx, row.ID, row.SelfID); err == nil {
			a.Self = &self
		}
	}
	if !full {
		return a, nil
	}
	a.Config = redactConfig(row.Config, schema)
	a.Device = row.Device
	st, err := c.st.AccountStats(ctx, row.ID)
	if err != nil {
		return a, err
	}
	a.Stats = &st
	return a, nil
}

// CreateAccount registers a new account with an adapter instance of its platform. instance may
// be empty when exactly one adapter serves the platform.
func (c *Core) CreateAccount(ctx context.Context, id, platform, instance string, cfg json.RawMessage) (model.Account, error) {
	if !accountIDRe.MatchString(id) {
		return model.Account{}, errInvalid("id must match %s", accountIDRe.String())
	}
	all := c.instances(platform)
	var ad adapter.Adapter // nil = provisioned ahead of its adapter
	switch {
	case instance != "":
		ad, _ = c.adapter(platform, instance)
	case len(all) == 0:
		return model.Account{}, errInvalid("no adapter connected for %q; pass \"adapter\" to provision the account ahead of it", platform)
	case len(all) == 1:
		ad = all[0]
	default:
		names := make([]string, 0, len(all))
		for _, a := range all {
			names = append(names, instanceOf(a.Info()))
		}
		return model.Account{}, errInvalid("platform %s has several adapters (%s); set \"adapter\"", platform, strings.Join(names, ", "))
	}
	if ad != nil {
		instance = instanceOf(ad.Info())
	}
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	if err := c.st.CreateAccount(ctx, id, platform, instance, cfg); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return model.Account{}, errConflict("account id already exists")
		}
		return model.Account{}, err
	}
	if ad == nil {
		// The account exists on its own; the adapter picks it up when it connects.
		_ = c.st.SetAccountStatus(ctx, id, model.StatusError, "", nil, &model.Error{Code: "adapter_offline", Message: "adapter " + platform + "/" + instance + " is not connected"})
		return c.GetAccount(ctx, id)
	}
	if err := ad.AddAccount(ctx, id, cfg, c.accountDir(id)); err != nil {
		_ = c.st.DeleteAccount(ctx, id)
		return model.Account{}, err
	}
	return c.GetAccount(ctx, id)
}

// Rebind moves an unpaired account to another adapter instance of the same platform.
func (c *Core) Rebind(ctx context.Context, id, instance string) (model.Account, error) {
	row, err := c.st.GetAccount(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Account{}, errNotFound("account")
	}
	if err != nil {
		return model.Account{}, err
	}
	if row.Status != model.StatusUnpaired && row.Status != model.StatusError {
		return model.Account{}, errConflict("logout before moving the account to another adapter")
	}
	next, ok := c.adapter(row.Platform, instance)
	if !ok {
		return model.Account{}, errInvalid("no adapter %s/%s is connected", row.Platform, instance)
	}
	if prev, ok := c.adapter(row.Platform, row.Adapter); ok && row.Adapter != instance {
		_ = prev.RemoveAccount(ctx, id)
	}
	if err := c.st.SetAccountAdapter(ctx, id, instance); err != nil {
		return model.Account{}, err
	}
	if err := next.AddAccount(ctx, id, row.Config, c.accountDir(id)); err != nil {
		return model.Account{}, err
	}
	return c.GetAccount(ctx, id)
}

// UpdateConfig replaces config; secret fields omitted by the caller keep their old value.
func (c *Core) UpdateConfig(ctx context.Context, id string, cfg json.RawMessage) (model.Account, error) {
	row, err := c.st.GetAccount(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Account{}, errNotFound("account")
	}
	if err != nil {
		return model.Account{}, err
	}
	ad, _ := c.resolve(row) // nil while the adapter is offline: the config is stored and applied on attach
	var old, next map[string]any
	_ = json.Unmarshal(row.Config, &old)
	if err := json.Unmarshal(cfg, &next); err != nil {
		return model.Account{}, errInvalid("config must be an object")
	}
	if old == nil {
		old = map[string]any{}
	}
	for k, v := range next {
		old[k] = v
	}
	merged, _ := json.Marshal(old)
	if err := c.st.SetAccountConfig(ctx, id, merged); err != nil {
		return model.Account{}, err
	}
	if ad != nil {
		if err := ad.AddAccount(ctx, id, merged, c.accountDir(id)); err != nil {
			return model.Account{}, err
		}
	}
	return c.GetAccount(ctx, id)
}

// DeleteAccount removes every stored row and, when its adapter is connected, the adapter state.
func (c *Core) DeleteAccount(ctx context.Context, id string) error {
	row, err := c.st.GetAccount(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("account")
	}
	if err != nil {
		return err
	}
	if ad, err := c.resolve(row); err == nil {
		if err := ad.RemoveAccount(ctx, id); err != nil {
			c.log.Warn("adapter remove account", "account", id, "err", err)
		}
	} else {
		c.log.Warn("deleting account without its adapter; platform-side session is not revoked", "account", id)
	}
	c.mu.Lock()
	delete(c.logins, id)
	c.mu.Unlock()
	if err := c.st.DeleteAccount(ctx, id); err != nil {
		return err
	}
	return os.RemoveAll(c.accountDir(id))
}

// Logout ends the session and returns the account to unpaired.
func (c *Core) Logout(ctx context.Context, id string) (model.Account, error) {
	_, ad, err := c.adapterFor(ctx, id)
	if err != nil {
		return model.Account{}, err
	}
	if err := ad.Logout(ctx, id); err != nil {
		return model.Account{}, err
	}
	if err := c.tx(ctx, func(tx *store.Store) error {
		if err := tx.ClearAccountSession(ctx, id); err != nil {
			return err
		}
		return c.emitAccountStatus(ctx, tx, id)
	}); err != nil {
		return model.Account{}, err
	}
	return c.GetAccount(ctx, id)
}

// Reconnect forces the adapter to reconnect.
func (c *Core) Reconnect(ctx context.Context, id string) (model.Account, error) {
	row, ad, err := c.adapterFor(ctx, id)
	if err != nil {
		return model.Account{}, err
	}
	if row.Status == model.StatusUnpaired {
		return model.Account{}, errNotReady(row.Status)
	}
	if err := ad.Reconnect(ctx, id); err != nil {
		return model.Account{}, err
	}
	return c.GetAccount(ctx, id)
}

func (c *Core) emitAccountStatus(ctx context.Context, tx *store.Store, id string) error {
	row, err := tx.GetAccount(ctx, id)
	if err != nil {
		return err
	}
	view, err := (&Core{st: tx, adapters: c.adapters}).accountView(ctx, row, false)
	if err != nil {
		return err
	}
	return emit(ctx, tx, id, model.EvAccountStatus, view)
}

// --- login machine ---

// LoginStart begins a flow.
func (c *Core) LoginStart(ctx context.Context, id, flow string) (model.LoginStep, error) {
	row, ad, err := c.adapterFor(ctx, id)
	if err != nil {
		return model.LoginStep{}, err
	}
	if row.Status != model.StatusUnpaired && row.Status != model.StatusError {
		return model.LoginStep{}, errConflict("account is " + row.Status + "; logout first")
	}
	if !hasFlow(ad, flow) {
		return model.LoginStep{}, errInvalid("unknown login flow %q", flow)
	}
	c.mu.Lock()
	if _, busy := c.logins[id]; busy {
		c.mu.Unlock()
		return model.LoginStep{}, errConflict("login already in progress")
	}
	c.logins[id] = &loginState{flow: flow}
	c.mu.Unlock()

	step, err := ad.LoginStart(ctx, id, flow)
	if err != nil {
		c.clearLogin(id)
		return model.LoginStep{}, err
	}
	_ = c.st.SetAccountStatus(ctx, id, model.StatusLoggingIn, "", nil, nil)
	return step, c.applyStep(ctx, id, step)
}

// LoginSubmit feeds fields into the current flow.
func (c *Core) LoginSubmit(ctx context.Context, id string, fields map[string]string) (model.LoginStep, error) {
	_, ad, err := c.adapterFor(ctx, id)
	if err != nil {
		return model.LoginStep{}, err
	}
	c.mu.Lock()
	ls, ok := c.logins[id]
	if ok {
		for _, k := range []string{"phone", "user", "username", "token"} {
			if v := fields[k]; v != "" && ls.identifier == "" {
				ls.identifier = v
			}
		}
	}
	c.mu.Unlock()
	if !ok {
		return model.LoginStep{}, errConflict("no login in progress")
	}
	step, err := ad.LoginSubmit(ctx, id, fields)
	if err != nil {
		return model.LoginStep{}, err
	}
	return step, c.applyStep(ctx, id, step)
}

// LoginGet returns the current step, refreshing rotating displays.
func (c *Core) LoginGet(ctx context.Context, id string) (model.LoginStep, error) {
	_, ad, err := c.adapterFor(ctx, id)
	if err != nil {
		return model.LoginStep{}, err
	}
	c.mu.Lock()
	_, ok := c.logins[id]
	c.mu.Unlock()
	if !ok {
		return model.LoginStep{}, errNotFound("login")
	}
	step, err := ad.LoginRefresh(ctx, id)
	if err != nil {
		return model.LoginStep{}, err
	}
	return step, c.applyStep(ctx, id, step)
}

// LoginCancel aborts the flow.
func (c *Core) LoginCancel(ctx context.Context, id string) error {
	_, ad, err := c.adapterFor(ctx, id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	_, ok := c.logins[id]
	c.mu.Unlock()
	if !ok {
		return errNotFound("login")
	}
	if err := ad.LoginCancel(ctx, id); err != nil {
		return err
	}
	c.clearLogin(id)
	return c.tx(ctx, func(tx *store.Store) error {
		if err := tx.ClearAccountSession(ctx, id); err != nil {
			return err
		}
		return c.emitAccountStatus(ctx, tx, id)
	})
}

func (c *Core) clearLogin(id string) {
	c.mu.Lock()
	delete(c.logins, id)
	c.mu.Unlock()
}

// applyStep records a step from the adapter (returned or pushed) and finalises done/failed.
func (c *Core) applyStep(ctx context.Context, id string, step model.LoginStep) error {
	c.mu.Lock()
	ls, ok := c.logins[id]
	if ok {
		ls.step = step
		if step.Flow == "" {
			step.Flow = ls.flow
		}
	}
	c.mu.Unlock()
	if !ok {
		return nil
	}
	return c.tx(ctx, func(tx *store.Store) error {
		switch step.Step {
		case model.StepDone:
			if step.Self != nil {
				self := *step.Self
				self.IsSelf, self.IsContact = true, true
				if _, err := tx.UpsertContact(ctx, id, self); err != nil {
					return err
				}
				if err := tx.SetAccountStatus(ctx, id, model.StatusConnecting, self.ID, nil, nil); err != nil {
					return err
				}
			}
			if err := tx.SetAccountLogin(ctx, id, model.LoginRecord{Flow: ls.flow, Identifier: ls.identifier, At: time.Now().UTC()}); err != nil {
				return err
			}
			c.clearLogin(id)
		case model.StepFailed:
			c.clearLogin(id)
			if err := tx.SetAccountStatus(ctx, id, model.StatusUnpaired, "", nil, step.Error); err != nil {
				return err
			}
		}
		if err := emit(ctx, tx, id, model.EvLoginStep, step); err != nil {
			return err
		}
		if step.Step == model.StepDone || step.Step == model.StepFailed {
			return c.emitAccountStatus(ctx, tx, id)
		}
		return nil
	})
}

func hasFlow(ad adapter.Adapter, flow string) bool {
	for _, f := range ad.Info().LoginFlows {
		if f.ID == flow {
			return true
		}
	}
	return false
}

// SelfPatch is the body of PATCH /accounts/{a}/self.
type SelfPatch struct {
	Name          *string `json:"name"`
	Bio           *string `json:"bio"`
	AvatarMediaID string  `json:"avatar_media_id"`
}

// UpdateSelf changes the account's profile on the platform (self.update) and refreshes Account.self.
func (c *Core) UpdateSelf(ctx context.Context, accountID string, p SelfPatch) (model.Account, error) {
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return model.Account{}, err
	}
	su, ok := ad.(adapter.SelfUpdater)
	if !ok || !ad.Info().Has(adapter.CapSelfUpdate) {
		return model.Account{}, errUnsupported(adapter.CapSelfUpdate)
	}
	if p.Name == nil && p.Bio == nil && p.AvatarMediaID == "" {
		return model.Account{}, errInvalid("nothing to update")
	}
	if p.AvatarMediaID != "" {
		md, err := c.st.GetMedia(ctx, p.AvatarMediaID)
		if errors.Is(err, store.ErrNotFound) {
			return model.Account{}, errNotFound("media " + p.AvatarMediaID)
		}
		if err != nil {
			return model.Account{}, err
		}
		if md.State != model.MediaReady {
			return model.Account{}, errInvalid("media %s is %s", md.ID, md.State)
		}
	}
	self, err := su.UpdateSelf(ctx, accountID, adapter.SelfUpdate{Name: p.Name, Bio: p.Bio, AvatarMediaID: p.AvatarMediaID, Media: &mediaSource{c: c}})
	if err != nil {
		return model.Account{}, err
	}
	self.IsSelf, self.IsContact = true, true
	err = c.tx(ctx, func(tx *store.Store) error {
		if _, err := tx.UpsertContact(ctx, accountID, self); err != nil {
			return err
		}
		return c.emitAccountStatus(ctx, tx, accountID)
	})
	if err != nil {
		return model.Account{}, err
	}
	return c.GetAccount(ctx, accountID)
}
