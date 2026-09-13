package core

import (
	"context"
	"errors"
	"io"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// MessageQuery narrows ListMessages.
type MessageQuery struct {
	Cursor string
	Before time.Time
	After  time.Time
	Limit  int
}

// ListMessages pages a chat.
func (c *Core) ListMessages(ctx context.Context, accountID, chatID string, q MessageQuery) ([]model.Message, string, error) {
	if _, err := c.st.GetAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		return nil, "", errNotFound("account")
	}
	rows, next, err := c.st.ListMessages(ctx, accountID, chatID, q.Cursor, q.Before, q.After, q.Limit)
	if err != nil {
		if q.Cursor != "" && err.Error() == "bad cursor" {
			return nil, "", errInvalid("bad cursor")
		}
		return nil, "", err
	}
	out := make([]model.Message, len(rows))
	for i := range rows {
		out[i] = rows[i].Message
	}
	return out, next, nil
}

// GetMessage returns one message; raw attaches the adapter payload.
func (c *Core) GetMessage(ctx context.Context, accountID, id string, raw bool) (model.Message, error) {
	m, err := c.st.FindMessage(ctx, accountID, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Message{}, errNotFound("message")
	}
	if err != nil {
		return model.Message{}, err
	}
	if raw {
		m.Raw, _ = c.st.RawPayload(ctx, m.Seq())
	}
	return m.Message, nil
}

var sendable = map[string]string{
	model.ContentText: adapter.CapSendText, model.ContentImage: adapter.CapSendMedia, model.ContentVideo: adapter.CapSendMedia,
	model.ContentAudio: adapter.CapSendMedia, model.ContentVoice: adapter.CapSendMedia, model.ContentFile: adapter.CapSendMedia,
	model.ContentSticker: adapter.CapSendMedia, model.ContentLocation: adapter.CapSendLocation, model.ContentContact: adapter.CapSendContact,
}

// Send delivers a message. The bool reports whether an existing message was returned (client_id replay).
func (c *Core) Send(ctx context.Context, accountID, chatID string, req model.SendRequest) (model.Message, bool, error) {
	row, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return model.Message{}, false, err
	}
	if req.ClientID != "" {
		if existing, err := c.st.FindByClientID(ctx, accountID, chatID, req.ClientID); err == nil {
			return existing.Message, true, nil
		}
	}
	capNeeded, ok := sendable[req.Content.Type]
	if !ok {
		return model.Message{}, false, errInvalid("content type %q is not sendable", req.Content.Type)
	}
	if err := requireCap(ad, capNeeded); err != nil {
		return model.Message{}, false, err
	}
	if req.ReplyTo != "" {
		if err := requireCap(ad, adapter.CapReply); err != nil {
			return model.Message{}, false, err
		}
	}
	if req.ThreadID != "" {
		if err := requireCap(ad, adapter.CapThread); err != nil {
			return model.Message{}, false, err
		}
	}
	switch req.Content.Type {
	case model.ContentText:
		if req.Content.Text == "" {
			return model.Message{}, false, errInvalid("text is required")
		}
	case model.ContentLocation:
		if req.Content.Location == nil {
			return model.Message{}, false, errInvalid("location is required")
		}
	case model.ContentContact:
		if len(req.Content.Contacts) == 0 {
			return model.Message{}, false, errInvalid("contacts is required")
		}
	default:
		if len(req.Content.Attachments) == 0 {
			return model.Message{}, false, errInvalid("attachments is required for %s", req.Content.Type)
		}
	}
	// Resolve uploads.
	var uploads []string
	for i := range req.Content.Attachments {
		md, err := c.st.GetMedia(ctx, req.Content.Attachments[i].MediaID)
		if errors.Is(err, store.ErrNotFound) {
			return model.Message{}, false, errNotFound("media " + req.Content.Attachments[i].MediaID)
		}
		if err != nil {
			return model.Message{}, false, err
		}
		if md.State != model.MediaReady {
			return model.Message{}, false, errInvalid("media %s is %s", md.ID, md.State)
		}
		req.Content.Attachments[i] = md.Attachment()
		if md.MessageSeq == 0 {
			uploads = append(uploads, md.ID)
		}
	}

	var replyTarget *model.Message
	if req.ReplyTo != "" {
		if t, err := c.st.GetMessage(ctx, accountID, chatID, req.ReplyTo); err == nil {
			replyTarget = &t.Message
		}
	}
	sent, err := ad.SendMessage(ctx, accountID, adapter.SendRequest{
		ChatID: chatID, ClientID: req.ClientID, Content: req.Content, ReplyTo: req.ReplyTo, ReplyTarget: replyTarget,
		ThreadID: req.ThreadID, Mentions: req.Mentions, Media: &mediaSource{c: c},
	})
	if err != nil {
		return model.Message{}, false, err
	}
	sent.AccountID, sent.ChatID, sent.FromMe, sent.ClientID = accountID, chatID, true, req.ClientID
	if sent.ChatID == "" {
		sent.ChatID = chatID
	}
	if sent.Sender.ID == "" {
		sent.Sender.ID = row.SelfID
	}
	if sent.Status == "" {
		sent.Status = model.MsgSent
	}
	if sent.Timestamp.IsZero() {
		sent.Timestamp = time.Now().UTC()
	}
	if sent.Content.Type == "" {
		sent.Content = req.Content
	}
	sent.ReplyTo, sent.ThreadID, sent.Mentions = req.ReplyTo, req.ThreadID, req.Mentions
	if self, err := c.st.GetContact(ctx, accountID, sent.Sender.ID); err == nil {
		sent.Sender.Name = self.Name
	}
	var out model.Message
	err = c.tx(ctx, func(tx *store.Store) error {
		created, err := tx.UpsertChat(ctx, model.Chat{AccountID: accountID, ID: sent.ChatID})
		if err != nil {
			return err
		}
		seq, _, err := tx.InsertMessage(ctx, sent, nil)
		if err != nil {
			return err
		}
		for _, id := range uploads {
			if err := tx.AttachMedia(ctx, id, seq); err != nil {
				return err
			}
		}
		if err := tx.TouchChat(ctx, accountID, sent.ChatID, sent.Timestamp, seq, false); err != nil {
			return err
		}
		if err := c.emitChat(ctx, tx, accountID, sent.ChatID, created); err != nil {
			return err
		}
		stored, err := tx.GetMessageBySeq(ctx, seq)
		if err != nil {
			return err
		}
		out = stored.Message
		return emit(ctx, tx, accountID, model.EvMessageNew, out)
	})
	return out, false, err
}

