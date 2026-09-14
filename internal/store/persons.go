package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"gimhq/chat-bridge/internal/model"
)

// LinkRef names a contact: a user on one account.
type LinkRef struct {
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
}

// PhoneMatch is another account's contact with the same phone, and the person it already belongs to.
type PhoneMatch struct {
	LinkRef
	PersonID string
}

// PersonFilter narrows ListPersons.
type PersonFilter struct {
	Tag string
	Q   string
}

const personCols = `rowid, id, COALESCE(name,''), tags, COALESCE(notes,''), created_at, updated_at`

// personDirectChats joins pl (a person_links row) to the direct chats with that contact.
const personDirectChats = `ch.account_id = pl.account_id AND ch.kind = 'direct' AND (ch.id = pl.user_id OR ch.id IN
	(SELECT mm.chat_id FROM chat_members mm WHERE mm.account_id = pl.account_id AND mm.user_id = pl.user_id AND mm.left_at IS NULL))`

func cleanTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// CreatePerson stores a person without links.
func (s *Store) CreatePerson(ctx context.Context, p model.Person) (model.Person, error) {
	if p.ID == "" {
		p.ID = "per_" + uuid.NewString()
	}
	now := unix(time.Now())
	_, err := s.q.ExecContext(ctx, `INSERT INTO persons (id, name, tags, notes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, nullStr(p.Name), mustJSON(cleanTags(p.Tags)), nullStr(p.Notes), now, now)
	if err != nil {
		return model.Person{}, fmt.Errorf("create person: %w", err)
	}
	return s.GetPerson(ctx, p.ID)
}

// GetPerson returns a person with links and derived channels.
func (s *Store) GetPerson(ctx context.Context, id string) (model.Person, error) {
	p, _, err := scanPerson(s.q.QueryRowContext(ctx, `SELECT `+personCols+` FROM persons WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Person{}, ErrNotFound
	}
	if err != nil {
		return model.Person{}, err
	}
	return p, s.fillPerson(ctx, &p)
}

func scanPerson(row scanner) (model.Person, int64, error) {
	var p model.Person
	var rowid, created, updated int64
	var tags string
	if err := row.Scan(&rowid, &p.ID, &p.Name, &tags, &p.Notes, &created, &updated); err != nil {
		return p, 0, err
	}
	_ = json.Unmarshal([]byte(tags), &p.Tags)
	if p.Tags == nil {
		p.Tags = []string{}
	}
	p.CreatedAt, p.UpdatedAt = fromUnix(created), fromUnix(updated)
	return p, rowid, nil
}

