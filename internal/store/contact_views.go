package store

import (
	"context"
	"fmt"

	"gimhq/chat-bridge/internal/model"
)

// contactDirectChat matches the direct chats with user ? of account ?: the chat id is the user
// (WhatsApp, Telegram), or the user is a current member of a direct room (Matrix).
const contactDirectChat = `ch.account_id = ? AND ch.kind = 'direct' AND (ch.id = ? OR ch.id IN
	(SELECT mm.chat_id FROM chat_members mm WHERE mm.account_id = ch.account_id AND mm.user_id = ? AND mm.left_at IS NULL))`

// ContactChats returns the direct chats with a contact and the other chats they are a current
// member of, most recent first.
func (s *Store) ContactChats(ctx context.Context, accountID, userID string) ([]model.Chat, error) {
	if _, err := s.GetContact(ctx, accountID, userID); err != nil {
		return nil, err
	}
	rows, err := s.q.QueryContext(ctx, `SELECT `+chatCols+` FROM chats WHERE chats.account_id = ? AND (chats.id = ? OR chats.id IN
		(SELECT m.chat_id FROM chat_members m WHERE m.account_id = chats.account_id AND m.user_id = ? AND m.left_at IS NULL))
		ORDER BY COALESCE(chats.last_message_ts,0) DESC, chats.id`, accountID, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("contact chats: %w", err)
	}
	return s.chatsWithLast(ctx, rows)
}

// ContactMessages pages a contact's messages, newest first. Scope "direct" is the whole
// conversation in the direct chats with them; "all" adds their own messages in other chats.
func (s *Store) ContactMessages(ctx context.Context, accountID, userID, scope, cursor string, limit int) ([]Stored, string, error) {
	if _, err := s.GetContact(ctx, accountID, userID); err != nil {
		return nil, "", err
	}
	where := `m.account_id = ? AND (EXISTS (SELECT 1 FROM chats ch WHERE ` + contactDirectChat + ` AND ch.id = m.chat_id)`
	args := []any{accountID, accountID, userID, userID}
	if scope == "all" {
		where += ` OR m.sender_id = ?`
		args = append(args, userID)
	}
	return s.pageMessages(ctx, "contact messages", where+`)`, args, cursor, limit)
}
