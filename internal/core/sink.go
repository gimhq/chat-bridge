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

// sink implements adapter.Sink on top of the store and event log.
type sink struct {
	c *Core
}

func (s *sink) Status(ctx context.Context, accountID string, st adapter.Status) error {
	c := s.c
	err := c.tx(ctx, func(tx *store.Store) error {
		selfID := ""
		if st.Self != nil {
			self := *st.Self
			self.IsSelf, self.IsContact = true, true
			if _, err := tx.UpsertContact(ctx, accountID, self); err != nil {
				return err
			}
			selfID = self.ID
		}
		if st.Status == model.StatusUnpaired {
			if err := tx.ClearAccountSession(ctx, accountID); err != nil {
				return err
			}
			if st.Error != nil {
				if err := tx.SetAccountStatus(ctx, accountID, model.StatusUnpaired, "", nil, st.Error); err != nil {
					return err
				}
			}
		} else if err := tx.SetAccountStatus(ctx, accountID, st.Status, selfID, st.Device, st.Error); err != nil {
			return err
		}
		return c.emitAccountStatus(ctx, tx, accountID)
	})
	if err != nil {
		return err
	}
	if st.Status == model.StatusConnected {
		c.clearLogin(accountID)
		go c.syncContacts(accountID)
	}
	return nil
}

