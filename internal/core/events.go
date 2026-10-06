package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// bus wakes waiters whenever the event log grows.
type bus struct {
	mu sync.Mutex
	ch chan struct{}
}

func newBus() *bus { return &bus{ch: make(chan struct{})} }

// wait returns a channel closed on the next notify.
func (b *bus) wait() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

func (b *bus) notify() {
	b.mu.Lock()
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

// ListEvents returns events after the cursor.
func (c *Core) ListEvents(ctx context.Context, cursor string, f store.EventFilter, limit int) ([]model.Event, string, error) {
	after, err := store.ParseEventID(cursor)
	if err != nil {
		return nil, "", errInvalid("bad cursor")
	}
	sc, err := c.scopeOf(ctx)
	if err != nil {
		return nil, "", err
	}
	if sc != nil {
		f.Scope = sc.events()
	}
	evs, err := c.st.ListEvents(ctx, after, f, limit)
	if err != nil {
		return nil, "", err
	}
	if sc != nil {
		for i := range evs {
			if evs[i].Type == model.EvAccountStatus {
				evs[i].Data = listViewOnly(evs[i].Data)
			}
		}
	}
	next := cursor
	if len(evs) > 0 {
		next = evs[len(evs)-1].ID
	}
	return evs, next, nil
}

// WaitEvents long-polls: it returns as soon as events exist, or after wait.
func (c *Core) WaitEvents(ctx context.Context, cursor string, f store.EventFilter, limit int, wait time.Duration) ([]model.Event, string, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		wake := c.bus.wait()
		evs, next, err := c.ListEvents(ctx, cursor, f, limit)
		if err != nil || len(evs) > 0 || wait <= 0 {
			return evs, next, err
		}
		select {
		case <-wake:
		case <-deadline.C:
			return evs, next, nil
		case <-ctx.Done():
			return evs, next, nil
		}
	}
}

// Stream pushes events to fn until ctx ends. fn returning an error stops the stream.
func (c *Core) Stream(ctx context.Context, cursor string, f store.EventFilter, fn func(model.Event) error) error {
	for {
		wake := c.bus.wait()
		evs, next, err := c.ListEvents(ctx, cursor, f, 100)
		if err != nil {
			return err
		}
		for _, ev := range evs {
			if err := fn(ev); err != nil {
				return err
			}
		}
		cursor = next
		if len(evs) == 100 {
			continue
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return nil
		}
	}
}

// LastEventID returns the newest cursor.
func (c *Core) LastEventID(ctx context.Context) string {
	id, _ := c.st.LastEventID(ctx)
	return store.FormatEventID(id)
}

// WebhookInput is the body of POST /webhooks.
type WebhookInput struct {
	URL       string   `json:"url"`
	Secret    string   `json:"secret"`
	AccountID string   `json:"account,omitempty"`
	Types     []string `json:"types,omitempty"`
	Cursor    string   `json:"cursor,omitempty"`
}

// CreateWebhook registers a subscription starting at the current cursor unless one is given.
func (c *Core) CreateWebhook(ctx context.Context, in WebhookInput) (model.Webhook, error) {
	if in.URL == "" || in.Secret == "" {
		return model.Webhook{}, errInvalid("url and secret are required")
	}
	cursor := in.Cursor
	if cursor == "" {
		cursor = c.LastEventID(ctx)
	}
	w := model.Webhook{ID: "whk_" + uuid.NewString(), URL: in.URL, Secret: in.Secret, AccountID: in.AccountID, Types: in.Types, Cursor: cursor}
	if err := c.st.CreateWebhook(ctx, w); err != nil {
		return model.Webhook{}, err
	}
	c.bus.notify()
	w.CreatedAt = time.Now().UTC()
	return w, nil
}

// ListWebhooks returns subscriptions.
func (c *Core) ListWebhooks(ctx context.Context) ([]model.Webhook, error) {
	ws, err := c.st.ListWebhooks(ctx)
	if ws == nil {
		ws = []model.Webhook{}
	}
	return ws, err
}

