package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gimhq/chat-bridge/internal/model"
)

// Media is one row of the media table.
type Media struct {
	ID          string
	AccountID   string
	MessageSeq  int64
	SHA256      string
	Mime        string
	Size        int64
	FileName    string
	Width       int
	Height      int
	DurationMs  int64
	ThumbnailID string
	State       string
	RemoteRef   json.RawMessage
	CreatedAt   time.Time
	ExpiresAt   *time.Time
}

const mediaCols = `id, account_id, COALESCE(message_seq,0), COALESCE(sha256,''), mime, COALESCE(size,0), COALESCE(file_name,''),
	COALESCE(width,0), COALESCE(height,0), COALESCE(duration_ms,0), COALESCE(thumbnail_id,''), state, remote_ref, created_at, expires_at`

// Attachment renders the row as the API shape.
func (m Media) Attachment() model.Attachment {
	a := model.Attachment{
		MediaID: m.ID, Mime: m.Mime, Size: m.Size, FileName: m.FileName, Width: m.Width, Height: m.Height,
		DurationMs: m.DurationMs, SHA256: m.SHA256, ThumbnailMediaID: m.ThumbnailID, State: m.State, RemoteRef: m.RemoteRef,
	}
	if m.State == model.MediaReady {
		a.URL = "/v1/media/" + m.ID
	}
	return a
}

// UpsertMedia inserts or replaces a media row.
func (s *Store) UpsertMedia(ctx context.Context, m Media) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	var seq any
	if m.MessageSeq != 0 {
		seq = m.MessageSeq
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO media
		(id, account_id, message_seq, sha256, mime, size, file_name, width, height, duration_ms, thumbnail_id, state, remote_ref, created_at, expires_at, last_access)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET message_seq = COALESCE(excluded.message_seq, message_seq), sha256 = COALESCE(excluded.sha256, sha256),
		mime = excluded.mime, size = COALESCE(excluded.size, size), file_name = COALESCE(excluded.file_name, file_name),
		width = COALESCE(excluded.width, width), height = COALESCE(excluded.height, height), duration_ms = COALESCE(excluded.duration_ms, duration_ms),
		thumbnail_id = COALESCE(excluded.thumbnail_id, thumbnail_id), state = excluded.state, remote_ref = COALESCE(excluded.remote_ref, remote_ref),
		expires_at = excluded.expires_at`,
		m.ID, m.AccountID, seq, nullStr(m.SHA256), m.Mime, nullInt(m.Size), nullStr(m.FileName), nullInt(int64(m.Width)), nullInt(int64(m.Height)),
		nullInt(m.DurationMs), nullStr(m.ThumbnailID), m.State, rawOrNull(m.RemoteRef), unix(m.CreatedAt), nullTime(m.ExpiresAt), unix(time.Now()))
	if err != nil {
		return fmt.Errorf("upsert media: %w", err)
	}
	return nil
}

// AttachMedia links an upload to a message and clears its expiry.
func (s *Store) AttachMedia(ctx context.Context, id string, seq int64) error {
	_, err := s.q.ExecContext(ctx, `UPDATE media SET message_seq = ?, expires_at = NULL WHERE id = ?`, seq, id)
	if err != nil {
		return fmt.Errorf("attach media: %w", err)
	}
	return nil
}

// GetMedia returns one row.
func (s *Store) GetMedia(ctx context.Context, id string) (Media, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+mediaCols+` FROM media WHERE id = ?`, id)
	m, err := scanMedia(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	return m, err
}

// TouchMedia bumps last_access.
func (s *Store) TouchMedia(ctx context.Context, id string) {
	_, _ = s.q.ExecContext(ctx, `UPDATE media SET last_access = ? WHERE id = ?`, unix(time.Now()), id)
}

// ExpiredUploads lists unreferenced uploads past their expiry.
func (s *Store) ExpiredUploads(ctx context.Context, now time.Time) ([]Media, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+mediaCols+` FROM media WHERE message_seq IS NULL AND expires_at IS NOT NULL AND expires_at < ?`, unix(now))
	if err != nil {
		return nil, fmt.Errorf("expired uploads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Media
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMedia removes a row and reports whether other rows still reference the same hash.
func (s *Store) DeleteMedia(ctx context.Context, id string) (sha string, stillReferenced bool, err error) {
	m, err := s.GetMedia(ctx, id)
	if err != nil {
		return "", false, err
	}
	if _, err := s.q.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, id); err != nil {
		return "", false, fmt.Errorf("delete media: %w", err)
	}
	if m.SHA256 == "" {
		return "", false, nil
	}
	var n int
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE sha256 = ?`, m.SHA256).Scan(&n); err != nil {
		return "", false, err
	}
	return m.SHA256, n > 0, nil
}

func scanMedia(row scanner) (Media, error) {
	var m Media
	var ref sql.NullString
	var created int64
	var expires sql.NullInt64
	if err := row.Scan(&m.ID, &m.AccountID, &m.MessageSeq, &m.SHA256, &m.Mime, &m.Size, &m.FileName, &m.Width, &m.Height,
		&m.DurationMs, &m.ThumbnailID, &m.State, &ref, &created, &expires); err != nil {
		return m, err
	}
	if ref.Valid {
		m.RemoteRef = json.RawMessage(ref.String)
	}
	m.CreatedAt = fromUnix(created)
	m.ExpiresAt = timePtr(expires)
	return m, nil
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
