package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gimhq/chat-bridge/internal/model"
)

const webhookCols = `id, url, secret, COALESCE(account_id,''), types, cursor, failures, next_try, paused_at, created_at`

// CreateWebhook stores a subscription.
func (s *Store) CreateWebhook(ctx context.Context, w model.Webhook) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO webhooks (id, url, secret, account_id, types, cursor, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.URL, w.Secret, nullStr(w.AccountID), jsonOrNull(typesOrNil(w.Types)), mustParseCursor(w.Cursor), unix(time.Now()))
	if err != nil {
		return fmt.Errorf("create webhook: %w", err)
	}
	return nil
}

func typesOrNil(t []string) any {
	if len(t) == 0 {
		return nil
	}
	return t
}

func mustParseCursor(c string) int64 {
	n, _ := ParseEventID(c)
	return n
}

// ListWebhooks returns every subscription.
func (s *Store) ListWebhooks(ctx context.Context) ([]model.Webhook, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+webhookCols+` FROM webhooks ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []model.Webhook
	for rows.Next() {
		w, _, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WebhookDelivery is a subscription with its scheduling state.
type WebhookDelivery struct {
	model.Webhook
	CursorN int64
	NextTry *time.Time
}

// DueWebhooks returns unpaused subscriptions whose retry time has passed.
func (s *Store) DueWebhooks(ctx context.Context, now time.Time) ([]WebhookDelivery, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE paused_at IS NULL AND (next_try IS NULL OR next_try <= ?)`, unix(now))
	if err != nil {
		return nil, fmt.Errorf("due webhooks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []WebhookDelivery
	for rows.Next() {
		w, d, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		d.Webhook = w
		out = append(out, d)
	}
	return out, rows.Err()
}

// AckWebhook advances the cursor after a 2xx.
func (s *Store) AckWebhook(ctx context.Context, id string, cursor int64) error {
	_, err := s.q.ExecContext(ctx, `UPDATE webhooks SET cursor = ?, failures = 0, next_try = NULL WHERE id = ?`, cursor, id)
	if err != nil {
		return fmt.Errorf("ack webhook: %w", err)
	}
	return nil
}

// FailWebhook records a failed delivery and schedules the retry (or pauses).
func (s *Store) FailWebhook(ctx context.Context, id string, nextTry time.Time, pause bool) error {
	var paused any
	if pause {
		paused = unix(time.Now())
	}
	_, err := s.q.ExecContext(ctx, `UPDATE webhooks SET failures = failures + 1, next_try = ?, paused_at = ? WHERE id = ?`, unix(nextTry), paused, id)
	if err != nil {
		return fmt.Errorf("fail webhook: %w", err)
	}
	return nil
}

// DeleteWebhook removes a subscription.
func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanWebhook(row scanner) (model.Webhook, WebhookDelivery, error) {
	var w model.Webhook
	var d WebhookDelivery
	var types sql.NullString
	var nextTry, paused sql.NullInt64
	var created int64
	if err := row.Scan(&w.ID, &w.URL, &w.Secret, &w.AccountID, &types, &d.CursorN, &w.Failures, &nextTry, &paused, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return w, d, ErrNotFound
		}
		return w, d, err
	}
	if types.Valid {
		_ = json.Unmarshal([]byte(types.String), &w.Types)
	}
	w.Cursor = FormatEventID(d.CursorN)
	w.PausedAt = timePtr(paused)
	w.CreatedAt = fromUnix(created)
	d.NextTry = timePtr(nextTry)
	return w, d, nil
}
