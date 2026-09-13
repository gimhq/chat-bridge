package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"gimhq/chat-bridge/internal/model"
)

const requestCols = `rowid, id, account_id, platform_key, kind, state, COALESCE(from_id,''), COALESCE(from_name,''),
	COALESCE(chat_id,''), COALESCE(chat_name,''), COALESCE(chat_kind,''), COALESCE(message,''), COALESCE(call_kind,''),
	platform_ref, raw, created_at, expires_at, answered_at`

// StoredRequest is a request row with the adapter-private fields the API never shows.
type StoredRequest struct {
	model.Request
	PlatformKey string
	PlatformRef json.RawMessage
	rowid       int64
}

// RequestChange reports what UpsertRequest did.
type RequestChange int

// Upsert outcomes.
const (
	RequestUnchanged RequestChange = iota
	RequestCreated
	RequestUpdated
)

// RequestFilter narrows ListRequests.
type RequestFilter struct {
	Kind  string
	State string
}

// UpsertRequest inserts a request or merges a re-emitted one (same account and platform key).
// Names, message and expiry follow the adapter. State only moves from pending to a terminal state,
// except that a request created later than the stored one reopens it (a new invite to the same room).
func (s *Store) UpsertRequest(ctx context.Context, in StoredRequest) (model.Request, RequestChange, error) {
	if in.State == "" {
		in.State = model.RequestPending
	}
	cur, err := s.requestByKey(ctx, in.AccountID, in.PlatformKey)
	if errors.Is(err, ErrNotFound) {
		if in.State != model.RequestPending {
			return model.Request{}, RequestUnchanged, nil // resolved before we ever saw it pending: nothing to show
		}
		in.ID = "req_" + uuid.NewString()
		if in.CreatedAt.IsZero() {
			in.CreatedAt = time.Now()
		}
		if err := s.insertRequest(ctx, in); err != nil {
			return model.Request{}, 0, err
		}
		out, err := s.GetRequest(ctx, in.AccountID, in.ID)
		return out.Request, RequestCreated, err
	}
	if err != nil {
		return model.Request{}, 0, err
	}
	next, changed := mergeRequest(cur, in)
	refChanged := len(in.PlatformRef) > 0 && !bytes.Equal(in.PlatformRef, cur.PlatformRef)
	if refChanged {
		next.PlatformRef = in.PlatformRef
	}
	if !changed && !refChanged {
		cur.FillActions()
		return cur.Request, RequestUnchanged, nil
	}
	if err := s.writeRequest(ctx, next); err != nil {
		return model.Request{}, 0, err
	}
	out, err := s.GetRequest(ctx, in.AccountID, cur.ID)
	if !changed {
		return out.Request, RequestUnchanged, err
	}
	return out.Request, RequestUpdated, err
}

func mergeRequest(cur, in StoredRequest) (StoredRequest, bool) {
	next := cur
	changed := false
	if in.From != nil {
		from := model.Sender{}
		if cur.From != nil {
			from = *cur.From
		}
		changed = setStr(&from.ID, in.From.ID) || changed
		changed = setStr(&from.Name, in.From.Name) || changed
		next.From = &from
	}
	if in.Chat != nil {
		chat := model.RequestChat{}
		if cur.Chat != nil {
			chat = *cur.Chat
		}
		changed = setStr(&chat.ID, in.Chat.ID) || changed
		changed = setStr(&chat.Name, in.Chat.Name) || changed
		changed = setStr(&chat.Kind, in.Chat.Kind) || changed
		next.Chat = &chat
	}
	changed = setStr(&next.Message, in.Message) || changed
	if in.Call != nil && (cur.Call == nil || cur.Call.Kind != in.Call.Kind) {
		next.Call, changed = &model.CallInfo{Kind: in.Call.Kind}, true
	}
	if in.ExpiresAt != nil && (cur.ExpiresAt == nil || cur.ExpiresAt.Unix() != in.ExpiresAt.Unix()) {
		next.ExpiresAt, changed = in.ExpiresAt, true
	}
	switch {
	case cur.State == model.RequestPending && in.State != model.RequestPending:
		next.State, changed = in.State, true
		if in.State != model.RequestExpired {
			now := time.Now()
			next.AnsweredAt = &now
		}
	case cur.State != model.RequestPending && in.State == model.RequestPending && in.CreatedAt.Unix() > cur.CreatedAt.Unix():
		next.State, next.AnsweredAt, next.CreatedAt, next.ExpiresAt, changed = model.RequestPending, nil, in.CreatedAt, in.ExpiresAt, true
	}
	return next, changed
}

