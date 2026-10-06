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
	// Person narrows to events about one person: its own updates, and events on a linked
	// account whose sender, user, chat or subject id is a linked contact.
	Person *PersonScope
	// Scope narrows to what a scoped token may see; nil does not restrict.
	Scope *EventScope
}

// EventScope is the resolved allowlist of a scoped token as the event log needs it.
type EventScope struct {
	Accounts []string
	Chats    []ChatRef
	Contacts []LinkRef
	Persons  []string
}

// Event types by the payload key that names their chat or contact (api.md §6). A type that is not
// listed here never reaches a scoped token.
var (
	scopeByChatID = []string{model.EvMessageNew, model.EvMessageUpdated, model.EvMessageDeleted, model.EvMessageReaction, model.EvMessageReceipt, model.EvChatTyping}
	scopeByChat   = []string{model.EvChatNew, model.EvChatUpdated}
)

// clause returns the SQL condition that keeps only events inside the scope.
func (sc *EventScope) clause() (string, []any) {
	var conds []string
	var args []any
	pairs := func(types []string, key string, n int, each func(i int) (string, string)) {
		if n == 0 {
			return
		}
		conds = append(conds, `(type IN (`+placeholders(len(types))+`) AND (account_id, json_extract(data, '$.`+key+`')) IN (VALUES `+
			strings.TrimSuffix(strings.Repeat("(?,?),", n), ",")+`))`)
		for _, t := range types {
			args = append(args, t)
		}
		for i := 0; i < n; i++ {
			a, b := each(i)
			args = append(args, a, b)
		}
	}
	chat := func(i int) (string, string) { return sc.Chats[i].AccountID, sc.Chats[i].ChatID }
	contact := func(i int) (string, string) { return sc.Contacts[i].AccountID, sc.Contacts[i].UserID }
	pairs(scopeByChatID, "chat_id", len(sc.Chats), chat)
	pairs(scopeByChat, "id", len(sc.Chats), chat)
	pairs([]string{model.EvContactUpdated}, "id", len(sc.Contacts), contact)
	pairs([]string{model.EvPresence}, "user_id", len(sc.Contacts), contact)
	if len(sc.Persons) > 0 {
		conds = append(conds, `(type = ? AND json_extract(data, '$.id') IN (`+placeholders(len(sc.Persons))+`))`)
		args = append(args, model.EvPersonUpdated)
		for _, p := range sc.Persons {
			args = append(args, p)
		}
	}
	if len(sc.Accounts) > 0 {
		conds = append(conds, `(type = ? AND account_id IN (`+placeholders(len(sc.Accounts))+`))`)
		args = append(args, model.EvAccountStatus)
		for _, a := range sc.Accounts {
			args = append(args, a)
		}
	}
	if len(conds) == 0 {
		return `0`, nil
	}
	return `(` + strings.Join(conds, " OR ") + `)`, args
}

// PersonScope is a person id with its links, resolved when the filter is built.
type PersonScope struct {
	ID    string
	Links []LinkRef
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
	if f.Person != nil {
		conds := []string{`(type = 'person.updated' AND json_extract(data, '$.id') = ?)`}
		args = append(args, f.Person.ID)
		for _, l := range f.Person.Links {
			conds = append(conds, `(account_id = ? AND ? IN (json_extract(data, '$.sender.id'), json_extract(data, '$.sender_id'),
				json_extract(data, '$.user_id'), json_extract(data, '$.chat_id'), json_extract(data, '$.from.id'), json_extract(data, '$.id')))`)
			args = append(args, l.AccountID, l.UserID)
		}
		where += ` AND (` + strings.Join(conds, " OR ") + `)`
	}
	if f.Scope != nil {
		cond, a := f.Scope.clause()
		where += ` AND ` + cond
		args = append(args, a...)
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
