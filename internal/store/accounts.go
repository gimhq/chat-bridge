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

// AccountRow is the persisted account without derived fields.
type AccountRow struct {
	ID          string
	Platform    string
	Adapter     string // adapter instance serving this account ("" = not yet assigned)
	Status      string
	SelfID      string
	Config      json.RawMessage
	Device      json.RawMessage
	Login       *model.LoginRecord
	Error       *model.Error
	CreatedAt   time.Time
	ConnectedAt *time.Time
}

const accountCols = `id, platform, adapter, status, COALESCE(self_id,''), config, device, login_flow, login_ident, login_at, error, created_at, connected_at`

// CreateAccount inserts a new account in status unpaired.
func (s *Store) CreateAccount(ctx context.Context, id, platform, instance string, cfg json.RawMessage) error {
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	_, err := s.q.ExecContext(ctx, `INSERT INTO accounts (id, platform, adapter, status, config, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, platform, instance, model.StatusUnpaired, string(cfg), unix(time.Now()))
	if err != nil {
		if isUnique(err) {
			return ErrConflict
		}
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

// GetAccount returns one account row.
func (s *Store) GetAccount(ctx context.Context, id string) (AccountRow, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = ?`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountRow{}, ErrNotFound
	}
	return a, err
}

// ListAccounts returns every account ordered by creation.
func (s *Store) ListAccounts(ctx context.Context) ([]AccountRow, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AccountRow
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAccountStatus updates status, error, device, self and connected_at.
func (s *Store) SetAccountStatus(ctx context.Context, id, status string, selfID string, device json.RawMessage, e *model.Error) error {
	var connected any
	if status == model.StatusConnected {
		connected = unix(time.Now())
	}
	dev := "{}"
	if len(device) > 0 {
		dev = string(device)
	}
	var errJSON any
	if e != nil {
		errJSON = mustJSON(e)
	}
	_, err := s.q.ExecContext(ctx, `UPDATE accounts SET status = ?, error = ?, device = ?,
		self_id = COALESCE(NULLIF(?, ''), self_id),
		connected_at = COALESCE(?, connected_at) WHERE id = ?`,
		status, errJSON, dev, selfID, connected, id)
	if err != nil {
		return fmt.Errorf("set account status: %w", err)
	}
	return nil
}

// SetAccountAdapter binds the account to an adapter instance.
func (s *Store) SetAccountAdapter(ctx context.Context, id, instance string) error {
	if _, err := s.q.ExecContext(ctx, `UPDATE accounts SET adapter = ? WHERE id = ?`, instance, id); err != nil {
		return fmt.Errorf("set account adapter: %w", err)
	}
	return nil
}

// SetAccountLogin records how the session was established.
func (s *Store) SetAccountLogin(ctx context.Context, id string, rec model.LoginRecord) error {
	_, err := s.q.ExecContext(ctx, `UPDATE accounts SET login_flow = ?, login_ident = ?, login_at = ? WHERE id = ?`,
		rec.Flow, nullStr(rec.Identifier), unix(rec.At), id)
	if err != nil {
		return fmt.Errorf("set account login: %w", err)
	}
	return nil
}

// ClearAccountSession resets an account to unpaired.
func (s *Store) ClearAccountSession(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE accounts SET status = ?, self_id = NULL, device = '{}', error = NULL,
		login_flow = NULL, login_ident = NULL, login_at = NULL, connected_at = NULL WHERE id = ?`, model.StatusUnpaired, id)
	if err != nil {
		return fmt.Errorf("clear account session: %w", err)
	}
	return nil
}

// SetAccountConfig replaces the config blob.
func (s *Store) SetAccountConfig(ctx context.Context, id string, cfg json.RawMessage) error {
	_, err := s.q.ExecContext(ctx, `UPDATE accounts SET config = ? WHERE id = ?`, string(cfg), id)
	if err != nil {
		return fmt.Errorf("set account config: %w", err)
	}
	return nil
}

// DeleteAccount removes the account and everything scoped to it.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	return s.Tx(ctx, func(tx *Store) error {
		for _, stmt := range []string{
			`DELETE FROM media WHERE account_id = ?`,
			`DELETE FROM messages WHERE account_id = ?`,
			`DELETE FROM chats WHERE account_id = ?`,
			`DELETE FROM contacts WHERE account_id = ?`,
			`DELETE FROM events WHERE account_id = ?`,
			`DELETE FROM accounts WHERE id = ?`,
		} {
			if _, err := tx.q.ExecContext(ctx, stmt, id); err != nil {
				return fmt.Errorf("delete account: %w", err)
			}
		}
		return nil
	})
}

// AccountStats computes the derived counters.
func (s *Store) AccountStats(ctx context.Context, id string) (model.AccountStats, error) {
	var st model.AccountStats
	var lastIn sql.NullInt64
	var lastEv sql.NullInt64
	err := s.q.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM chats WHERE account_id = ?),
		(SELECT COUNT(*) FROM messages WHERE account_id = ?),
		(SELECT MAX(received_at) FROM messages WHERE account_id = ? AND from_me = 0),
		(SELECT MAX(id) FROM events WHERE account_id = ?),
		(SELECT COUNT(*) FROM requests WHERE account_id = ? AND state = 'pending')`, id, id, id, id, id).
		Scan(&st.Chats, &st.Messages, &lastIn, &lastEv, &st.RequestsPending)
	if err != nil {
		return st, fmt.Errorf("account stats: %w", err)
	}
	st.LastInboundAt = timePtr(lastIn)
	if lastEv.Valid {
		st.LastEventID = FormatEventID(lastEv.Int64)
	}
	return st, nil
}

func scanAccount(row scanner) (AccountRow, error) {
	var a AccountRow
	var cfg, dev string
	var flow, ident, errJSON sql.NullString
	var loginAt, created sql.NullInt64
	var connected sql.NullInt64
	if err := row.Scan(&a.ID, &a.Platform, &a.Adapter, &a.Status, &a.SelfID, &cfg, &dev, &flow, &ident, &loginAt, &errJSON, &created, &connected); err != nil {
		return a, err
	}
	a.Config = json.RawMessage(cfg)
	a.Device = json.RawMessage(dev)
	if flow.Valid {
		a.Login = &model.LoginRecord{Flow: flow.String, Identifier: ident.String, At: fromUnix(loginAt.Int64)}
	}
	if errJSON.Valid && errJSON.String != "null" {
		var e model.Error
		if json.Unmarshal([]byte(errJSON.String), &e) == nil && e.Code != "" {
			a.Error = &e
		}
	}
	a.CreatedAt = fromUnix(created.Int64)
	a.ConnectedAt = timePtr(connected)
	return a, nil
}

type scanner interface {
	Scan(dest ...any) error
}
