package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gimhq/chat-bridge/internal/model"
)

const messageCols = `seq, account_id, chat_id, id, sender_id, COALESCE(sender_name,''), from_me, ts, type, content,
	COALESCE(reply_to,''), COALESCE(thread_id,''), mentions, forwarded, ephemeral, COALESCE(status,''), COALESCE(client_id,''),
	edited_at, deleted_at`

// InsertMessage stores m (idempotent on account/chat/id). It returns the row seq and whether a
// row was inserted. raw, when non-empty, is stored in raw_payloads.
func (s *Store) InsertMessage(ctx context.Context, m model.Message, raw json.RawMessage) (int64, bool, error) {
	if m.Mentions == nil {
		m.Mentions = []string{}
	}
	var seq int64
	err := s.q.QueryRowContext(ctx, `INSERT INTO messages
		(account_id, chat_id, id, sender_id, sender_name, from_me, ts, received_at, type, text, content, reply_to, thread_id,
		 mentions, forwarded, ephemeral, status, client_id, edited_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, chat_id, id) DO NOTHING RETURNING seq`,
		m.AccountID, m.ChatID, m.ID, m.Sender.ID, nullStr(m.Sender.Name), boolInt(m.FromMe), unix(m.Timestamp), unix(time.Now()),
		m.Content.Type, nullStr(m.Content.Text), mustJSON(m.Content), nullStr(m.ReplyTo), nullStr(m.ThreadID),
		mustJSON(m.Mentions), boolInt(m.Forwarded), jsonOrNull(ephemeralOrNil(m.Ephemeral)), nullStr(m.Status), nullStr(m.ClientID),
		nullTime(m.EditedAt), nullTime(m.DeletedAt)).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		existing, err := s.GetMessage(ctx, m.AccountID, m.ChatID, m.ID)
		if err != nil {
			return 0, false, err
		}
		return existing.seq, false, nil
	}
	if err != nil {
		if isUnique(err) {
			return 0, false, ErrConflict
		}
		return 0, false, fmt.Errorf("insert message: %w", err)
	}
	if len(raw) > 0 {
		if _, err := s.q.ExecContext(ctx, `INSERT OR REPLACE INTO raw_payloads (message_seq, raw, created_at) VALUES (?, ?, ?)`,
			seq, string(raw), unix(time.Now())); err != nil {
			return 0, false, fmt.Errorf("insert raw: %w", err)
		}
	}
	return seq, true, nil
}

func ephemeralOrNil(e *model.Ephemeral) any {
	if e == nil {
		return nil
	}
	return e
}

// Stored is a message with its internal seq.
type Stored struct {
	model.Message
	seq int64
}

// Seq returns the internal row id.
func (m Stored) Seq() int64 { return m.seq }

// GetMessage returns a message by its external key.
func (s *Store) GetMessage(ctx context.Context, accountID, chatID, id string) (Stored, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+messageCols+` FROM messages WHERE account_id = ? AND chat_id = ? AND id = ?`, accountID, chatID, id)
	return s.finishMessage(ctx, row)
}

// FindMessage returns a message by account and id; the newest wins on collision.
func (s *Store) FindMessage(ctx context.Context, accountID, id string) (Stored, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+messageCols+` FROM messages WHERE account_id = ? AND id = ? ORDER BY seq DESC LIMIT 1`, accountID, id)
	return s.finishMessage(ctx, row)
}

// FindByClientID returns the message previously sent with this client id.
func (s *Store) FindByClientID(ctx context.Context, accountID, chatID, clientID string) (Stored, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+messageCols+` FROM messages WHERE account_id = ? AND chat_id = ? AND client_id = ?`, accountID, chatID, clientID)
	return s.finishMessage(ctx, row)
}

// GetMessageBySeq returns a message by internal seq.
func (s *Store) GetMessageBySeq(ctx context.Context, seq int64) (Stored, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+messageCols+` FROM messages WHERE seq = ?`, seq)
	return s.finishMessage(ctx, row)
}

func (s *Store) finishMessage(ctx context.Context, row scanner) (Stored, error) {
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, ErrNotFound
	}
	if err != nil {
		return Stored{}, err
	}
	batch := []Stored{m}
	if err := s.decorate(ctx, batch); err != nil {
		return Stored{}, err
	}
	return batch[0], nil
}

// RawPayload returns the adapter's raw payload for a message, if retained.
func (s *Store) RawPayload(ctx context.Context, seq int64) (json.RawMessage, error) {
	var raw string
	err := s.q.QueryRowContext(ctx, `SELECT raw FROM raw_payloads WHERE message_seq = ?`, seq).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("raw payload: %w", err)
	}
	return json.RawMessage(raw), nil
}

