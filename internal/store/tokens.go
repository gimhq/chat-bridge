package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gimhq/chat-bridge/internal/model"
)

// schemaV2 adds scoped API tokens (docs/storage.md §2). Only the SHA-256 of a secret is stored.
const schemaV2 = `
CREATE TABLE tokens (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  secret_hash   TEXT NOT NULL UNIQUE,
  scope         TEXT NOT NULL DEFAULT '{}',
  created_at    INTEGER NOT NULL,
  last_used_at  INTEGER
);
`

// ChatRef names a chat on one account.
type ChatRef struct {
	AccountID string
	ChatID    string
}

// Only restricts a list query to the listed ids. The zero value does not restrict.
type Only struct {
	Set bool
	IDs []string
}

// clause returns an SQL condition on col and its arguments; "" when nothing is restricted.
func (o Only) clause(col string) (string, []any) {
	if !o.Set {
		return "", nil
	}
	if len(o.IDs) == 0 {
		return ` AND 0`, nil
	}
	args := make([]any, len(o.IDs))
	for i, id := range o.IDs {
		args[i] = id
	}
	return ` AND ` + col + ` IN (` + placeholders(len(o.IDs)) + `)`, args
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

const tokenCols = `id, name, scope, created_at, last_used_at` //nolint:gosec // G101: column names, not a credential

func scanToken(row scanner) (model.Token, error) {
	var t model.Token
	var scope string
	var created int64
	var used sql.NullInt64
	if err := row.Scan(&t.ID, &t.Name, &scope, &created, &used); err != nil {
		return t, err
	}
	if err := json.Unmarshal([]byte(scope), &t.Scope); err != nil {
		return t, fmt.Errorf("token scope: %w", err)
	}
	t.CreatedAt = fromUnix(created)
	t.LastUsedAt = timePtr(used)
	return t, nil
}

// CreateToken stores a token; secretHash is the hex SHA-256 of its secret.
func (s *Store) CreateToken(ctx context.Context, t model.Token, secretHash string) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO tokens (id, name, secret_hash, scope, created_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.Name, secretHash, mustJSON(t.Scope), unix(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("create token: %w", err)
	}
	return nil
}

// GetToken returns one token by id.
func (s *Store) GetToken(ctx context.Context, id string) (model.Token, error) {
	t, err := scanToken(s.q.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM tokens WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// TokenBySecret returns the token whose secret hashes to secretHash.
func (s *Store) TokenBySecret(ctx context.Context, secretHash string) (model.Token, error) {
	t, err := scanToken(s.q.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM tokens WHERE secret_hash = ?`, secretHash))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// ListTokens returns every token, oldest first.
func (s *Store) ListTokens(ctx context.Context) ([]model.Token, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+tokenCols+` FROM tokens ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []model.Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateToken changes the name and scope of a token; nil leaves a field alone.
func (s *Store) UpdateToken(ctx context.Context, id string, name *string, scope *model.TokenScope) error {
	set, args := []string{}, []any{}
	if name != nil {
		set, args = append(set, `name = ?`), append(args, *name)
	}
	if scope != nil {
		set, args = append(set, `scope = ?`), append(args, mustJSON(scope))
	}
	if len(set) == 0 {
		_, err := s.GetToken(ctx, id)
		return err
	}
	res, err := s.q.ExecContext(ctx, `UPDATE tokens SET `+strings.Join(set, ", ")+` WHERE id = ?`, append(args, id)...)
	if err != nil {
		return fmt.Errorf("update token: %w", err)
	}
	return affected(res)
}

// DeleteToken revokes a token.
func (s *Store) DeleteToken(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	return affected(res)
}

// TouchToken records a use, at most once per minute so reads do not turn into writes.
func (s *Store) TouchToken(ctx context.Context, id string, now time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE tokens SET last_used_at = ? WHERE id = ? AND COALESCE(last_used_at, 0) < ?`,
		unix(now), id, unix(now.Add(-time.Minute)))
	return err
}

// affected maps "no row changed" to ErrNotFound.
func affected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DirectChats returns the direct chats with a contact (see contactDirectChat).
func (s *Store) DirectChats(ctx context.Context, l LinkRef) ([]string, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT ch.id FROM chats ch WHERE `+contactDirectChat, l.AccountID, l.UserID, l.UserID)
	if err != nil {
		return nil, fmt.Errorf("direct chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MediaChat returns the chat of the message a media row belongs to; ok is false for media that
// no message references (uploads, avatars).
func (s *Store) MediaChat(ctx context.Context, m Media) (ChatRef, bool, error) {
	if m.MessageSeq == 0 {
		return ChatRef{}, false, nil
	}
	var ref ChatRef
	err := s.q.QueryRowContext(ctx, `SELECT account_id, chat_id FROM messages WHERE seq = ?`, m.MessageSeq).Scan(&ref.AccountID, &ref.ChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatRef{}, false, nil
	}
	return ref, err == nil, err
}
