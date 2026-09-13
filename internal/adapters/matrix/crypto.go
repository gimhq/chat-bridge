package matrix

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver used for crypto.db

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

// cryptoFile holds the olm account, megolm sessions, device keys, and room state for one account.
// It is opened with modernc (no cgo); the mautrix helper only sees a *dbutil.Database, so its own
// cgo sqlite driver is never used. Requires the `goolm` build tag.
const cryptoFile = "crypto.db"

// openCrypto attaches an E2EE helper to the client. The pickle key is created on first use and
// persisted in the session file.
func (acc *account) openCrypto(s *session) error {
	if len(s.PickleKey) == 0 {
		s.PickleKey = make([]byte, 32)
		if _, err := rand.Read(s.PickleKey); err != nil {
			return err
		}
		if err := acc.saveSession(*s); err != nil {
			return err
		}
	}
	path := filepath.Join(acc.dir, cryptoFile)
	raw, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("open crypto db: %w", err)
	}
	if err := raw.Ping(); err != nil {
		_ = raw.Close()
		return fmt.Errorf("open crypto db: %w", err)
	}
	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		_ = raw.Close()
		return err
	}
	helper, err := cryptohelper.NewCryptoHelper(acc.cli, s.PickleKey, db)
	if err != nil {
		_ = raw.Close()
		return err
	}
	helper.DecryptErrorCallback = acc.onDecryptError
	acc.mu.Lock()
	acc.crypto, acc.cryptoDB = helper, db
	acc.cli.Crypto = helper
	acc.mu.Unlock()
	return nil
}

// initCrypto runs the helper's Init (store upgrades, key upload) before syncing starts.
func (acc *account) initCrypto(ctx context.Context) error {
	acc.mu.Lock()
	helper := acc.crypto
	acc.mu.Unlock()
	if helper == nil {
		return nil
	}
	return helper.Init(ctx)
}

// closeCrypto releases the helper and database without deleting anything.
func (acc *account) closeCrypto() {
	acc.mu.Lock()
	helper, db := acc.crypto, acc.cryptoDB
	acc.crypto, acc.cryptoDB = nil, nil
	if acc.cli != nil {
		acc.cli.Crypto = nil
	}
	acc.mu.Unlock()
	if helper != nil {
		_ = helper.Close()
	}
	if db != nil {
		_ = db.Close()
	}
}

// removeCrypto closes and deletes the crypto database (logout).
func (acc *account) removeCrypto() {
	acc.closeCrypto()
	path := filepath.Join(acc.dir, cryptoFile)
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}
}

// onDecryptError surfaces an undecryptable event as an unsupported message so the timeline
// shows that something arrived, plus a platform event with the reason.
func (acc *account) onDecryptError(evt *event.Event, err error) {
	m := model.Message{
		ID: evt.ID.String(), ChatID: evt.RoomID.String(), Sender: model.Sender{ID: evt.Sender.String()}, FromMe: evt.Sender == acc.selfID(),
		Timestamp: msTime(evt.Timestamp), Content: model.Content{Type: model.ContentUnsupported, Unsupported: &model.Unsupported{PlatformType: "m.room.encrypted"}},
	}
	acc.rep.Events(
		adapter.Event{Kind: adapter.EvMessage, Message: &m, Chat: acc.chatHint(evt.RoomID), Sender: userContact(evt.Sender, "")},
		adapter.Event{Kind: adapter.EvPlatform, PlatformType: "decrypt_failed", ChatID: evt.RoomID.String(), UserID: evt.Sender.String(), MessageID: evt.ID.String(),
			Raw: rawJSON(map[string]string{"error": err.Error()})},
	)
}