// ListMessages pages a chat newest first. Cursor encodes (ts, seq) of the last row.
func (s *Store) ListMessages(ctx context.Context, accountID, chatID, cursor string, before, after time.Time, limit int) ([]Stored, string, error) {
	where := `account_id = ? AND chat_id = ?`
	args := []any{accountID, chatID}
	if cursor != "" {
		ts, seq, err := DecodeMessageCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		where += ` AND (ts < ? OR (ts = ? AND seq < ?))`
		args = append(args, ts, ts, seq)
	}
	if !before.IsZero() {
		where += ` AND ts < ?`
		args = append(args, unix(before))
	}
	if !after.IsZero() {
		where += ` AND ts > ?`
		args = append(args, unix(after))
	}
	args = append(args, limit+1)
	rows, err := s.q.QueryContext(ctx, `SELECT `+messageCols+` FROM messages WHERE `+where+` ORDER BY ts DESC, seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Stored, 0, limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = EncodeMessageCursor(last.Timestamp.Unix(), last.seq)
	}
	if err := s.decorate(ctx, out); err != nil {
		return nil, "", err
	}
	return out, next, nil
}

// EncodeMessageCursor packs (ts, seq) into an opaque string.
func EncodeMessageCursor(ts, seq int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(ts, 10) + ":" + strconv.FormatInt(seq, 10)))
}

// DecodeMessageCursor is the inverse of EncodeMessageCursor.
func DecodeMessageCursor(c string) (int64, int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, 0, fmt.Errorf("bad cursor")
	}
	ts, seq, ok := splitCursor(string(b))
	if !ok {
		return 0, 0, fmt.Errorf("bad cursor")
	}
	n, err := strconv.ParseInt(seq, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad cursor")
	}
	return ts, n, nil
}

func splitCursor(c string) (int64, string, bool) {
	i := strings.IndexByte(c, ':')
	if i < 0 {
		return 0, "", false
	}
	ts, err := strconv.ParseInt(c[:i], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return ts, c[i+1:], true
}

// UpdateMessage loads a message, applies fn, and writes content/status/edited/deleted back.
// When the content changes on an edit the previous body is kept in message_versions.
func (s *Store) UpdateMessage(ctx context.Context, seq int64, fn func(m *model.Message)) (Stored, error) {
	cur, err := s.GetMessageBySeq(ctx, seq)
	if err != nil {
		return Stored{}, err
	}
	next := cur.Message
	fn(&next)
	if next.EditedAt != nil && (cur.EditedAt == nil || !cur.EditedAt.Equal(*next.EditedAt)) {
		if _, err := s.q.ExecContext(ctx, `INSERT OR REPLACE INTO message_versions (message_seq, edited_at, content) VALUES (?, ?, ?)`,
			seq, unix(*next.EditedAt), mustJSON(cur.Content)); err != nil {
			return Stored{}, fmt.Errorf("message version: %w", err)
		}
	}
	_, err = s.q.ExecContext(ctx, `UPDATE messages SET type = ?, text = ?, content = ?, status = ?, edited_at = ?, deleted_at = ?, sender_name = ? WHERE seq = ?`,
		next.Content.Type, nullStr(next.Content.Text), mustJSON(next.Content), nullStr(next.Status),
		nullTime(next.EditedAt), nullTime(next.DeletedAt), nullStr(next.Sender.Name), seq)
	if err != nil {
		return Stored{}, fmt.Errorf("update message: %w", err)
	}
	return s.GetMessageBySeq(ctx, seq)
}

// SetReaction adds or removes one reaction.
func (s *Store) SetReaction(ctx context.Context, seq int64, senderID, emoji string, remove bool, at time.Time) error {
	var err error
	if remove {
		_, err = s.q.ExecContext(ctx, `DELETE FROM reactions WHERE message_seq = ? AND sender_id = ? AND (emoji = ? OR ? = '')`, seq, senderID, emoji, emoji)
	} else {
		// One reaction per sender on WhatsApp/Telegram-style platforms: replace any previous emoji.
		if _, err = s.q.ExecContext(ctx, `DELETE FROM reactions WHERE message_seq = ? AND sender_id = ?`, seq, senderID); err == nil {
			_, err = s.q.ExecContext(ctx, `INSERT INTO reactions (message_seq, sender_id, emoji, ts) VALUES (?, ?, ?, ?)`, seq, senderID, emoji, unix(at))
		}
	}
	if err != nil {
		return fmt.Errorf("set reaction: %w", err)
	}
	return nil
}

// SetReceipt records a per-user receipt and returns the message's rolled-up status.
func (s *Store) SetReceipt(ctx context.Context, seq int64, userID, kind string, at time.Time) (string, error) {
	if _, err := s.q.ExecContext(ctx, `INSERT OR REPLACE INTO receipts (message_seq, user_id, kind, ts) VALUES (?, ?, ?, ?)`,
		seq, userID, kind, unix(at)); err != nil {
		return "", fmt.Errorf("set receipt: %w", err)
	}
	var hasRead, hasDelivered int
	if err := s.q.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM receipts WHERE message_seq = ? AND kind = 'read'),
		EXISTS(SELECT 1 FROM receipts WHERE message_seq = ? AND kind = 'delivered')`, seq, seq).Scan(&hasRead, &hasDelivered); err != nil {
		return "", fmt.Errorf("rollup receipt: %w", err)
	}
	status := model.MsgSent
	switch {
	case hasRead == 1:
		status = model.MsgRead
	case hasDelivered == 1:
		status = model.MsgDelivered
	}
	if _, err := s.q.ExecContext(ctx, `UPDATE messages SET status = ? WHERE seq = ? AND from_me = 1`, status, seq); err != nil {
		return "", fmt.Errorf("update status: %w", err)
	}
	return status, nil
}

