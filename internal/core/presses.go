package core

import (
	"context"
	"net/http"
	"sync"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// pressForget is how long an answered or expired press is remembered, so a late answer gets 409.
const pressForget = 10 * time.Minute

// press is one button press waiting for an answer. Presses live in memory only.
type press struct {
	chatID string
	state  string // pending|answered|expired
}

type pressKey struct{ account, id string }

// presses tracks button presses until they are answered or time out.
type presses struct {
	mu sync.Mutex
	m  map[pressKey]*press
}

// ingestPress logs a message.button event and starts the press's answer timer.
func (c *Core) ingestPress(ctx context.Context, tx *store.Store, accountID string, ev adapter.Event) error {
	if ev.PressID == "" || ev.ChatID == "" {
		return nil
	}
	if ev.Sender != nil && ev.Sender.ID == ev.UserID {
		changed, err := tx.UpsertContact(ctx, accountID, *ev.Sender)
		if err != nil {
			return err
		}
		if changed {
			if err := c.autoLink(ctx, tx, accountID, ev.UserID); err != nil {
				return err
			}
		}
	}
	sender := model.Sender{ID: ev.UserID, Name: ev.UserID}
	if ct, err := tx.GetContact(ctx, accountID, ev.UserID); err == nil {
		sender.Name = store.ResolveName(ct, tx.MemberChatName(ctx, accountID, ev.ChatID, ev.UserID))
	}
	at := ev.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	c.trackPress(accountID, ev.PressID, ev.ChatID)
	return emit(ctx, tx, accountID, model.EvMessageButton, model.ButtonPress{
		ChatID: ev.ChatID, MessageID: ev.MessageID, UserID: ev.UserID, Sender: sender, Data: ev.Data, PressID: ev.PressID, PressedAt: at})
}

// trackPress records a pending press; if nobody answers it in time, the core answers it with no
// text so the user's client stops waiting.
func (c *Core) trackPress(accountID, pressID, chatID string) {
	k := pressKey{accountID, pressID}
	c.presses.mu.Lock()
	c.presses.m[k] = &press{chatID: chatID, state: "pending"}
	c.presses.mu.Unlock()
	time.AfterFunc(c.pressTimeout, func() {
		if c.finishPress(k, "expired") != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pa, ok := c.pressAnswerer(ctx, accountID)
		if !ok {
			c.log.Warn("answer expired press: no answerer", "account", accountID, "press", pressID)
			return
		}
		if err := pa.AnswerPress(ctx, accountID, pressID, "", false); err != nil {
			c.log.Warn("answer expired press", "account", accountID, "press", pressID, "err", err)
			return
		}
		c.log.Debug("answered expired press", "account", accountID, "press", pressID)
	})
}

// finishPress moves a pending press to state and forgets it later; it fails when the press is not
// pending any more.
func (c *Core) finishPress(k pressKey, state string) error {
	c.presses.mu.Lock()
	defer c.presses.mu.Unlock()
	p, ok := c.presses.m[k]
	switch {
	case !ok:
		return errNotFound("press")
	case p.state == "answered":
		return &Error{Status: http.StatusConflict, Code: "press_answered", Message: "press was already answered"}
	case p.state == "expired":
		return &Error{Status: http.StatusConflict, Code: "press_expired", Message: "press was answered automatically after the timeout"}
	}
	p.state = state
	time.AfterFunc(pressForget, func() {
		c.presses.mu.Lock()
		delete(c.presses.m, k)
		c.presses.mu.Unlock()
	})
	return nil
}

func (c *Core) pressAnswerer(ctx context.Context, accountID string) (adapter.PressAnswerer, bool) {
	row, err := c.st.GetAccount(ctx, accountID)
	if err != nil {
		return nil, false
	}
	ad, err := c.resolve(row)
	if err != nil {
		return nil, false
	}
	pa, ok := ad.(adapter.PressAnswerer)
	return pa, ok
}

// AnswerPress answers a button press once: the text is shown to the user who pressed.
func (c *Core) AnswerPress(ctx context.Context, accountID, pressID string, ans model.PressAnswer) error {
	if err := c.allowWrite(ctx); err != nil {
		return err
	}
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return err
	}
	pa, ok := ad.(adapter.PressAnswerer)
	if !ok || !can(ad, accountID, adapter.CapButtons) {
		return errUnsupported(adapter.CapButtons)
	}
	k := pressKey{accountID, pressID}
	c.presses.mu.Lock()
	p, found := c.presses.m[k]
	chatID := ""
	if found {
		chatID = p.chatID
	}
	c.presses.mu.Unlock()
	if !found {
		return errNotFound("press")
	}
	if err := c.allowChat(ctx, accountID, chatID); err != nil {
		return err
	}
	if err := c.finishPress(k, "answered"); err != nil {
		return err
	}
	return pa.AnswerPress(ctx, accountID, pressID, ans.Text, ans.Alert)
}