// DeleteWebhook removes a subscription.
func (c *Core) DeleteWebhook(ctx context.Context, id string) error {
	err := c.st.DeleteWebhook(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("webhook")
	}
	return err
}

const (
	webhookBatch    = 100
	webhookTimeout  = 10 * time.Second
	webhookMaxTries = 20 // ~24h of exponential backoff capped at 1h
)

// webhookLoop delivers pending events to each subscription.
func (c *Core) webhookLoop() {
	defer c.wg.Done()
	for {
		wake := c.bus.wait()
		c.deliverAll()
		select {
		case <-c.stopCh:
			return
		case <-wake:
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Core) deliverAll() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	due, err := c.st.DueWebhooks(ctx, time.Now())
	if err != nil {
		c.log.Warn("due webhooks", "err", err)
		return
	}
	for _, w := range due {
		c.deliverOne(ctx, w)
	}
}

func (c *Core) deliverOne(ctx context.Context, w store.WebhookDelivery) {
	f := store.EventFilter{AccountID: w.AccountID, Types: w.Types}
	cursor := w.CursorN
	oldest, _ := c.st.OldestEventID(ctx)
	gap := false
	if cursor > 0 && oldest > cursor+1 {
		cursor, gap = oldest-1, true
	}
	evs, err := c.st.ListEvents(ctx, cursor, f, webhookBatch)
	if err != nil || len(evs) == 0 {
		return
	}
	payload := map[string]any{"events": evs}
	if gap {
		payload["gap"] = true
	}
	body, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, []byte(w.Secret))
	mac.Write(body)
	last := evs[len(evs)-1].ID

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		c.failWebhook(ctx, w)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ChatBridge-Delivery", uuid.NewString())
	req.Header.Set("X-ChatBridge-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-ChatBridge-Cursor", last)
	resp, err := c.httpc.Do(req)
	if err != nil {
		c.failWebhook(ctx, w)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.failWebhook(ctx, w)
		return
	}
	n, _ := store.ParseEventID(last)
	if err := c.st.AckWebhook(ctx, w.ID, n); err != nil {
		c.log.Warn("ack webhook", "id", w.ID, "err", err)
	}
	if len(evs) == webhookBatch {
		c.bus.notify()
	}
}

func (c *Core) failWebhook(ctx context.Context, w store.WebhookDelivery) {
	backoff := 5 * time.Second << uint(min(w.Failures, 10))
	if backoff > time.Hour {
		backoff = time.Hour
	}
	pause := w.Failures+1 >= webhookMaxTries
	if err := c.st.FailWebhook(ctx, w.ID, time.Now().Add(backoff), pause); err != nil {
		c.log.Warn("fail webhook", "id", w.ID, "err", err)
	}
	c.log.Warn("webhook delivery failed", "id", w.ID, "failures", w.Failures+1, "paused", pause)
}

// Status is the process-level view for GET /v1/status.
type Status struct {
	Version      string          `json:"version"`
	UptimeS      int64           `json:"uptime_s"`
	Accounts     []model.Account `json:"accounts"`
	EventsCursor string          `json:"events_cursor"`
}

var started = time.Now()

// Ready reports whether the store answers; it backs GET /readyz.
func (c *Core) Ready(ctx context.Context) error { return c.st.Ping(ctx) }

// Status builds the process view.
func (c *Core) Status(ctx context.Context, version string) (Status, error) {
	accs, err := c.ListAccounts(ctx)
	if err != nil {
		return Status{}, err
	}
	if accs == nil {
		accs = []model.Account{}
	}
	return Status{Version: version, UptimeS: int64(time.Since(started).Seconds()), Accounts: accs, EventsCursor: c.LastEventID(ctx)}, nil
}

// listViewOnly strips the fields of an account payload that the list view leaves out, so a
// scoped token never sees configuration or device detail in account.status events.
func listViewOnly(data json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return json.RawMessage("{}")
	}
	delete(m, "config")
	delete(m, "device")
	delete(m, "stats")
	b, _ := json.Marshal(m)
	return b
}
