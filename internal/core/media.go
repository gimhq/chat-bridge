package core

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

const uploadTTL = time.Hour

// Upload stores consumer bytes as an unreferenced upload (media_id upl_…).
func (c *Core) Upload(ctx context.Context, accountID string, r io.Reader, meta adapter.MediaMeta) (model.Attachment, error) {
	if _, err := c.st.GetAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		return model.Attachment{}, errNotFound("account")
	}
	sha, size, err := c.blobs.Put(r)
	if err != nil {
		return model.Attachment{}, err
	}
	if meta.Mime == "" {
		meta.Mime = "application/octet-stream"
	}
	exp := time.Now().Add(uploadTTL)
	md := store.Media{
		ID: "upl_" + uuid.NewString(), AccountID: accountID, SHA256: sha, Mime: meta.Mime, Size: size, FileName: meta.FileName,
		Width: meta.Width, Height: meta.Height, DurationMs: meta.DurationMs, State: model.MediaReady, ExpiresAt: &exp,
	}
	if err := c.st.UpsertMedia(ctx, md); err != nil {
		return model.Attachment{}, err
	}
	return md.Attachment(), nil
}

// MediaFile is what GET /media/{id} serves.
type MediaFile struct {
	Attachment model.Attachment
	Path       string
}

// GetMedia returns metadata and the on-disk path (empty unless ready).
func (c *Core) GetMedia(ctx context.Context, id string) (MediaFile, error) {
	md, err := c.st.GetMedia(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return MediaFile{}, errNotFound("media")
	}
	if err != nil {
		return MediaFile{}, err
	}
	mf := MediaFile{Attachment: md.Attachment()}
	if md.State == model.MediaReady && md.SHA256 != "" {
		mf.Path = c.blobs.Path(md.SHA256)
		c.st.TouchMedia(ctx, id)
	}
	return mf, nil
}

// FetchMedia starts a download for a remote/purged/failed attachment.
func (c *Core) FetchMedia(ctx context.Context, id string) (model.Attachment, error) {
	md, err := c.st.GetMedia(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Attachment{}, errNotFound("media")
	}
	if err != nil {
		return model.Attachment{}, err
	}
	if md.State == model.MediaReady || md.State == model.MediaPending {
		return md.Attachment(), nil
	}
	if len(md.RemoteRef) == 0 {
		return model.Attachment{}, errInvalid("media has no remote reference")
	}
	if _, _, err := c.connected(ctx, md.AccountID); err != nil {
		return model.Attachment{}, err
	}
	md.State = model.MediaPending
	if err := c.st.UpsertMedia(ctx, md); err != nil {
		return model.Attachment{}, err
	}
	go c.fetchMedia(md.AccountID, id)
	return md.Attachment(), nil
}

// gcLoop purges expired uploads and old events.
func (c *Core) gcLoop() {
	defer c.wg.Done()
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		c.gcOnce()
		select {
		case <-c.stopCh:
			return
		case <-t.C:
		}
	}
}

func (c *Core) gcOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	now := time.Now()
	expired, err := c.st.ExpiredUploads(ctx, now)
	if err != nil {
		c.log.Warn("gc uploads", "err", err)
	}
	for _, md := range expired {
		sha, referenced, err := c.st.DeleteMedia(ctx, md.ID)
		if err != nil {
			continue
		}
		if sha != "" && !referenced {
			_ = c.blobs.Remove(sha)
		}
	}
	if err := c.st.PruneEvents(ctx, now, c.retain); err != nil {
		c.log.Warn("gc events", "err", err)
	}
	if err := c.expireRequests(ctx, now); err != nil {
		c.log.Warn("gc requests", "err", err)
	}
}