func (s *Store) fillPerson(ctx context.Context, p *model.Person) error {
	rows, err := s.q.QueryContext(ctx, `SELECT pl.account_id, pl.user_id, pl.source, pl.linked_at, COALESCE(a.platform,''),
		COALESCE(c.names,'{}'), COALESCE(c.handle,''), COALESCE(c.phone,'')
		FROM person_links pl LEFT JOIN accounts a ON a.id = pl.account_id
		LEFT JOIN contacts c ON c.account_id = pl.account_id AND c.id = pl.user_id
		WHERE pl.person_id = ? ORDER BY pl.linked_at, pl.account_id, pl.user_id`, p.ID)
	if err != nil {
		return fmt.Errorf("person links: %w", err)
	}
	p.Links = []model.PersonLink{}
	names := map[LinkRef]string{}
	for rows.Next() {
		var l model.PersonLink
		var linked int64
		var namesJSON string
		if err := rows.Scan(&l.AccountID, &l.UserID, &l.Source, &linked, &l.Platform, &namesJSON, &l.Handle, &l.Phone); err != nil {
			_ = rows.Close()
			return err
		}
		ct := model.Contact{ID: l.UserID, Handle: l.Handle}
		_ = json.Unmarshal([]byte(namesJSON), &ct.Names)
		l.Name = ResolveName(ct, "")
		l.LinkedAt = fromUnix(linked)
		names[LinkRef{l.AccountID, l.UserID}] = l.Name
		p.Links = append(p.Links, l)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	crows, err := s.q.QueryContext(ctx, `SELECT pl.account_id, pl.user_id, ch.id, COALESCE(ch.name,''), ch.last_message_ts
		FROM person_links pl JOIN chats ch ON `+personDirectChats+`
		WHERE pl.person_id = ? ORDER BY COALESCE(ch.last_message_ts,0) DESC, ch.account_id, ch.id`, p.ID)
	if err != nil {
		return fmt.Errorf("person channels: %w", err)
	}
	defer func() { _ = crows.Close() }()
	p.Channels = []model.PersonChannel{}
	for crows.Next() {
		var ch model.PersonChannel
		var last sql.NullInt64
		if err := crows.Scan(&ch.AccountID, &ch.UserID, &ch.ChatID, &ch.Name, &last); err != nil {
			return err
		}
		if ch.Name == "" {
			ch.Name = names[LinkRef{ch.AccountID, ch.UserID}]
		}
		ch.LastMessageAt = timePtr(last)
		p.Channels = append(p.Channels, ch)
	}
	return crows.Err()
}

// ListPersons pages persons newest first; Q matches the name, notes, and linked contacts' names,
// handles, phones and ids.
func (s *Store) ListPersons(ctx context.Context, f PersonFilter, cursor string, limit int) ([]model.Person, string, error) {
	where := `1 = 1`
	var args []any
	if f.Tag != "" {
		where += ` AND EXISTS (SELECT 1 FROM json_each(p.tags) WHERE value = ?)`
		args = append(args, f.Tag)
	}
	if q := strings.TrimSpace(strings.ToLower(f.Q)); q != "" {
		like := "%" + q + "%"
		where += ` AND (LOWER(COALESCE(p.name,'')) LIKE ? OR LOWER(COALESCE(p.notes,'')) LIKE ? OR EXISTS (SELECT 1 FROM person_links pl
			JOIN contacts c ON c.account_id = pl.account_id AND c.id = pl.user_id WHERE pl.person_id = p.id AND (LOWER(c.names) LIKE ?
			OR LOWER(COALESCE(c.handle,'')) LIKE ? OR LOWER(COALESCE(c.phone,'')) LIKE ? OR LOWER(c.id) LIKE ?)))`
		args = append(args, like, like, like, like, like, like)
	}
	if cursor != "" {
		ts, rowid, err := DecodeMessageCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		where += ` AND (p.created_at < ? OR (p.created_at = ? AND p.rowid < ?))`
		args = append(args, ts, ts, rowid)
	}
	args = append(args, limit+1)
	rows, err := s.q.QueryContext(ctx, `SELECT `+strings.ReplaceAll(personCols, "rowid, id", "p.rowid, p.id")+` FROM persons p WHERE `+where+
		` ORDER BY p.created_at DESC, p.rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list persons: %w", err)
	}
	var out []model.Person
	var rowids []int64
	for rows.Next() {
		p, rowid, err := scanPerson(rows)
		if err != nil {
			_ = rows.Close()
			return nil, "", err
		}
		out, rowids = append(out, p), append(rowids, rowid)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = EncodeMessageCursor(out[limit-1].CreatedAt.Unix(), rowids[limit-1])
	}
	for i := range out {
		if err := s.fillPerson(ctx, &out[i]); err != nil {
			return nil, "", err
		}
	}
	if out == nil {
		out = []model.Person{}
	}
	return out, next, nil
}

// UpdatePerson changes the local label; nil leaves a field alone.
func (s *Store) UpdatePerson(ctx context.Context, id string, name, notes *string, tags []string) (model.Person, error) {
	p, err := s.GetPerson(ctx, id)
	if err != nil {
		return model.Person{}, err
	}
	if name != nil {
		p.Name = *name
	}
	if notes != nil {
		p.Notes = *notes
	}
	if tags != nil {
		p.Tags = cleanTags(tags)
	}
	if _, err := s.q.ExecContext(ctx, `UPDATE persons SET name = ?, notes = ?, tags = ?, updated_at = ? WHERE id = ?`,
		nullStr(p.Name), nullStr(p.Notes), mustJSON(p.Tags), unix(time.Now()), id); err != nil {
		return model.Person{}, fmt.Errorf("update person: %w", err)
	}
	return s.GetPerson(ctx, id)
}

func (s *Store) touchPerson(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE persons SET updated_at = ? WHERE id = ?`, unix(time.Now()), id)
	return err
}

// PersonLinks returns the contacts of a person.
func (s *Store) PersonLinks(ctx context.Context, id string) ([]LinkRef, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT account_id, user_id FROM person_links WHERE person_id = ? ORDER BY linked_at, account_id, user_id`, id)
	if err != nil {
		return nil, fmt.Errorf("person links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LinkRef
	for rows.Next() {
		var l LinkRef
		if err := rows.Scan(&l.AccountID, &l.UserID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeletePerson removes the person; its contacts become unlinked and are returned.
func (s *Store) DeletePerson(ctx context.Context, id string) ([]LinkRef, error) {
	links, err := s.PersonLinks(ctx, id)
	if err != nil {
		return nil, err
	}
	res, err := s.q.ExecContext(ctx, `DELETE FROM persons WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("delete person: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return links, nil
}

// PersonOf returns the person a contact belongs to, or "".
func (s *Store) PersonOf(ctx context.Context, l LinkRef) (string, error) {
	var id string
	err := s.q.QueryRowContext(ctx, `SELECT person_id FROM person_links WHERE account_id = ? AND user_id = ?`, l.AccountID, l.UserID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// LinkContact adds a contact to a person. A contact linked to another person is a conflict;
// linking it to the same person again is a no-op.
func (s *Store) LinkContact(ctx context.Context, personID string, l LinkRef, source string) error {
	var one int
	if err := s.q.QueryRowContext(ctx, `SELECT 1 FROM persons WHERE id = ?`, personID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := s.GetContact(ctx, l.AccountID, l.UserID); err != nil {
		return err
	}
	cur, err := s.PersonOf(ctx, l)
	if err != nil {
		return err
	}
	if cur == personID {
		return nil
	}
	if cur != "" {
		return fmt.Errorf("%w: contact %s on %s belongs to %s", ErrConflict, l.UserID, l.AccountID, cur)
	}
	if _, err := s.q.ExecContext(ctx, `INSERT INTO person_links (account_id, user_id, person_id, source, linked_at) VALUES (?, ?, ?, ?, ?)`,
		l.AccountID, l.UserID, personID, source, unix(time.Now())); err != nil {
		return fmt.Errorf("link contact: %w", err)
	}
	return s.touchPerson(ctx, personID)
}

// UnlinkContact removes a contact from a person and records the split against every remaining
// contact, so phone auto-linking does not join them again.
func (s *Store) UnlinkContact(ctx context.Context, personID string, l LinkRef) error {
	cur, err := s.PersonOf(ctx, l)
	if err != nil {
		return err
	}
	if cur != personID {
		return ErrNotFound
	}
	links, err := s.PersonLinks(ctx, personID)
	if err != nil {
		return err
	}
	if _, err := s.q.ExecContext(ctx, `DELETE FROM person_links WHERE account_id = ? AND user_id = ?`, l.AccountID, l.UserID); err != nil {
		return fmt.Errorf("unlink contact: %w", err)
	}
	for _, o := range links {
		if o == l {
			continue
		}
		for _, pair := range [][2]LinkRef{{l, o}, {o, l}} {
			if _, err := s.q.ExecContext(ctx, `INSERT OR IGNORE INTO person_unlinks (account_id, user_id, other_account_id, other_user_id) VALUES (?, ?, ?, ?)`,
				pair[0].AccountID, pair[0].UserID, pair[1].AccountID, pair[1].UserID); err != nil {
				return fmt.Errorf("record unlink: %w", err)
			}
		}
	}
	return s.touchPerson(ctx, personID)
}

// PhoneMatches finds contacts on other accounts with the same normalised phone, skipping self
// contacts and pairs the owner split.
func (s *Store) PhoneMatches(ctx context.Context, l LinkRef) ([]PhoneMatch, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT c.account_id, c.id, COALESCE(pl.person_id,'')
		FROM contacts me JOIN contacts c ON c.phone_norm = me.phone_norm AND c.account_id <> me.account_id AND c.is_self = 0
		LEFT JOIN person_links pl ON pl.account_id = c.account_id AND pl.user_id = c.id
		WHERE me.account_id = ? AND me.id = ? AND me.phone_norm IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM person_unlinks u WHERE u.account_id = me.account_id AND u.user_id = me.id
			AND u.other_account_id = c.account_id AND u.other_user_id = c.id)
		ORDER BY c.account_id, c.id`, l.AccountID, l.UserID)
	if err != nil {
		return nil, fmt.Errorf("phone matches: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PhoneMatch
	for rows.Next() {
		var m PhoneMatch
		if err := rows.Scan(&m.AccountID, &m.UserID, &m.PersonID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SuggestPersons groups unlinked, non-self contacts that share a phone across at least two accounts.
func (s *Store) SuggestPersons(ctx context.Context) ([]model.PersonSuggestion, error) {
	const unlinked = `c.phone_norm IS NOT NULL AND c.is_self = 0 AND NOT EXISTS (SELECT 1 FROM person_links pl WHERE pl.account_id = c.account_id AND pl.user_id = c.id)`
	rows, err := s.q.QueryContext(ctx, `SELECT c.phone_norm, c.account_id, c.id, c.names, COALESCE(c.handle,''), COALESCE(c.phone,''), COALESCE(a.platform,'')
		FROM contacts c LEFT JOIN accounts a ON a.id = c.account_id
		WHERE `+unlinked+` AND c.phone_norm IN (SELECT c.phone_norm FROM contacts c WHERE `+unlinked+`
			GROUP BY c.phone_norm HAVING COUNT(DISTINCT c.account_id) >= 2)
		ORDER BY c.phone_norm, c.account_id, c.id`)
	if err != nil {
		return nil, fmt.Errorf("suggest persons: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []model.PersonSuggestion{}
	last := ""
	for rows.Next() {
		var norm, namesJSON string
		var l model.PersonLink
		if err := rows.Scan(&norm, &l.AccountID, &l.UserID, &namesJSON, &l.Handle, &l.Phone, &l.Platform); err != nil {
			return nil, err
		}
		ct := model.Contact{ID: l.UserID, Handle: l.Handle}
		_ = json.Unmarshal([]byte(namesJSON), &ct.Names)
		l.Name = ResolveName(ct, "")
		if norm != last {
			out = append(out, model.PersonSuggestion{Reason: "phone"})
			last = norm
		}
		out[len(out)-1].Contacts = append(out[len(out)-1].Contacts, l)
	}
	return out, rows.Err()
}

// MergePersons moves the links and tags of sources into target and deletes the sources. Notes of
// the first source fill an empty target.
func (s *Store) MergePersons(ctx context.Context, target string, sources []string) (model.Person, error) {
	t, err := s.GetPerson(ctx, target)
	if err != nil {
		return model.Person{}, err
	}
	tags, notes := t.Tags, t.Notes
	for _, id := range sources {
		src, err := s.GetPerson(ctx, id)
		if err != nil {
			return model.Person{}, err
		}
		tags = append(tags, src.Tags...)
		if notes == "" {
			notes = src.Notes
		}
		if _, err := s.q.ExecContext(ctx, `UPDATE person_links SET person_id = ? WHERE person_id = ?`, target, id); err != nil {
			return model.Person{}, fmt.Errorf("move links: %w", err)
		}
		if _, err := s.q.ExecContext(ctx, `DELETE FROM persons WHERE id = ?`, id); err != nil {
			return model.Person{}, fmt.Errorf("delete merged person: %w", err)
		}
	}
	return s.UpdatePerson(ctx, target, nil, &notes, tags)
}

// PersonChats returns the direct chats with a person's contacts, most recent first.
func (s *Store) PersonChats(ctx context.Context, id string) ([]model.Chat, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+chatCols+` FROM chats WHERE chats.kind = 'direct' AND EXISTS (SELECT 1 FROM person_links pl
		JOIN chats ch ON `+personDirectChats+` WHERE pl.person_id = ? AND ch.account_id = chats.account_id AND ch.id = chats.id)
		ORDER BY COALESCE(chats.last_message_ts,0) DESC, chats.account_id, chats.id`, id)
	if err != nil {
		return nil, fmt.Errorf("person chats: %w", err)
	}
	out := []model.Chat{}
	var seqs []int64
	for rows.Next() {
		c, seq, err := scanChat(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out, seqs = append(out, c), append(seqs, seq)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if seqs[i] != 0 {
			if m, err := s.GetMessageBySeq(ctx, seqs[i]); err == nil {
				out[i].LastMessage = &m.Message
			}
		}
	}
	return out, s.NameDirectChats(ctx, out)
}

// PersonMessages merges a person's messages across accounts, newest first. Scope "direct" is the
// direct chats with the linked contacts; "all" adds their messages in groups.
func (s *Store) PersonMessages(ctx context.Context, id, scope, cursor string, limit int) ([]Stored, string, error) {
	where := `EXISTS (SELECT 1 FROM person_links pl JOIN chats ch ON ` + personDirectChats + `
		WHERE pl.person_id = ? AND ch.account_id = m.account_id AND ch.id = m.chat_id)`
	args := []any{id}
	if scope == "all" {
		where = `(` + where + ` OR EXISTS (SELECT 1 FROM person_links pl WHERE pl.person_id = ? AND pl.account_id = m.account_id AND pl.user_id = m.sender_id))`
		args = append(args, id)
	}
	if cursor != "" {
		ts, seq, err := DecodeMessageCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		where += ` AND (m.ts < ? OR (m.ts = ? AND m.seq < ?))`
		args = append(args, ts, ts, seq)
	}
	args = append(args, limit+1)
	rows, err := s.q.QueryContext(ctx, `SELECT `+messageColsM+` FROM messages m WHERE `+where+` ORDER BY m.ts DESC, m.seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("person messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Stored, 0, limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = EncodeMessageCursor(out[limit-1].Timestamp.Unix(), out[limit-1].seq)
	}
	return out, next, s.decorate(ctx, out)
}

// decoratePersons sets sender.person_id from the links of each message's account.
func (s *Store) decoratePersons(ctx context.Context, msgs []Stored) error {
	byAccount := map[string][]any{}
	for i := range msgs {
		byAccount[msgs[i].AccountID] = append(byAccount[msgs[i].AccountID], msgs[i].Sender.ID)
	}
	for account, ids := range byAccount {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		rows, err := s.q.QueryContext(ctx, `SELECT user_id, person_id FROM person_links WHERE account_id = ? AND user_id IN (`+ph+`)`, append([]any{account}, ids...)...)
		if err != nil {
			return fmt.Errorf("sender persons: %w", err)
		}
		persons := map[string]string{}
		for rows.Next() {
			var user, person string
			if err := rows.Scan(&user, &person); err != nil {
				_ = rows.Close()
				return err
			}
			persons[user] = person
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range msgs {
			if msgs[i].AccountID == account {
				msgs[i].Sender.PersonID = persons[msgs[i].Sender.ID]
			}
		}
	}
	return nil
}
