package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// ReIDResult says what ReID touched.
type ReIDResult struct {
	Changed bool // any row
	Contact bool // a contact now lives under the new id
	Chat    bool // a direct chat now lives under the new id
}

// ReID moves everything an account stores under oldID to newID: the contact, the direct chat
// whose id is that user, and every reference to the user (senders, members, reactions, receipts,
// mentions and system notices, requests, the account's self id). Rows that already exist under
// newID win; the old rows are merged into them. Calling it again is a no-op.
func (s *Store) ReID(ctx context.Context, accountID, oldID, newID string) (ReIDResult, error) {
	var res ReIDResult
	if oldID == "" || newID == "" || oldID == newID {
		return res, nil
	}
	if ok, err := s.referenced(ctx, accountID, oldID); err != nil || !ok {
		return res, err
	}
	var err error
	if res.Contact, err = s.reIDContact(ctx, accountID, oldID, newID); err != nil {
		return res, err
	}
	if res.Chat, err = s.reIDChat(ctx, accountID, oldID, newID); err != nil {
		return res, err
	}
	refs, err := s.reIDRefs(ctx, accountID, oldID, newID)
	if err != nil {
		return res, err
	}
	res.Changed = res.Contact || res.Chat || refs
	return res, nil
}

// referenced reports whether the account stores id as a contact, chat, sender, member, request
// party or self. Adapters announce every identity change they learn, most of them for users the
// store never saw, so ReID checks the indexed places first; an id that only appears in mentions
// or notices is left as it is.
func (s *Store) referenced(ctx context.Context, accountID, id string) (bool, error) {
	var ok bool
	err := s.q.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM contacts WHERE account_id = ? AND id = ?) OR
		EXISTS(SELECT 1 FROM chats WHERE account_id = ? AND id = ?) OR
		EXISTS(SELECT 1 FROM messages WHERE account_id = ? AND sender_id = ?) OR
		EXISTS(SELECT 1 FROM chat_members WHERE account_id = ? AND user_id = ?) OR
		EXISTS(SELECT 1 FROM requests WHERE account_id = ? AND (from_id = ? OR chat_id = ?)) OR
		EXISTS(SELECT 1 FROM accounts WHERE id = ? AND self_id = ?)`,
		accountID, id, accountID, id, accountID, id, accountID, id, accountID, id, id, accountID, id).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("re-id lookup: %w", err)
	}
	return ok, nil
}

func (s *Store) reIDContact(ctx context.Context, accountID, oldID, newID string) (bool, error) {
	old, err := s.GetContact(ctx, accountID, oldID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	merged := old
	merged.ID = newID
	if cur, err := s.GetContact(ctx, accountID, newID); err == nil {
		merged = mergeContact(old, cur)
		if cur.Names.AliasSource == "local" {
			merged.Names.Alias, merged.Names.AliasSource = cur.Names.Alias, "local"
		}
		merged.Blocked = old.Blocked || cur.Blocked
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if err := s.writeContact(ctx, accountID, merged); err != nil {
		return false, err
	}
	oldPerson, err := s.PersonOf(ctx, LinkRef{AccountID: accountID, UserID: oldID})
	if err != nil {
		return false, err
	}
	newPerson, err := s.PersonOf(ctx, LinkRef{AccountID: accountID, UserID: newID})
	if err != nil {
		return false, err
	}
	if oldPerson != "" && newPerson == "" {
		if _, err := s.q.ExecContext(ctx, `UPDATE person_links SET user_id = ? WHERE account_id = ? AND user_id = ?`, newID, accountID, oldID); err != nil {
			return false, fmt.Errorf("re-id person link: %w", err)
		}
	}
	for _, col := range []string{"user_id", "other_user_id"} {
		acct := "account_id"
		if col == "other_user_id" {
			acct = "other_account_id"
		}
		if _, err := s.q.ExecContext(ctx, `UPDATE OR IGNORE person_unlinks SET `+col+` = ? WHERE `+acct+` = ? AND `+col+` = ?`, newID, accountID, oldID); err != nil {
			return false, fmt.Errorf("re-id unlinks: %w", err)
		}
		if _, err := s.q.ExecContext(ctx, `DELETE FROM person_unlinks WHERE `+acct+` = ? AND `+col+` = ?`, accountID, oldID); err != nil {
			return false, fmt.Errorf("re-id unlinks: %w", err)
		}
	}
	// Deleting the old row also drops its person link when the new contact already had one.
	if _, err := s.q.ExecContext(ctx, `DELETE FROM contacts WHERE account_id = ? AND id = ?`, accountID, oldID); err != nil {
		return false, fmt.Errorf("re-id contact: %w", err)
	}
	return true, nil
}

func (s *Store) reIDChat(ctx context.Context, accountID, oldID, newID string) (bool, error) {
	if _, err := s.GetChat(ctx, accountID, oldID); errors.Is(err, ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	stmts := []struct {
		what string
		sql  string
		args []any
	}{
		{"merge chat", `INSERT INTO chats (account_id, id, kind, name, avatar_media, unread_count, last_message_ts, last_message_seq,
			muted, archived, tags, pinned_ids, ephemeral_ttl_s, raw, updated_at)
			SELECT account_id, ?, kind, name, avatar_media, unread_count, last_message_ts, last_message_seq,
			muted, archived, tags, pinned_ids, ephemeral_ttl_s, raw, updated_at FROM chats WHERE account_id = ? AND id = ?
			ON CONFLICT(account_id, id) DO UPDATE SET
			name = COALESCE(chats.name, excluded.name), avatar_media = COALESCE(chats.avatar_media, excluded.avatar_media),
			unread_count = chats.unread_count + excluded.unread_count, muted = max(chats.muted, excluded.muted),
			archived = min(chats.archived, excluded.archived), ephemeral_ttl_s = COALESCE(chats.ephemeral_ttl_s, excluded.ephemeral_ttl_s),
			raw = COALESCE(chats.raw, excluded.raw), updated_at = excluded.updated_at`,
			[]any{newID, accountID, oldID}},
		// A message present in both chats (history synced under both ids) keeps the new copy.
		{"drop duplicate messages", `DELETE FROM messages WHERE account_id = ? AND chat_id = ? AND (id IN
			(SELECT id FROM messages WHERE account_id = ? AND chat_id = ?) OR client_id IN
			(SELECT client_id FROM messages WHERE account_id = ? AND chat_id = ? AND client_id IS NOT NULL))`,
			[]any{accountID, oldID, accountID, newID, accountID, newID}},
		{"move messages", `UPDATE messages SET chat_id = ? WHERE account_id = ? AND chat_id = ?`, []any{newID, accountID, oldID}},
		{"move members", `INSERT OR IGNORE INTO chat_members (account_id, chat_id, user_id, chat_name, role, joined_at, left_at)
			SELECT account_id, ?, user_id, chat_name, role, joined_at, left_at FROM chat_members WHERE account_id = ? AND chat_id = ?`,
			[]any{newID, accountID, oldID}},
		{"last message", `UPDATE chats SET (last_message_ts, last_message_seq) =
			(SELECT ts, seq FROM messages WHERE account_id = ? AND chat_id = ? ORDER BY ts DESC, seq DESC LIMIT 1)
			WHERE account_id = ? AND id = ?`, []any{accountID, newID, accountID, newID}},
		{"delete chat", `DELETE FROM chats WHERE account_id = ? AND id = ?`, []any{accountID, oldID}},
	}
	for _, st := range stmts {
		if _, err := s.q.ExecContext(ctx, st.sql, st.args...); err != nil {
			return false, fmt.Errorf("re-id chat: %s: %w", st.what, err)
		}
	}
	return true, nil
}

func (s *Store) reIDRefs(ctx context.Context, accountID, oldID, newID string) (bool, error) {
	quotedOld, quotedNew := strconv.Quote(oldID), strconv.Quote(newID)
	inAccount := `message_seq IN (SELECT seq FROM messages WHERE account_id = ?)`
	stmts := []struct {
		what string
		sql  string
		args []any
	}{
		{"senders", `UPDATE messages SET sender_id = ?, sender_name = CASE WHEN sender_name = ? THEN ? ELSE sender_name END
			WHERE account_id = ? AND sender_id = ?`, []any{newID, oldID, newID, accountID, oldID}},
		{"mentions and notices", `UPDATE messages SET mentions = replace(mentions, ?, ?),
			content = CASE WHEN type = 'system' THEN replace(content, ?, ?) ELSE content END
			WHERE account_id = ? AND ((mentions <> '[]' AND instr(mentions, ?) > 0) OR (type = 'system' AND instr(content, ?) > 0))`,
			[]any{quotedOld, quotedNew, quotedOld, quotedNew, accountID, quotedOld, quotedOld}},
		{"members", `INSERT OR IGNORE INTO chat_members (account_id, chat_id, user_id, chat_name, role, joined_at, left_at)
			SELECT account_id, chat_id, ?, chat_name, role, joined_at, left_at FROM chat_members WHERE account_id = ? AND user_id = ?`,
			[]any{newID, accountID, oldID}},
		{"old members", `DELETE FROM chat_members WHERE account_id = ? AND user_id = ?`, []any{accountID, oldID}},
		{"reactions", `UPDATE OR IGNORE reactions SET sender_id = ? WHERE sender_id = ? AND ` + inAccount, []any{newID, oldID, accountID}},
		{"old reactions", `DELETE FROM reactions WHERE sender_id = ? AND ` + inAccount, []any{oldID, accountID}},
		{"receipts", `UPDATE OR IGNORE receipts SET user_id = ? WHERE user_id = ? AND ` + inAccount, []any{newID, oldID, accountID}},
		{"old receipts", `DELETE FROM receipts WHERE user_id = ? AND ` + inAccount, []any{oldID, accountID}},
		{"request senders", `UPDATE requests SET from_id = ? WHERE account_id = ? AND from_id = ?`, []any{newID, accountID, oldID}},
		{"request chats", `UPDATE requests SET chat_id = ? WHERE account_id = ? AND chat_id = ?`, []any{newID, accountID, oldID}},
		{"self", `UPDATE accounts SET self_id = ? WHERE id = ? AND self_id = ?`, []any{newID, accountID, oldID}},
	}
	changed := false
	for _, st := range stmts {
		r, err := s.q.ExecContext(ctx, st.sql, st.args...)
		if err != nil {
			return false, fmt.Errorf("re-id %s: %w", st.what, err)
		}
		if n, _ := r.RowsAffected(); n > 0 && st.what != "old members" && st.what != "old reactions" && st.what != "old receipts" {
			changed = true
		}
	}
	return changed, nil
}

// IdentityCandidates lists the ids an account stores for users and direct chats, for an adapter
// to map to its current form.
func (s *Store) IdentityCandidates(ctx context.Context, accountID string) ([]string, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id FROM contacts WHERE account_id = ?
		UNION SELECT id FROM chats WHERE account_id = ? AND kind = 'direct'
		UNION SELECT DISTINCT sender_id FROM messages WHERE account_id = ?
		UNION SELECT DISTINCT user_id FROM chat_members WHERE account_id = ?`, accountID, accountID, accountID, accountID)
	if err != nil {
		return nil, fmt.Errorf("identity candidates: %w", err)
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
