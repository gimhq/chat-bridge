package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gimhq/chat-bridge/internal/model"
)

const chatCols = `account_id, id, kind, COALESCE(name,''), COALESCE(avatar_media,''), unread_count, last_message_ts, last_message_seq,
	muted, archived, tags, pinned_ids, ephemeral_ttl_s, raw, updated_at`

// UpsertChat creates the chat or overlays non-empty fields of c. It reports whether the row was created.
func (s *Store) UpsertChat(ctx context.Context, c model.Chat) (bool, error) {
	cur, err := s.GetChat(ctx, c.AccountID, c.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	created := errors.Is(err, ErrNotFound)
	if created {
		cur = model.Chat{AccountID: c.AccountID, ID: c.ID, Kind: c.Kind, Tags: []string{}, PinnedMessageIDs: []string{}}
	}
	if c.Kind != "" {
		cur.Kind = c.Kind
	}
	if cur.Kind == "" {
		cur.Kind = model.ChatDirect
	}
	if c.Name != "" {
		cur.Name = c.Name
	}
	if c.Avatar != nil {
		cur.Avatar = c.Avatar
	}
	if c.EphemeralTTL != nil {
		cur.EphemeralTTL = c.EphemeralTTL
	}
	if len(c.Raw) > 0 {
		cur.Raw = c.Raw
	}
	if c.UnreadCount > 0 {
		cur.UnreadCount = c.UnreadCount // platform-reported counter (history sync, dialog list)
	}
	if !created {
		cur.Muted, cur.Archived = c.Muted || cur.Muted, c.Archived || cur.Archived
	} else {
		cur.Muted, cur.Archived = c.Muted, c.Archived
	}
	if err := s.writeChat(ctx, cur); err != nil {
		return false, err
	}
	return created, nil
}

// SetChatFlags updates the owner-controlled flags. Nil pointers leave a field alone.
func (s *Store) SetChatFlags(ctx context.Context, accountID, chatID string, muted, archived *bool, tags []string) (model.Chat, error) {
	c, err := s.GetChat(ctx, accountID, chatID)
	if err != nil {
		return model.Chat{}, err
	}
	if muted != nil {
		c.Muted = *muted
	}
	if archived != nil {
		c.Archived = *archived
	}
	if tags != nil {
		c.Tags = tags
	}
	if err := s.writeChat(ctx, c); err != nil {
		return model.Chat{}, err
	}
	return s.GetChat(ctx, accountID, chatID)
}

// SetChatName stores a name the platform accepted.
func (s *Store) SetChatName(ctx context.Context, accountID, chatID, name string) (model.Chat, error) {
	c, err := s.GetChat(ctx, accountID, chatID)
	if err != nil {
		return model.Chat{}, err
	}
	c.Name = name
	if err := s.writeChat(ctx, c); err != nil {
		return model.Chat{}, err
	}
	return s.GetChat(ctx, accountID, chatID)
}

func (s *Store) writeChat(ctx context.Context, c model.Chat) error {
	if c.Tags == nil {
		c.Tags = []string{}
	}
	if c.PinnedMessageIDs == nil {
		c.PinnedMessageIDs = []string{}
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO chats
		(account_id, id, kind, name, avatar_media, unread_count, muted, archived, tags, pinned_ids, ephemeral_ttl_s, raw, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, id) DO UPDATE SET kind=excluded.kind, name=excluded.name, avatar_media=excluded.avatar_media,
		unread_count=excluded.unread_count, muted=excluded.muted, archived=excluded.archived, tags=excluded.tags, pinned_ids=excluded.pinned_ids,
		ephemeral_ttl_s=excluded.ephemeral_ttl_s, raw=excluded.raw, updated_at=excluded.updated_at`,
		c.AccountID, c.ID, c.Kind, nullStr(c.Name), nullStr(avatarID(c.Avatar)), c.UnreadCount, boolInt(c.Muted), boolInt(c.Archived),
		mustJSON(c.Tags), mustJSON(c.PinnedMessageIDs), c.EphemeralTTL, rawOrNull(c.Raw), unix(time.Now()))
	if err != nil {
		return fmt.Errorf("write chat: %w", err)
	}
	return nil
}

// TouchChat bumps last-message pointers and, for inbound messages, the unread counter.
func (s *Store) TouchChat(ctx context.Context, accountID, chatID string, ts time.Time, seq int64, inbound bool) error {
	_, err := s.q.ExecContext(ctx, `UPDATE chats SET
		last_message_ts = CASE WHEN last_message_ts IS NULL OR last_message_ts <= ? THEN ? ELSE last_message_ts END,
		last_message_seq = CASE WHEN last_message_ts IS NULL OR last_message_ts <= ? THEN ? ELSE last_message_seq END,
		unread_count = unread_count + ?, updated_at = ?
		WHERE account_id = ? AND id = ?`,
		unix(ts), unix(ts), unix(ts), seq, boolInt(inbound), unix(time.Now()), accountID, chatID)
	if err != nil {
		return fmt.Errorf("touch chat: %w", err)
	}
	return nil
}

// ClearUnread resets the unread counter.
func (s *Store) ClearUnread(ctx context.Context, accountID, chatID string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE chats SET unread_count = 0 WHERE account_id = ? AND id = ?`, accountID, chatID)
	if err != nil {
		return fmt.Errorf("clear unread: %w", err)
	}
	return nil
}

// GetChat returns one chat without participants or last_message.
func (s *Store) GetChat(ctx context.Context, accountID, id string) (model.Chat, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+chatCols+` FROM chats WHERE account_id = ? AND id = ?`, accountID, id)
	c, _, err := scanChat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Chat{}, ErrNotFound
	}
	return c, err
}