func setStr(dst *string, v string) bool {
	if v == "" || *dst == v {
		return false
	}
	*dst = v
	return true
}

func (s *Store) insertRequest(ctx context.Context, r StoredRequest) error {
	f, c := flatRequest(r.Request)
	_, err := s.q.ExecContext(ctx, `INSERT INTO requests
		(id, account_id, platform_key, kind, state, from_id, from_name, chat_id, chat_name, chat_kind, message, call_kind,
		 platform_ref, raw, created_at, expires_at, answered_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.AccountID, r.PlatformKey, r.Kind, r.State, nullStr(f.ID), nullStr(f.Name), nullStr(c.ID), nullStr(c.Name), nullStr(c.Kind),
		nullStr(r.Message), nullStr(callKind(r.Call)), rawOrNull(r.PlatformRef), rawOrNull(r.Raw), unix(r.CreatedAt),
		unixPtr(r.ExpiresAt), unixPtr(r.AnsweredAt), unix(time.Now()))
	if err != nil {
		return fmt.Errorf("insert request: %w", err)
	}
	return nil
}

func (s *Store) writeRequest(ctx context.Context, r StoredRequest) error {
	f, c := flatRequest(r.Request)
	_, err := s.q.ExecContext(ctx, `UPDATE requests SET state = ?, from_id = ?, from_name = ?, chat_id = ?, chat_name = ?, chat_kind = ?,
		message = ?, call_kind = ?, platform_ref = ?, created_at = ?, expires_at = ?, answered_at = ?, updated_at = ? WHERE id = ?`,
		r.State, nullStr(f.ID), nullStr(f.Name), nullStr(c.ID), nullStr(c.Name), nullStr(c.Kind), nullStr(r.Message), nullStr(callKind(r.Call)),
		rawOrNull(r.PlatformRef), unix(r.CreatedAt), unixPtr(r.ExpiresAt), unixPtr(r.AnsweredAt), unix(time.Now()), r.ID)
	if err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	return nil
}

func flatRequest(r model.Request) (model.Sender, model.RequestChat) {
	var f model.Sender
	var c model.RequestChat
	if r.From != nil {
		f = *r.From
	}
	if r.Chat != nil {
		c = *r.Chat
	}
	return f, c
}

func callKind(c *model.CallInfo) string {
	if c == nil {
		return ""
	}
	return c.Kind
}

func unixPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return unix(*t)
}

// GetRequest returns one request with its private fields.
func (s *Store) GetRequest(ctx context.Context, accountID, id string) (StoredRequest, error) {
	return scanRequest(s.q.QueryRowContext(ctx, `SELECT `+requestCols+` FROM requests WHERE account_id = ? AND id = ?`, accountID, id))
}

func (s *Store) requestByKey(ctx context.Context, accountID, key string) (StoredRequest, error) {
	return scanRequest(s.q.QueryRowContext(ctx, `SELECT `+requestCols+` FROM requests WHERE account_id = ? AND platform_key = ?`, accountID, key))
}

// SetRequestState records the owner's answer (or any terminal state).
func (s *Store) SetRequestState(ctx context.Context, accountID, id, state string, answeredAt *time.Time) (model.Request, error) {
	res, err := s.q.ExecContext(ctx, `UPDATE requests SET state = ?, answered_at = ?, updated_at = ? WHERE account_id = ? AND id = ?`,
		state, unixPtr(answeredAt), unix(time.Now()), accountID, id)
	if err != nil {
		return model.Request{}, fmt.Errorf("set request state: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Request{}, ErrNotFound
	}
	r, err := s.GetRequest(ctx, accountID, id)
	return r.Request, err
}

// ExpireRequests marks pending requests whose expiry has passed and returns them.
func (s *Store) ExpireRequests(ctx context.Context, now time.Time) ([]model.Request, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+requestCols+` FROM requests WHERE state = 'pending' AND expires_at IS NOT NULL AND expires_at <= ?
		ORDER BY created_at, rowid`, unix(now))
	if err != nil {
		return nil, fmt.Errorf("due requests: %w", err)
	}
	var due []StoredRequest
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		due = append(due, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]model.Request, 0, len(due))
	for _, r := range due {
		upd, err := s.SetRequestState(ctx, r.AccountID, r.ID, model.RequestExpired, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, upd)
	}
	return out, nil
}

// ListRequests pages an account's requests newest first.
func (s *Store) ListRequests(ctx context.Context, accountID string, f RequestFilter, cursor string, limit int) ([]model.Request, string, error) {
	where := `account_id = ?`
	args := []any{accountID}
	if f.Kind != "" {
		where += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.State != "" {
		where += ` AND state = ?`
		args = append(args, f.State)
	}
	if cursor != "" {
		ts, rowid, err := DecodeMessageCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		where += ` AND (created_at < ? OR (created_at = ? AND rowid < ?))`
		args = append(args, ts, ts, rowid)
	}
	args = append(args, limit+1)
	rows, err := s.q.QueryContext(ctx, `SELECT `+requestCols+` FROM requests WHERE `+where+` ORDER BY created_at DESC, rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var page []StoredRequest
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, "", err
		}
		page = append(page, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(page) > limit {
		page = page[:limit]
		last := page[limit-1]
		next = EncodeMessageCursor(last.CreatedAt.Unix(), last.rowid)
	}
	out := make([]model.Request, len(page))
	for i := range page {
		out[i] = page[i].Request
	}
	return out, next, nil
}

// PendingRequests counts an account's open requests.
func (s *Store) PendingRequests(ctx context.Context, accountID string) (int64, error) {
	var n int64
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests WHERE account_id = ? AND state = 'pending'`, accountID).Scan(&n)
	return n, err
}

func scanRequest(row scanner) (StoredRequest, error) {
	var r StoredRequest
	var fromID, fromName, chatID, chatName, chatKind, call string
	var ref, raw sql.NullString
	var created int64
	var expires, answered sql.NullInt64
	err := row.Scan(&r.rowid, &r.ID, &r.AccountID, &r.PlatformKey, &r.Kind, &r.State, &fromID, &fromName, &chatID, &chatName, &chatKind,
		&r.Message, &call, &ref, &raw, &created, &expires, &answered)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, fmt.Errorf("scan request: %w", err)
	}
	if fromID != "" || fromName != "" {
		r.From = &model.Sender{ID: fromID, Name: fromName}
	}
	if chatID != "" || chatName != "" {
		r.Chat = &model.RequestChat{ID: chatID, Name: chatName, Kind: chatKind}
	}
	if call != "" {
		r.Call = &model.CallInfo{Kind: call}
	}
	if ref.Valid {
		r.PlatformRef = json.RawMessage(ref.String)
	}
	if raw.Valid {
		r.Raw = json.RawMessage(raw.String)
	}
	r.CreatedAt = fromUnix(created)
	r.ExpiresAt = timePtr(expires)
	r.AnsweredAt = timePtr(answered)
	r.FillActions()
	return r, nil
}