// mediaSource lets adapters read upload bytes while sending.
type mediaSource struct {
	c *Core
}

func (m *mediaSource) Open(ctx context.Context, mediaID string) (io.ReadCloser, model.Attachment, error) {
	md, err := m.c.st.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, model.Attachment{}, err
	}
	f, err := m.c.blobs.Open(md.SHA256)
	if err != nil {
		return nil, model.Attachment{}, err
	}
	return f, md.Attachment(), nil
}

// Edit changes a sent message's text.
func (c *Core) Edit(ctx context.Context, accountID, id string, content model.Content) (model.Message, error) {
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return model.Message{}, err
	}
	ed, ok := ad.(adapter.Editor)
	if !ok || !ad.Info().Has(adapter.CapEdit) {
		return model.Message{}, errUnsupported(adapter.CapEdit)
	}
	m, err := c.st.FindMessage(ctx, accountID, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Message{}, errNotFound("message")
	}
	if err != nil {
		return model.Message{}, err
	}
	if !m.FromMe {
		return model.Message{}, errInvalid("only own messages can be edited")
	}
	if content.Type != model.ContentText || content.Text == "" {
		return model.Message{}, errInvalid("edit requires text content")
	}
	if _, err := ed.EditMessage(ctx, accountID, m.ChatID, m.ID, content); err != nil {
		return model.Message{}, err
	}
	now := time.Now().UTC()
	var out model.Message
	err = c.tx(ctx, func(tx *store.Store) error {
		upd, err := tx.UpdateMessage(ctx, m.Seq(), func(mm *model.Message) {
			mm.Content.Text = content.Text
			mm.Content.Format = content.Format
			mm.EditedAt = &now
		})
		if err != nil {
			return err
		}
		out = upd.Message
		return emit(ctx, tx, accountID, model.EvMessageUpdated, out)
	})
	return out, err
}

// Delete unsends a message for everyone.
func (c *Core) Delete(ctx context.Context, accountID, id string) (model.Message, error) {
	_, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return model.Message{}, err
	}
	del, ok := ad.(adapter.Deleter)
	if !ok || !ad.Info().Has(adapter.CapDelete) {
		return model.Message{}, errUnsupported(adapter.CapDelete)
	}
	m, err := c.st.FindMessage(ctx, accountID, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Message{}, errNotFound("message")
	}
	if err != nil {
		return model.Message{}, err
	}
	if err := del.DeleteMessage(ctx, accountID, m.ChatID, m.ID, m.Sender.ID); err != nil {
		return model.Message{}, err
	}
	now := time.Now().UTC()
	var out model.Message
	err = c.tx(ctx, func(tx *store.Store) error {
		upd, err := tx.UpdateMessage(ctx, m.Seq(), func(mm *model.Message) {
			mm.DeletedAt = &now
			mm.Content = model.Content{Type: model.ContentDeleted}
		})
		if err != nil {
			return err
		}
		out = upd.Message
		return emit(ctx, tx, accountID, model.EvMessageDeleted, map[string]any{"chat_id": m.ChatID, "message_id": m.ID, "deleted_at": now})
	})
	return out, err
}

// React adds or removes the account's reaction.
func (c *Core) React(ctx context.Context, accountID, id, emoji string, remove bool) error {
	row, ad, err := c.connected(ctx, accountID)
	if err != nil {
		return err
	}
	re, ok := ad.(adapter.Reactor)
	if !ok || !ad.Info().Has(adapter.CapReaction) {
		return errUnsupported(adapter.CapReaction)
	}
	m, err := c.st.FindMessage(ctx, accountID, id)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("message")
	}
	if err != nil {
		return err
	}
	if err := re.React(ctx, accountID, m.ChatID, m.ID, m.Sender.ID, emoji, remove); err != nil {
		return err
	}
	return c.tx(ctx, func(tx *store.Store) error {
		if err := tx.SetReaction(ctx, m.Seq(), row.SelfID, emoji, remove, time.Now()); err != nil {
			return err
		}
		return emit(ctx, tx, accountID, model.EvMessageReaction, map[string]any{
			"chat_id": m.ChatID, "message_id": m.ID, "sender_id": row.SelfID, "emoji": emoji, "removed": remove})
	})
}
