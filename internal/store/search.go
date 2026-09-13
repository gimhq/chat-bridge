package store

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// messageColsM is messageCols qualified with the alias used by the search join.
const messageColsM = `m.seq, m.account_id, m.chat_id, m.id, m.sender_id, COALESCE(m.sender_name,''), m.from_me, m.ts, m.type, m.content,
	COALESCE(m.reply_to,''), COALESCE(m.thread_id,''), m.mentions, m.forwarded, m.ephemeral, COALESCE(m.status,''), COALESCE(m.client_id,''),
	m.edited_at, m.deleted_at`

// SearchMessages finds messages whose text contains every whitespace-separated term of q, newest
// first. Terms of three or more characters use the trigram FTS index; shorter terms fall back to
// LIKE so two-character CJK queries still work. chatID narrows to one chat when set.
func (s *Store) SearchMessages(ctx context.Context, accountID, chatID, q, cursor string, limit int) ([]Stored, string, error) {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return nil, "", fmt.Errorf("empty query")
	}
	where := `m.account_id = ?`
	args := []any{accountID}
	if chatID != "" {
		where += ` AND m.chat_id = ?`
		args = append(args, chatID)
	}
	var fts []string
	for _, t := range terms {
		if utf8.RuneCountInString(t) >= 3 {
			fts = append(fts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		} else {
			where += ` AND m.text LIKE ? ESCAPE '\'`
			args = append(args, "%"+likeEscape(t)+"%")
		}
	}
	from := `messages m`
	if len(fts) > 0 {
		from = `messages_fts f JOIN messages m ON m.seq = f.rowid`
		where = `f.text MATCH ? AND ` + where
		args = append([]any{strings.Join(fts, " ")}, args...)
	}
	if cursor != "" {
		ts, seq, err := DecodeMessageCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		where += ` AND (m.ts < ? OR (m.ts = ? AND m.seq < ?))`
		args = append(args, ts, ts, seq)
	}
	args = append(args, limit+1)
	rows, err := s.q.QueryContext(ctx, `SELECT `+messageColsM+` FROM `+from+` WHERE `+where+` ORDER BY m.ts DESC, m.seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("search messages: %w", err)
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

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