// decorate fills reactions and attachment rows for a batch.
func (s *Store) decorate(ctx context.Context, msgs []Stored) error {
	if len(msgs) == 0 {
		return nil
	}
	idx := make(map[int64]int, len(msgs))
	args := make([]any, 0, len(msgs))
	for i := range msgs {
		idx[msgs[i].seq] = i
		args = append(args, msgs[i].seq)
		msgs[i].Reactions = []model.Reaction{}
		if msgs[i].Mentions == nil {
			msgs[i].Mentions = []string{}
		}
	}
	// Messages stored before their sender was known keep the id as name; resolve at read time.
	unnamed := map[string][]string{}
	for i := range msgs {
		if msgs[i].Sender.Name == "" || msgs[i].Sender.Name == msgs[i].Sender.ID {
			unnamed[msgs[i].AccountID] = append(unnamed[msgs[i].AccountID], msgs[i].Sender.ID)
		}
	}
	for account, ids := range unnamed {
		names, err := s.ContactNames(ctx, account, ids)
		if err != nil {
			return err
		}
		for i := range msgs {
			if msgs[i].AccountID != account {
				continue
			}
			if n := names[msgs[i].Sender.ID]; n != "" && n != msgs[i].Sender.ID {
				msgs[i].Sender.Name = n
			} else if msgs[i].Sender.Name == "" {
				msgs[i].Sender.Name = msgs[i].Sender.ID
			}
		}
	}
	if err := s.decoratePersons(ctx, msgs); err != nil {
		return err
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(msgs)), ",")
	rows, err := s.q.QueryContext(ctx, `SELECT message_seq, sender_id, emoji FROM reactions WHERE message_seq IN (`+ph+`) ORDER BY ts`, args...)
	if err != nil {
		return fmt.Errorf("reactions: %w", err)
	}
	for rows.Next() {
		var seq int64
		var r model.Reaction
		if err := rows.Scan(&seq, &r.SenderID, &r.Emoji); err != nil {
			_ = rows.Close()
			return err
		}
		i := idx[seq]
		msgs[i].Reactions = append(msgs[i].Reactions, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	mrows, err := s.q.QueryContext(ctx, `SELECT `+mediaCols+` FROM media WHERE message_seq IN (`+ph+`)`, args...)
	if err != nil {
		return fmt.Errorf("attachments: %w", err)
	}
	defer func() { _ = mrows.Close() }()
	for mrows.Next() {
		md, err := scanMedia(mrows)
		if err != nil {
			return err
		}
		i := idx[md.MessageSeq]
		att := md.Attachment()
		replaced := false
		for j := range msgs[i].Content.Attachments {
			if msgs[i].Content.Attachments[j].MediaID == att.MediaID {
				msgs[i].Content.Attachments[j] = att
				replaced = true
			}
		}
		if !replaced {
			msgs[i].Content.Attachments = append(msgs[i].Content.Attachments, att)
		}
	}
	return mrows.Err()
}

func scanMessage(row scanner) (Stored, error) {
	var m Stored
	var fromMe, forwarded int
	var ts int64
	var content, mentions string
	var ephemeral sql.NullString
	var edited, deleted sql.NullInt64
	if err := row.Scan(&m.seq, &m.AccountID, &m.ChatID, &m.ID, &m.Sender.ID, &m.Sender.Name, &fromMe, &ts, new(string), &content,
		&m.ReplyTo, &m.ThreadID, &mentions, &forwarded, &ephemeral, &m.Status, &m.ClientID, &edited, &deleted); err != nil {
		return m, err
	}
	m.FromMe, m.Forwarded = fromMe == 1, forwarded == 1
	m.Timestamp = fromUnix(ts)
	_ = json.Unmarshal([]byte(content), &m.Content)
	_ = json.Unmarshal([]byte(mentions), &m.Mentions)
	if ephemeral.Valid {
		var e model.Ephemeral
		if json.Unmarshal([]byte(ephemeral.String), &e) == nil {
			m.Ephemeral = &e
		}
	}
	m.EditedAt, m.DeletedAt = timePtr(edited), timePtr(deleted)
	return m, nil
}