// ChatFilter narrows ListChats.
type ChatFilter struct {
	Kind     string
	Archived *bool
	Tag      string
}

// ListChats pages chats by recent activity. The cursor is "<last_message_ts>:<id>".
func (s *Store) ListChats(ctx context.Context, accountID string, f ChatFilter, cursor string, limit int) ([]model.Chat, string, error) {
	where := `account_id = ?`
	args := []any{accountID}
	if f.Kind != "" {
		where += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.Archived != nil {
		where += ` AND archived = ?`
		args = append(args, boolInt(*f.Archived))
	}
	if f.Tag != "" {
		where += ` AND EXISTS (SELECT 1 FROM json_each(tags) WHERE value = ?)`
		args = append(args, f.Tag)
	}
	if cursor != "" {
		ts, id, ok := splitCursor(cursor)
		if !ok {
			return nil, "", fmt.Errorf("%w: bad cursor", ErrNotFound)
		}
		where += ` AND (COALESCE(last_message_ts,0) < ? OR (COALESCE(last_message_ts,0) = ? AND id > ?))`
		args = append(args, ts, ts, id)
	}
	args = append(args, limit+1)
	rows, err := s.q.QueryContext(ctx, `SELECT `+chatCols+` FROM chats WHERE `+where+
		` ORDER BY COALESCE(last_message_ts,0) DESC, id LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]model.Chat, 0, limit)
	seqs := make([]int64, 0, limit)
	for rows.Next() {
		c, seq, err := scanChat(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, c)
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out, seqs = out[:limit], seqs[:limit]
		last := out[limit-1]
		var ts int64
		if last.LastMessageAt != nil {
			ts = last.LastMessageAt.Unix()
		}
		next = strconv.FormatInt(ts, 10) + ":" + last.ID
	}
	for i := range out {
		if seqs[i] == 0 {
			continue
		}
		m, err := s.GetMessageBySeq(ctx, seqs[i])
		if err == nil {
			out[i].LastMessage = &m.Message
		}
	}
	return out, next, nil
}

// UpsertMember records membership; Left marks departure but keeps the row.
func (s *Store) UpsertMember(ctx context.Context, accountID, chatID, userID, chatName, role string, left bool) error {
	if role == "" {
		role = "member"
	}
	var leftAt any
	if left {
		leftAt = unix(time.Now())
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO chat_members (account_id, chat_id, user_id, chat_name, role, joined_at, left_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, chat_id, user_id) DO UPDATE SET
		chat_name = COALESCE(excluded.chat_name, chat_name), role = excluded.role, left_at = excluded.left_at,
		joined_at = CASE WHEN excluded.left_at IS NULL AND left_at IS NOT NULL THEN excluded.joined_at ELSE joined_at END`,
		accountID, chatID, userID, nullStr(chatName), role, unix(time.Now()), leftAt)
	if err != nil {
		return fmt.Errorf("upsert member: %w", err)
	}
	return nil
}

// ListMembers returns current members with resolved names.
func (s *Store) ListMembers(ctx context.Context, accountID, chatID string) ([]model.Participant, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT user_id, COALESCE(chat_name,''), role FROM chat_members
		WHERE account_id = ? AND chat_id = ? AND left_at IS NULL ORDER BY user_id`, accountID, chatID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []model.Participant
	var ids []string
	for rows.Next() {
		var p model.Participant
		if err := rows.Scan(&p.ID, &p.ChatName, &p.Role); err != nil {
			return nil, err
		}
		out = append(out, p)
		ids = append(ids, p.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names, err := s.ContactNames(ctx, accountID, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Name = names[out[i].ID]
		if out[i].Name == "" {
			out[i].Name = out[i].ChatName
		}
		if out[i].Name == "" {
			out[i].Name = out[i].ID
		}
	}
	return out, nil
}

// MemberChatName returns the per-chat nickname if known.
func (s *Store) MemberChatName(ctx context.Context, accountID, chatID, userID string) string {
	var name string
	_ = s.q.QueryRowContext(ctx, `SELECT COALESCE(chat_name,'') FROM chat_members WHERE account_id = ? AND chat_id = ? AND user_id = ?`,
		accountID, chatID, userID).Scan(&name)
	return name
}

func scanChat(row scanner) (model.Chat, int64, error) {
	var c model.Chat
	var avatar, tags, pinned string
	var lastTS, lastSeq, ttl sql.NullInt64
	var muted, archived int
	var raw sql.NullString
	var updated int64
	if err := row.Scan(&c.AccountID, &c.ID, &c.Kind, &c.Name, &avatar, &c.UnreadCount, &lastTS, &lastSeq,
		&muted, &archived, &tags, &pinned, &ttl, &raw, &updated); err != nil {
		return c, 0, err
	}
	if avatar != "" {
		c.Avatar = &model.AvatarRef{MediaID: avatar}
	}
	c.LastMessageAt = timePtr(lastTS)
	c.Muted, c.Archived = muted == 1, archived == 1
	_ = json.Unmarshal([]byte(tags), &c.Tags)
	_ = json.Unmarshal([]byte(pinned), &c.PinnedMessageIDs)
	if c.Tags == nil {
		c.Tags = []string{}
	}
	if c.PinnedMessageIDs == nil {
		c.PinnedMessageIDs = []string{}
	}
	if ttl.Valid {
		c.EphemeralTTL = &ttl.Int64
	}
	if raw.Valid {
		c.Raw = json.RawMessage(raw.String)
	}
	c.UpdatedAt = fromUnix(updated)
	return c, lastSeq.Int64, nil
}
