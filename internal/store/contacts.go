package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gimhq/chat-bridge/internal/model"
)

const contactCols = `account_id, id, COALESCE(handle,''), COALESCE(phone,''), COALESCE(email,''), names,
	COALESCE(avatar_media,''), is_self, is_contact, blocked, COALESCE(bio,''), raw, updated_at,
	(SELECT pl.person_id FROM person_links pl WHERE pl.account_id = contacts.account_id AND pl.user_id = contacts.id)`

// normPhone keeps the digits of a phone number; shorter than 7 digits is not a phone to match on.
func normPhone(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() < 7 {
		return ""
	}
	return b.String()
}

// UpsertContact merges c into the stored row. Empty fields in c never erase stored values.
// It reports whether anything visible changed.
func (s *Store) UpsertContact(ctx context.Context, accountID string, c model.Contact) (bool, error) {
	cur, err := s.GetContact(ctx, accountID, c.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	exists := err == nil
	merged := mergeContact(cur, c)
	if exists && contactEqual(cur, merged) {
		return false, nil
	}
	return true, s.writeContact(ctx, accountID, merged)
}

func (s *Store) writeContact(ctx context.Context, accountID string, c model.Contact) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO contacts
		(account_id, id, handle, phone, email, names, avatar_media, is_self, is_contact, blocked, bio, raw, updated_at, phone_norm)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, id) DO UPDATE SET handle=excluded.handle, phone=excluded.phone, phone_norm=excluded.phone_norm, email=excluded.email,
		names=excluded.names, avatar_media=excluded.avatar_media, is_self=excluded.is_self,
		is_contact=excluded.is_contact, blocked=excluded.blocked, bio=excluded.bio, raw=excluded.raw, updated_at=excluded.updated_at`,
		accountID, c.ID, nullStr(c.Handle), nullStr(c.Phone), nullStr(c.Email), mustJSON(c.Names),
		nullStr(avatarID(c.Avatar)), boolInt(c.IsSelf), boolInt(c.IsContact),
		boolInt(c.Blocked), nullStr(c.Bio), rawOrNull(c.Raw), unix(time.Now()), nullStr(normPhone(c.Phone)))
	if err != nil {
		return fmt.Errorf("upsert contact: %w", err)
	}
	return nil
}

// SetLocalAlias stores an owner alias in the bridge (alias_source = local). Empty clears it.
func (s *Store) SetLocalAlias(ctx context.Context, accountID, userID, alias string) (model.Contact, error) {
	c, err := s.GetContact(ctx, accountID, userID)
	if err != nil {
		return model.Contact{}, err
	}
	c.Names.Alias, c.Names.AliasSource = alias, "local"
	if alias == "" {
		c.Names.AliasSource = ""
	}
	if err := s.writeContact(ctx, accountID, c); err != nil {
		return model.Contact{}, err
	}
	return s.GetContact(ctx, accountID, userID)
}

// SetBlocked stores the block state the platform accepted.
func (s *Store) SetBlocked(ctx context.Context, accountID, userID string, blocked bool) (model.Contact, error) {
	c, err := s.GetContact(ctx, accountID, userID)
	if err != nil {
		return model.Contact{}, err
	}
	c.Blocked = blocked
	if err := s.writeContact(ctx, accountID, c); err != nil {
		return model.Contact{}, err
	}
	return s.GetContact(ctx, accountID, userID)
}

// GetContact returns one contact.
func (s *Store) GetContact(ctx context.Context, accountID, id string) (model.Contact, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+contactCols+` FROM contacts WHERE account_id = ? AND id = ?`, accountID, id)
	c, err := scanContact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Contact{}, ErrNotFound
	}
	return c, err
}

// ListContacts pages the address book ordered by id.
func (s *Store) ListContacts(ctx context.Context, accountID, q, cursor string, limit int) ([]model.Contact, string, error) {
	args := []any{accountID}
	where := `account_id = ?`
	if q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where += ` AND (LOWER(names) LIKE ? OR LOWER(COALESCE(handle,'')) LIKE ? OR LOWER(id) LIKE ?)`
		args = append(args, like, like, like)
	}
	if cursor != "" {
		where += ` AND id > ?`
		args = append(args, cursor)
	}
	args = append(args, limit+1)
	rows, err := s.q.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts WHERE `+where+` ORDER BY id LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list contacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]model.Contact, 0, limit)
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, c)
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	return out, next, rows.Err()
}

// ContactNames returns id → resolved name for a set of users.
func (s *Store) ContactNames(ctx context.Context, accountID string, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, accountID)
	for _, id := range ids {
		args = append(args, id)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.q.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts WHERE account_id = ? AND id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("contact names: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out[c.ID] = c.Name
	}
	return out, rows.Err()
}

func scanContact(row scanner) (model.Contact, error) {
	var c model.Contact
	var account, names, avatar string
	var isSelf, isContact, blocked int
	var raw, person sql.NullString
	var updated int64
	if err := row.Scan(&account, &c.ID, &c.Handle, &c.Phone, &c.Email, &names, &avatar,
		&isSelf, &isContact, &blocked, &c.Bio, &raw, &updated, &person); err != nil {
		return c, err
	}
	c.PersonID = person.String
	_ = json.Unmarshal([]byte(names), &c.Names)
	if avatar != "" {
		c.Avatar = &model.AvatarRef{MediaID: avatar}
	}
	c.IsSelf, c.IsContact, c.Blocked = isSelf == 1, isContact == 1, blocked == 1
	if raw.Valid {
		c.Raw = json.RawMessage(raw.String)
	}
	c.UpdatedAt = fromUnix(updated)
	c.Name = ResolveName(c, "")
	return c, nil
}

// ResolveName applies the docs §3.7 order: alias → chat_name → profile → username → handle → id.
func ResolveName(c model.Contact, chatName string) string {
	for _, v := range []string{c.Names.Alias, chatName, c.Names.Profile, c.Names.Username, c.Handle, c.ID} {
		if v != "" {
			return v
		}
	}
	return c.ID
}

// mergeContact overlays non-empty fields of in on cur. A local alias is never overwritten by
// a platform-sourced one.
func mergeContact(cur, in model.Contact) model.Contact {
	out := cur
	out.ID = in.ID
	pick := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	pick(&out.Handle, in.Handle)
	pick(&out.Phone, in.Phone)
	pick(&out.Email, in.Email)
	pick(&out.Bio, in.Bio)
	if cur.Names.AliasSource != "local" {
		pick(&out.Names.Alias, in.Names.Alias)
	}
	pick(&out.Names.Profile, in.Names.Profile)
	pick(&out.Names.Username, in.Names.Username)
	pick(&out.Names.First, in.Names.First)
	pick(&out.Names.Last, in.Names.Last)
	if in.Avatar != nil {
		out.Avatar = in.Avatar
	}
	out.IsSelf = out.IsSelf || in.IsSelf
	out.IsContact = out.IsContact || in.IsContact
	out.Blocked = in.Blocked
	if len(in.Raw) > 0 {
		out.Raw = in.Raw
	}
	return out
}

func contactEqual(a, b model.Contact) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	a.Name, b.Name = "", ""
	return mustJSON(a) == mustJSON(b)
}

func avatarID(a *model.AvatarRef) string {
	if a == nil {
		return ""
	}
	return a.MediaID
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "constraint failed")
}
