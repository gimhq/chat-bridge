// Package store persists bridge state in SQLite. Schema and semantics follow docs/storage.md.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a uniqueness rule is violated.
var ErrConflict = errors.New("conflict")

// queryer is satisfied by *sql.DB and *sql.Tx.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store wraps the SQLite connection. Inside Tx the same type runs on the transaction.
type Store struct {
	db *sql.DB
	q  queryer
}

var migrations = []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5}

// Open opens (or creates) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=cache_size(-32768)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db, q: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("schema_version: %w", err)
	}
	var v int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema %d is newer than this binary (%d)", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_version; INSERT INTO schema_version(version) VALUES (?)`, i+1); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Tx runs fn inside one transaction. The Store passed to fn must not escape it.
func (s *Store) Tx(ctx context.Context, fn func(tx *Store) error) error {
	if _, ok := s.q.(*sql.Tx); ok {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(&Store{db: s.db, q: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func unix(t time.Time) int64 { return t.Unix() }

func fromUnix(v int64) time.Time { return time.Unix(v, 0).UTC() }

func nullTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Unix()
}

func timePtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromUnix(v.Int64)
	return &t
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonOrNull(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

func rawOrNull(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	return string(r)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const schemaV2 = `ALTER TABLE accounts ADD COLUMN adapter TEXT NOT NULL DEFAULT '';`

// schemaV3 adds full-text search over message text. Trigram tokens make substring search work
// for CJK text (no word boundaries); the index is external-content, so message rows stay the
// single source of truth and the triggers keep it in step.
const schemaV3 = `
CREATE VIRTUAL TABLE messages_fts USING fts5(text, content='messages', content_rowid='seq', tokenize='trigram');
CREATE TRIGGER messages_fts_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, text) VALUES (new.seq, new.text);
END;
CREATE TRIGGER messages_fts_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text) VALUES ('delete', old.seq, old.text);
END;
CREATE TRIGGER messages_fts_au AFTER UPDATE OF text ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text) VALUES ('delete', old.seq, old.text);
  INSERT INTO messages_fts(rowid, text) VALUES (new.seq, new.text);
END;
INSERT INTO messages_fts(messages_fts) VALUES ('rebuild');
`

// schemaV4 adds requests (api.md §4.8): invites, join requests and calls waiting for the owner.
// platform_key is the adapter's stable key, so re-emitted requests update the same row.
const schemaV4 = `
CREATE TABLE requests (
  id           TEXT PRIMARY KEY,
  account_id   TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  platform_key TEXT NOT NULL,
  kind         TEXT NOT NULL,
  state        TEXT NOT NULL,
  from_id      TEXT,
  from_name    TEXT,
  chat_id      TEXT,
  chat_name    TEXT,
  chat_kind    TEXT,
  message      TEXT,
  call_kind    TEXT,
  platform_ref TEXT,
  raw          TEXT,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER,
  answered_at  INTEGER,
  updated_at   INTEGER NOT NULL,
  UNIQUE (account_id, platform_key)
);
CREATE INDEX requests_open ON requests(account_id, state, created_at DESC);
CREATE INDEX requests_expiry ON requests(expires_at) WHERE state = 'pending';
`

// schemaV5 adds persons (api.md §3.8): a bridge-local identity over contacts on several accounts.
// contacts.phone_norm (digits only, at least 7) drives auto-linking and suggestions.
const schemaV5 = `
ALTER TABLE contacts ADD COLUMN phone_norm TEXT;
UPDATE contacts SET phone_norm = CASE
  WHEN length(replace(replace(replace(replace(replace(replace(COALESCE(phone,''),'+',''),' ',''),'-',''),'(',''),')',''),'.','')) >= 7
  THEN replace(replace(replace(replace(replace(replace(phone,'+',''),' ',''),'-',''),'(',''),')',''),'.','') END;
CREATE INDEX contacts_phone_norm ON contacts(phone_norm) WHERE phone_norm IS NOT NULL;
CREATE TABLE persons (
  id          TEXT PRIMARY KEY,
  name        TEXT,
  tags        TEXT NOT NULL DEFAULT '[]',
  notes       TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE TABLE person_links (
  account_id  TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  person_id   TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
  source      TEXT NOT NULL,
  linked_at   INTEGER NOT NULL,
  PRIMARY KEY (account_id, user_id),
  FOREIGN KEY (account_id, user_id) REFERENCES contacts(account_id, id) ON DELETE CASCADE
);
CREATE INDEX person_links_person ON person_links(person_id);
CREATE TABLE person_unlinks (
  account_id TEXT NOT NULL, user_id TEXT NOT NULL,
  other_account_id TEXT NOT NULL, other_user_id TEXT NOT NULL,
  PRIMARY KEY (account_id, user_id, other_account_id, other_user_id)
);
`

const schemaV1 = `
CREATE TABLE accounts (
  id            TEXT PRIMARY KEY,
  platform      TEXT NOT NULL,
  status        TEXT NOT NULL,
  self_id       TEXT,
  config        TEXT NOT NULL DEFAULT '{}',
  device        TEXT NOT NULL DEFAULT '{}',
  login_flow    TEXT,
  login_ident   TEXT,
  login_at      INTEGER,
  error         TEXT,
  created_at    INTEGER NOT NULL,
  connected_at  INTEGER
);

CREATE TABLE contacts (
  account_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  id            TEXT NOT NULL,
  handle        TEXT,
  phone         TEXT,
  email         TEXT,
  names         TEXT NOT NULL DEFAULT '{}',
  avatar_media  TEXT,
  is_self       INTEGER NOT NULL DEFAULT 0,
  is_contact    INTEGER NOT NULL DEFAULT 0,
  blocked       INTEGER NOT NULL DEFAULT 0,
  bio           TEXT,
  raw           TEXT,
  updated_at    INTEGER NOT NULL,
  PRIMARY KEY (account_id, id)
);
CREATE INDEX contacts_phone ON contacts(phone) WHERE phone IS NOT NULL;

CREATE TABLE chats (
  account_id        TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  id                TEXT NOT NULL,
  kind              TEXT NOT NULL,
  name              TEXT,
  avatar_media      TEXT,
  unread_count      INTEGER NOT NULL DEFAULT 0,
  last_message_ts   INTEGER,
  last_message_seq  INTEGER,
  muted             INTEGER NOT NULL DEFAULT 0,
  archived          INTEGER NOT NULL DEFAULT 0,
  tags              TEXT NOT NULL DEFAULT '[]',
  pinned_ids        TEXT NOT NULL DEFAULT '[]',
  ephemeral_ttl_s   INTEGER,
  raw               TEXT,
  updated_at        INTEGER NOT NULL,
  PRIMARY KEY (account_id, id)
);
CREATE INDEX chats_recent ON chats(account_id, archived, last_message_ts DESC);

CREATE TABLE chat_members (
  account_id  TEXT NOT NULL,
  chat_id     TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  chat_name   TEXT,
  role        TEXT NOT NULL DEFAULT 'member',
  joined_at   INTEGER,
  left_at     INTEGER,
  PRIMARY KEY (account_id, chat_id, user_id),
  FOREIGN KEY (account_id, chat_id) REFERENCES chats(account_id, id) ON DELETE CASCADE
);

CREATE TABLE messages (
  seq           INTEGER PRIMARY KEY,
  account_id    TEXT NOT NULL,
  chat_id       TEXT NOT NULL,
  id            TEXT NOT NULL,
  sender_id     TEXT NOT NULL,
  sender_name   TEXT,
  from_me       INTEGER NOT NULL,
  ts            INTEGER NOT NULL,
  received_at   INTEGER NOT NULL,
  type          TEXT NOT NULL,
  text          TEXT,
  content       TEXT NOT NULL,
  reply_to      TEXT,
  thread_id     TEXT,
  mentions      TEXT NOT NULL DEFAULT '[]',
  forwarded     INTEGER NOT NULL DEFAULT 0,
  ephemeral     TEXT,
  status        TEXT,
  client_id     TEXT,
  edited_at     INTEGER,
  deleted_at    INTEGER,
  UNIQUE (account_id, chat_id, id),
  FOREIGN KEY (account_id, chat_id) REFERENCES chats(account_id, id) ON DELETE CASCADE
);
CREATE INDEX messages_timeline ON messages(account_id, chat_id, ts DESC, seq DESC);
CREATE INDEX messages_by_id ON messages(account_id, id);
CREATE UNIQUE INDEX messages_client_id ON messages(account_id, chat_id, client_id) WHERE client_id IS NOT NULL;

CREATE TABLE message_versions (
  message_seq INTEGER NOT NULL REFERENCES messages(seq) ON DELETE CASCADE,
  edited_at   INTEGER NOT NULL,
  content     TEXT NOT NULL,
  PRIMARY KEY (message_seq, edited_at)
);

CREATE TABLE reactions (
  message_seq INTEGER NOT NULL REFERENCES messages(seq) ON DELETE CASCADE,
  sender_id   TEXT NOT NULL,
  emoji       TEXT NOT NULL,
  ts          INTEGER NOT NULL,
  PRIMARY KEY (message_seq, sender_id, emoji)
);

CREATE TABLE receipts (
  message_seq INTEGER NOT NULL REFERENCES messages(seq) ON DELETE CASCADE,
  user_id     TEXT NOT NULL,
  kind        TEXT NOT NULL,
  ts          INTEGER NOT NULL,
  PRIMARY KEY (message_seq, user_id, kind)
);

CREATE TABLE media (
  id            TEXT PRIMARY KEY,
  account_id    TEXT NOT NULL,
  message_seq   INTEGER REFERENCES messages(seq) ON DELETE SET NULL,
  sha256        TEXT,
  mime          TEXT NOT NULL,
  size          INTEGER,
  file_name     TEXT,
  width         INTEGER,
  height        INTEGER,
  duration_ms   INTEGER,
  thumbnail_id  TEXT,
  state         TEXT NOT NULL,
  remote_ref    TEXT,
  created_at    INTEGER NOT NULL,
  expires_at    INTEGER,
  last_access   INTEGER
);
CREATE INDEX media_sha ON media(sha256);
CREATE INDEX media_message ON media(message_seq);
CREATE INDEX media_gc ON media(state, last_access);

CREATE TABLE raw_payloads (
  message_seq INTEGER PRIMARY KEY REFERENCES messages(seq) ON DELETE CASCADE,
  raw         TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);

CREATE TABLE events (
  id          INTEGER PRIMARY KEY,
  account_id  TEXT,
  type        TEXT NOT NULL,
  ts_ms       INTEGER NOT NULL,
  data        TEXT NOT NULL
);
CREATE INDEX events_type ON events(type, id);

CREATE TABLE webhooks (
  id          TEXT PRIMARY KEY,
  url         TEXT NOT NULL,
  secret      TEXT NOT NULL,
  account_id  TEXT,
  types       TEXT,
  cursor      INTEGER NOT NULL DEFAULT 0,
  failures    INTEGER NOT NULL DEFAULT 0,
  next_try    INTEGER,
  paused_at   INTEGER,
  created_at  INTEGER NOT NULL
);
`
