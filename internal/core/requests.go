package core

import (
	"context"
	"errors"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// ListRequests pages an account's invites, join requests and calls (api.md §4.8).
func (c *Core) ListRequests(ctx context.Context, accountID string, f store.RequestFilter, cursor string, limit int) ([]model.Request, string, error) {
	if _, err := c.st.GetAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		return nil, "", errNotFound("account")
	}
	out, next, err := c.st.ListRequests(ctx, accountID, f, cursor, limit)
	if err != nil && cursor != "" && err.Error() == "bad cursor" {
		return nil, "", errInvalid("bad cursor")
	}
	return out, next, err
}

// GetRequest returns one request.
func (c *Core) GetRequest(ctx context.Context, accountID, id string) (model.Request, error) {
	r, err := c.st.GetRequest(ctx, accountID, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Request{}, errNotFound("request")
	}
	return r.Request, err
}

// AnswerRequest accepts or rejects a pending request on the platform, then records the answer.
// Ignore only records it: the platform is not contacted, so other devices still see the request
// (a call keeps ringing there). Calls cannot be accepted.
func (c *Core) AnswerRequest(ctx context.Context, accountID, id, action, reason string) (model.Request, error) {
	if action != model.ActionAccept && action != model.ActionReject && action != model.ActionIgnore {
		return model.Request{}, errInvalid("action must be accept, reject or ignore")
	}
	r, err := c.st.GetRequest(ctx, accountID, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Request{}, errNotFound("request")
	}
	if err != nil {
		return model.Request{}, err
	}
	if r.State != model.RequestPending {
		return model.Request{}, errConflict("request is " + r.State)
	}
	if r.Kind == model.RequestKindCall && action == model.ActionAccept {
		return model.Request{}, errInvalid("calls cannot be accepted from the bridge")
	}
	if action == model.ActionIgnore {
		return c.closeRequest(ctx, accountID, id, model.RequestIgnored)
	}
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return model.Request{}, err
	}
	ans, ok := ad.(adapter.RequestAnswerer)
	if !ok {
		return model.Request{}, errUnsupported("request.answer")
	}
	a := adapter.RequestAnswer{Kind: r.Kind, Key: r.PlatformKey, PlatformRef: r.PlatformRef, Action: action, Reason: reason}
	if r.Chat != nil {
		a.ChatID = r.Chat.ID
	}
	if r.From != nil {
		a.FromID = r.From.ID
	}
	if err := ans.AnswerRequest(ctx, accountID, a); err != nil {
		return model.Request{}, err
	}
	state := model.RequestAccepted
	if action == model.ActionReject {
		state = model.RequestRejected
	}
	return c.closeRequest(ctx, accountID, id, state)
}

// closeRequest records the final state of a request and emits request.updated.
func (c *Core) closeRequest(ctx context.Context, accountID, id, state string) (model.Request, error) {
	now := time.Now()
	var out model.Request
	err := c.tx(ctx, func(tx *store.Store) error {
		upd, err := tx.SetRequestState(ctx, accountID, id, state, &now)
		if err != nil {
			return err
		}
		out = upd
		return emit(ctx, tx, accountID, model.EvRequestUpdated, upd)
	})
	return out, err
}

// ingestRequest stores an adapter-reported request and emits request.new / request.updated.
func (c *Core) ingestRequest(ctx context.Context, tx *store.Store, accountID string, in adapter.Request) error {
	if in.Key == "" || in.Kind == "" {
		return nil
	}
	r := store.StoredRequest{PlatformKey: in.Key, PlatformRef: in.PlatformRef, Request: model.Request{
		AccountID: accountID, Kind: in.Kind, State: in.State, Message: in.Message, CreatedAt: in.CreatedAt, ExpiresAt: in.ExpiresAt}}
	if in.FromID != "" || in.FromName != "" {
		r.From = &model.Sender{ID: in.FromID, Name: in.FromName}
	}
	if in.ChatID != "" || in.ChatName != "" {
		r.Chat = &model.RequestChat{ID: in.ChatID, Name: in.ChatName, Kind: in.ChatKind}
	}
	if in.CallKind != "" {
		r.Call = &model.CallInfo{Kind: in.CallKind}
	}
	out, change, err := tx.UpsertRequest(ctx, r)
	if err != nil {
		return err
	}
	switch change {
	case store.RequestCreated:
		return emit(ctx, tx, accountID, model.EvRequestNew, out)
	case store.RequestUpdated:
		return emit(ctx, tx, accountID, model.EvRequestUpdated, out)
	}
	return nil
}

// expireRequests closes pending requests past their expiry (GC loop).
func (c *Core) expireRequests(ctx context.Context, now time.Time) error {
	return c.tx(ctx, func(tx *store.Store) error {
		expired, err := tx.ExpireRequests(ctx, now)
		if err != nil {
			return err
		}
		for _, r := range expired {
			if err := emit(ctx, tx, r.AccountID, model.EvRequestUpdated, r); err != nil {
				return err
			}
		}
		return nil
	})
}