// syncContacts pulls the address book once per connect.
func (c *Core) syncContacts(accountID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, ad, err := c.adapterFor(ctx, accountID)
	if err != nil {
		return
	}
	contacts, err := ad.ListContacts(ctx, accountID)
	if err != nil {
		c.log.Warn("list contacts", "account", accountID, "err", err)
		return
	}
	err = c.tx(ctx, func(tx *store.Store) error {
		for _, ct := range contacts {
			if _, err := tx.UpsertContact(ctx, accountID, ct); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.log.Error("sync contacts", "account", accountID, "err", err)
	}
	c.log.Info("contacts synced", "account", accountID, "count", len(contacts))
}

func (s *sink) LoginStep(ctx context.Context, accountID string, step model.LoginStep) error {
	return s.c.applyStep(ctx, accountID, step)
}

func (s *sink) PutMedia(ctx context.Context, accountID, mediaID string, meta adapter.MediaMeta, r io.Reader) (model.Attachment, error) {
	return s.c.storeMedia(ctx, accountID, mediaID, meta, r)
}

func (s *sink) Events(ctx context.Context, accountID string, evs []adapter.Event) error {
	c := s.c
	var pending []string
	err := c.tx(ctx, func(tx *store.Store) error {
		for _, ev := range evs {
			ids, err := c.ingest(ctx, tx, accountID, ev)
			if err != nil {
				return err
			}
			pending = append(pending, ids...)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, id := range pending {
		go c.fetchMedia(accountID, id)
	}
	return nil
}

// ingest applies one adapter event inside tx and returns media ids to download.
func (c *Core) ingest(ctx context.Context, tx *store.Store, accountID string, ev adapter.Event) ([]string, error) {
	switch ev.Kind {
	case adapter.EvMessage:
		return c.ingestMessage(ctx, tx, accountID, ev)
	case adapter.EvMessageUpdate:
		m, err := tx.GetMessage(ctx, accountID, ev.ChatID, ev.MessageID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		at := ev.At
		upd, err := tx.UpdateMessage(ctx, m.Seq(), func(m *model.Message) {
			if ev.Content != nil {
				m.Content = *ev.Content
			}
			if !at.IsZero() {
				m.EditedAt = &at
			}
		})
		if err != nil {
			return nil, err
		}
		return nil, emit(ctx, tx, accountID, model.EvMessageUpdated, upd.Message)
	case adapter.EvMessageDelete:
		m, err := tx.GetMessage(ctx, accountID, ev.ChatID, ev.MessageID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		at := ev.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		if _, err := tx.UpdateMessage(ctx, m.Seq(), func(m *model.Message) {
			m.DeletedAt = &at
			m.Content = model.Content{Type: model.ContentDeleted}
		}); err != nil {
			return nil, err
		}
		return nil, emit(ctx, tx, accountID, model.EvMessageDeleted, map[string]any{"chat_id": ev.ChatID, "message_id": ev.MessageID, "deleted_at": at})
	case adapter.EvReaction:
		m, err := tx.GetMessage(ctx, accountID, ev.ChatID, ev.MessageID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if err := tx.SetReaction(ctx, m.Seq(), ev.UserID, ev.Emoji, ev.Removed, orNow(ev.At)); err != nil {
			return nil, err
		}
		return nil, emit(ctx, tx, accountID, model.EvMessageReaction, map[string]any{
			"chat_id": ev.ChatID, "message_id": ev.MessageID, "sender_id": ev.UserID, "emoji": ev.Emoji, "removed": ev.Removed})
	case adapter.EvReceipt:
		for _, id := range ev.MessageIDs {
			m, err := tx.GetMessage(ctx, accountID, ev.ChatID, id)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			status, err := tx.SetReceipt(ctx, m.Seq(), ev.UserID, ev.Receipt, orNow(ev.At))
			if err != nil {
				return nil, err
			}
			if m.FromMe && status != m.Status {
				upd, err := tx.GetMessageBySeq(ctx, m.Seq())
				if err != nil {
					return nil, err
				}
				if err := emit(ctx, tx, accountID, model.EvMessageUpdated, upd.Message); err != nil {
					return nil, err
				}
			}
		}
		return nil, emit(ctx, tx, accountID, model.EvMessageReceipt, map[string]any{
			"chat_id": ev.ChatID, "message_ids": ev.MessageIDs, "user_id": ev.UserID, "kind": ev.Receipt})
	case adapter.EvChat:
		if ev.Chat == nil {
			return nil, nil
		}
		ch := *ev.Chat
		ch.AccountID = accountID
		created, err := tx.UpsertChat(ctx, ch)
		if err != nil {
			return nil, err
		}
		return nil, c.emitChat(ctx, tx, accountID, ch.ID, created)
	case adapter.EvMember:
		if ev.Member == nil {
			return nil, nil
		}
		m := ev.Member
		if _, err := tx.UpsertChat(ctx, model.Chat{AccountID: accountID, ID: m.ChatID, Kind: model.ChatGroup}); err != nil {
			return nil, err
		}
		if err := tx.UpsertMember(ctx, accountID, m.ChatID, m.UserID, m.ChatName, m.Role, m.Left); err != nil {
			return nil, err
		}
		return nil, c.emitChat(ctx, tx, accountID, m.ChatID, false)
	case adapter.EvContact:
		if ev.Contact == nil {
			return nil, nil
		}
		changed, err := tx.UpsertContact(ctx, accountID, *ev.Contact)
		if err != nil || !changed {
			return nil, err
		}
		ct, err := tx.GetContact(ctx, accountID, ev.Contact.ID)
		if err != nil {
			return nil, err
		}
		return nil, emit(ctx, tx, accountID, model.EvContactUpdated, ct)
	case adapter.EvTyping:
		return nil, emit(ctx, tx, accountID, model.EvChatTyping, map[string]any{"chat_id": ev.ChatID, "user_id": ev.UserID, "state": ev.State})
	case adapter.EvPresence:
		return nil, emit(ctx, tx, accountID, model.EvPresence, map[string]any{"user_id": ev.UserID, "state": ev.State, "last_seen": ev.LastSeen})
	case adapter.EvPlatform:
		return nil, emit(ctx, tx, accountID, model.EvPlatform, map[string]any{
			"platform_type": ev.PlatformType, "chat_id": ev.ChatID, "user_id": ev.UserID, "message_id": ev.MessageID, "raw": ev.Raw})
	}
	c.log.Warn("unknown adapter event kind", "kind", ev.Kind)
	return nil, nil
}

func (c *Core) ingestMessage(ctx context.Context, tx *store.Store, accountID string, ev adapter.Event) ([]string, error) {
	if ev.Message == nil {
		return nil, nil
	}
	m := *ev.Message
	m.AccountID = accountID
	chat := model.Chat{AccountID: accountID, ID: m.ChatID}
	if ev.Chat != nil {
		chat = *ev.Chat
		chat.AccountID, chat.ID = accountID, m.ChatID
	}
	created, err := tx.UpsertChat(ctx, chat)
	if err != nil {
		return nil, err
	}
	senderChanged := false
	if ev.Sender != nil && ev.Sender.ID == m.Sender.ID {
		if senderChanged, err = tx.UpsertContact(ctx, accountID, *ev.Sender); err != nil {
			return nil, err
		}
	}
	if m.Sender.Name == "" {
		if ct, err := tx.GetContact(ctx, accountID, m.Sender.ID); err == nil {
			m.Sender.Name = store.ResolveName(ct, tx.MemberChatName(ctx, accountID, m.ChatID, m.Sender.ID))
		} else {
			m.Sender.Name = m.Sender.ID
		}
	}
	if !m.FromMe {
		if err := tx.UpsertMember(ctx, accountID, m.ChatID, m.Sender.ID, m.Sender.ChatName, "", false); err != nil {
			return nil, err
		}
	}
	// Attachments: rows carry state; bytes come later.
	var toFetch []string
	seq, inserted, err := tx.InsertMessage(ctx, m, ev.Raw)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return nil, nil
	}
	for i := range m.Content.Attachments {
		att := m.Content.Attachments[i]
		state := att.State
		if state == "" || state == model.MediaPending || state == model.MediaRemote {
			if !ev.Backfill && c.policy.wants(m.Content.Type, att.Size) {
				state = model.MediaPending
				toFetch = append(toFetch, att.MediaID)
			} else {
				state = model.MediaRemote
			}
		}
		if err := tx.UpsertMedia(ctx, store.Media{
			ID: att.MediaID, AccountID: accountID, MessageSeq: seq, Mime: att.Mime, Size: att.Size, FileName: att.FileName,
			Width: att.Width, Height: att.Height, DurationMs: att.DurationMs, State: state, RemoteRef: att.RemoteRef, SHA256: att.SHA256,
		}); err != nil {
			return nil, err
		}
	}
	if err := tx.TouchChat(ctx, accountID, m.ChatID, m.Timestamp, seq, !m.FromMe && !ev.Backfill); err != nil {
		return nil, err
	}
	if err := c.emitChat(ctx, tx, accountID, m.ChatID, created); err != nil {
		return nil, err
	}
	if senderChanged {
		ct, err := tx.GetContact(ctx, accountID, m.Sender.ID)
		if err == nil {
			if err := emit(ctx, tx, accountID, model.EvContactUpdated, ct); err != nil {
				return nil, err
			}
		}
	}
	stored, err := tx.GetMessageBySeq(ctx, seq)
	if err != nil {
		return nil, err
	}
	return toFetch, emit(ctx, tx, accountID, model.EvMessageNew, stored.Message)
}

// emitChat emits chat.new when created, otherwise chat.updated only for explicit chat events.
func (c *Core) emitChat(ctx context.Context, tx *store.Store, accountID, chatID string, created bool) error {
	ch, err := tx.GetChat(ctx, accountID, chatID)
	if err != nil {
		return err
	}
	typ := model.EvChatUpdated
	if created {
		typ = model.EvChatNew
	}
	return emit(ctx, tx, accountID, typ, ch)
}

func orNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}

// fetchMedia downloads a pending attachment through the adapter and flips it to ready.
func (c *Core) fetchMedia(accountID, mediaID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, ad, err := c.adapterFor(ctx, accountID)
	if err != nil {
		return
	}
	md, err := c.st.GetMedia(ctx, mediaID)
	if err != nil {
		return
	}
	pr, pw := io.Pipe()
	metaCh := make(chan adapter.MediaMeta, 1)
	go func() {
		meta, err := ad.FetchMedia(ctx, accountID, mediaID, md.RemoteRef, pw)
		if err != nil {
			_ = pw.CloseWithError(err)
		} else {
			_ = pw.Close()
		}
		metaCh <- meta
	}()
	att, err := c.storeMedia(ctx, accountID, mediaID, adapter.MediaMeta{Mime: md.Mime, FileName: md.FileName, Width: md.Width, Height: md.Height, DurationMs: md.DurationMs}, pr)
	meta := <-metaCh
	if errors.Is(err, adapter.ErrMediaStoredBySink) {
		return // the adapter uploaded the bytes itself
	}
	if err != nil {
		c.log.Warn("fetch media", "account", accountID, "media", mediaID, "err", err)
		_ = c.tx(ctx, func(tx *store.Store) error {
			md.State = model.MediaFailed
			return tx.UpsertMedia(ctx, md)
		})
		return
	}
	if meta.Mime != "" && meta.Mime != att.Mime {
		_ = c.tx(ctx, func(tx *store.Store) error {
			md.Mime, md.State, md.SHA256, md.Size = meta.Mime, model.MediaReady, att.SHA256, att.Size
			return tx.UpsertMedia(ctx, md)
		})
	}
}

// storeMedia writes bytes to the blob store and marks the row ready, emitting message.updated
// when the row belongs to a message.
func (c *Core) storeMedia(ctx context.Context, accountID, mediaID string, meta adapter.MediaMeta, r io.Reader) (model.Attachment, error) {
	sha, size, err := c.blobs.Put(r)
	if err != nil {
		return model.Attachment{}, err
	}
	var out model.Attachment
	err = c.tx(ctx, func(tx *store.Store) error {
		md, err := tx.GetMedia(ctx, mediaID)
		if errors.Is(err, store.ErrNotFound) {
			md = store.Media{ID: mediaID, AccountID: accountID}
		} else if err != nil {
			return err
		}
		md.SHA256, md.Size, md.State = sha, size, model.MediaReady
		if meta.Mime != "" {
			md.Mime = meta.Mime
		}
		if md.Mime == "" {
			md.Mime = "application/octet-stream"
		}
		if meta.FileName != "" {
			md.FileName = meta.FileName
		}
		if meta.Width > 0 {
			md.Width, md.Height = meta.Width, meta.Height
		}
		if meta.DurationMs > 0 {
			md.DurationMs = meta.DurationMs
		}
		if err := tx.UpsertMedia(ctx, md); err != nil {
			return err
		}
		out = md.Attachment()
		if md.MessageSeq == 0 {
			return nil
		}
		m, err := tx.GetMessageBySeq(ctx, md.MessageSeq)
		if err != nil {
			return err
		}
		return emit(ctx, tx, accountID, model.EvMessageUpdated, m.Message)
	})
	return out, err
}
