package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gimhq/chat-bridge/internal/model"
)

// FormatEventID renders a cursor as the zero-padded string used on the wire.
func FormatEventID(id int64) string { return fmt.Sprintf("%016d", id) }

// ParseEventID accepts the wire form or a bare integer. Empty means 0.
func ParseEventID(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(strings.TrimLeft(s, "0"), 10, 64)
	if err != nil {
		if strings.Trim(s, "0") == "" {
			return 0, nil
		}
		return 0, fmt.Errorf("bad event cursor")
	}
	return n, nil
}

// AppendEvent stores one event and returns its cursor.
func (s *Store) AppendEvent(ctx context.Context, accountID, typ string, data any) (int64, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return 0, fmt.Errorf("marshal event: %w", err)
	}
	res, err := s.q.ExecContext(ctx, `INSERT INTO events (account_id, type, ts_ms, data) VALUES (?, ?, ?, ?)`,
		nullStr(accountID), typ, time.Now().UnixMilli(), string(b))
	if err != nil {
		return 0, fmt.Errorf("append event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// EventFilter narrows ListEvents.
type EventFilter struct {
	AccountID string
	Types     []string
}

// ListEvents returns events with id > after, oldest first.
func (s *Store) ListEvents(ctx context.Context, after int64, f EventFilter, limit int) ([]model.Event, error) {
	where := `id > ?`
	args := []any{after}
	if f.AccountID != "" {
		where += ` AND account_id = ?`
		args = append(args, f.AccountID)
	}
	if len(f.Types) > 0 {
		where += ` AND type IN (` + strings.TrimSuffix(strings.Repeat("?,", len(f.Types)), ",") + `)`
		for _, t := range f.Types {
			args = append(args, t)
		}
	} else {
		where += ` AND type <> ?`
		args = append(args, model.EvPlatform)
	}
	args = append(args, limit)
	rows, err := s.q.QueryContext(ctx, `SELECT id, COALESCE(account_id,''), type, ts_ms, data FROM events WHERE `+where+` ORDER BY id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]model.Event, 0, limit)
	for rows.Next() {
		var id, ts int64
		var e model.Event
		var data string
		if err := rows.Scan(&id, &e.AccountID, &e.Type, &ts, &data); err != nil {
			return nil, err
		}
		e.ID = FormatEventID(id)
		e.Timestamp = time.UnixMilli(ts).UTC()
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastEventID returns the newest cursor.
func (s *Store) LastEventID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.q.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("last event: %w", err)
	}
	return id, nil
}

// OldestEventID returns the smallest surviving cursor (0 when empty).
func (s *Store) OldestEventID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.q.QueryRowContext(ctx, `SELECT COALESCE(MIN(id),0) FROM events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("oldest event: %w", err)
	}
	return id, nil
}

// PruneEvents applies retention: general events older than keep, typing/presence after 1h, platform after 24h.
func (s *Store) PruneEvents(ctx context.Context, now time.Time, keep time.Duration) error {
	stmts := []struct {
		sql string
		age time.Duration
	}{
		{`DELETE FROM events WHERE ts_ms < ? AND type NOT IN ('chat.typing','presence','platform.event')`, keep},
		{`DELETE FROM events WHERE ts_ms < ? AND type IN ('chat.typing','presence')`, time.Hour},
		{`DELETE FROM events WHERE ts_ms < ? AND type = 'platform.event'`, 24 * time.Hour},
	}
	for _, st := range stmts {
		if _, err := s.q.ExecContext(ctx, st.sql, now.Add(-st.age).UnixMilli()); err != nil {
			return fmt.Errorf("prune events: %w", err)
		}
	}
	return nil
}
