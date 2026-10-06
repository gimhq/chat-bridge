package core

import (
	"context"
	"errors"
	"net/http"

	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// PersonInput is the body of POST /persons.
type PersonInput struct {
	Name  string          `json:"name"`
	Tags  []string        `json:"tags"`
	Notes string          `json:"notes"`
	Links []store.LinkRef `json:"links"`
}

// PersonPatch is the body of PATCH /persons/{p}; absent fields stay unchanged.
type PersonPatch struct {
	Name  *string  `json:"name"`
	Tags  []string `json:"tags"`
	Notes *string  `json:"notes"`
}

// ListPersons pages persons (api.md §4.7).
func (c *Core) ListPersons(ctx context.Context, f store.PersonFilter, cursor string, limit int) ([]model.Person, string, error) {
	sc, err := c.scopeOf(ctx)
	if err != nil {
		return nil, "", err
	}
	if sc != nil {
		f.Only = store.Only{Set: true}
		for id := range sc.persons {
			f.Only.IDs = append(f.Only.IDs, id)
		}
	}
	out, next, err := c.st.ListPersons(ctx, f, cursor, limit)
	if err != nil && cursor != "" && err.Error() == "bad cursor" {
		return nil, "", errInvalid("bad cursor")
	}
	return out, next, err
}

// GetPerson returns one person with links and channels.
func (c *Core) GetPerson(ctx context.Context, id string) (model.Person, error) {
	if err := c.allowPerson(ctx, id); err != nil {
		return model.Person{}, err
	}
	p, err := c.st.GetPerson(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Person{}, errNotFound("person")
	}
	return p, err
}

// CreatePerson stores a person and links the given contacts manually.
func (c *Core) CreatePerson(ctx context.Context, in PersonInput) (model.Person, error) {
	var out model.Person
	err := c.tx(ctx, func(tx *store.Store) error {
		p, err := tx.CreatePerson(ctx, model.Person{Name: in.Name, Tags: in.Tags, Notes: in.Notes})
		if err != nil {
			return err
		}
		for _, l := range in.Links {
			if err := linkErr(tx.LinkContact(ctx, p.ID, l, model.LinkManual), l); err != nil {
				return err
			}
		}
		if out, err = tx.GetPerson(ctx, p.ID); err != nil {
			return err
		}
		return c.emitPerson(ctx, tx, out, in.Links)
	})
	return out, err
}

func linkErr(err error, l store.LinkRef) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errNotFound("contact " + l.UserID + " on " + l.AccountID)
	case errors.Is(err, store.ErrConflict):
		return &Error{Status: http.StatusConflict, Code: "conflict", Message: "contact already belongs to another person",
			Details: map[string]any{"account_id": l.AccountID, "user_id": l.UserID}}
	}
	return err
}

// PatchPerson changes name, tags, or notes.
func (c *Core) PatchPerson(ctx context.Context, id string, p PersonPatch) (model.Person, error) {
	var out model.Person
	err := c.tx(ctx, func(tx *store.Store) error {
		upd, err := tx.UpdatePerson(ctx, id, p.Name, p.Notes, p.Tags)
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("person")
		}
		if err != nil {
			return err
		}
		out = upd
		return c.emitPerson(ctx, tx, out, nil)
	})
	return out, err
}

// DeletePerson removes a person; contacts and messages stay.
func (c *Core) DeletePerson(ctx context.Context, id string) error {
	return c.tx(ctx, func(tx *store.Store) error {
		links, err := tx.DeletePerson(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("person")
		}
		if err != nil {
			return err
		}
		if err := emit(ctx, tx, "", model.EvPersonUpdated, map[string]any{"id": id, "deleted": true}); err != nil {
			return err
		}
		return c.emitContacts(ctx, tx, links)
	})
}

// LinkPerson adds a contact to a person by hand.
func (c *Core) LinkPerson(ctx context.Context, id string, l store.LinkRef) (model.Person, error) {
	var out model.Person
	err := c.tx(ctx, func(tx *store.Store) error {
		if _, err := tx.GetPerson(ctx, id); errors.Is(err, store.ErrNotFound) {
			return errNotFound("person")
		}
		if err := linkErr(tx.LinkContact(ctx, id, l, model.LinkManual), l); err != nil {
			return err
		}
		var err error
		if out, err = tx.GetPerson(ctx, id); err != nil {
			return err
		}
		return c.emitPerson(ctx, tx, out, []store.LinkRef{l})
	})
	return out, err
}

// UnlinkPerson removes a contact from a person; phone auto-linking will not join the pair again.
func (c *Core) UnlinkPerson(ctx context.Context, id string, l store.LinkRef) (model.Person, error) {
	var out model.Person
	err := c.tx(ctx, func(tx *store.Store) error {
		if err := tx.UnlinkContact(ctx, id, l); errors.Is(err, store.ErrNotFound) {
			return errNotFound("link")
		} else if err != nil {
			return err
		}
		var err error
		if out, err = tx.GetPerson(ctx, id); err != nil {
			return err
		}
		return c.emitPerson(ctx, tx, out, []store.LinkRef{l})
	})
	return out, err
}

// MergePersons folds the from persons into id.
func (c *Core) MergePersons(ctx context.Context, id string, from []string) (model.Person, error) {
	if len(from) == 0 {
		return model.Person{}, errInvalid("from is required")
	}
	var out model.Person
	err := c.tx(ctx, func(tx *store.Store) error {
		var moved []store.LinkRef
		for _, src := range from {
			if src == id {
				return errInvalid("a person cannot be merged into itself")
			}
			links, err := tx.PersonLinks(ctx, src)
			if err != nil {
				return err
			}
			moved = append(moved, links...)
		}
		merged, err := tx.MergePersons(ctx, id, from)
		if errors.Is(err, store.ErrNotFound) {
			return errNotFound("person")
		}
		if err != nil {
			return err
		}
		out = merged
		for _, src := range from {
			if err := emit(ctx, tx, "", model.EvPersonUpdated, map[string]any{"id": src, "deleted": true, "merged_into": id}); err != nil {
				return err
			}
		}
		return c.emitPerson(ctx, tx, out, moved)
	})
	return out, err
}

// PersonChats lists the direct chats with a person's contacts.
func (c *Core) PersonChats(ctx context.Context, id string) ([]model.Chat, error) {
	if _, err := c.GetPerson(ctx, id); err != nil {
		return nil, err
	}
	return c.st.PersonChats(ctx, id)
}

// PersonMessages merges a person's messages across accounts; scope is "direct" (default) or "all".
func (c *Core) PersonMessages(ctx context.Context, id, scope, cursor string, limit int) ([]model.Message, string, error) {
	if scope == "" {
		scope = "direct"
	}
	if scope != "direct" && scope != "all" {
		return nil, "", errInvalid("scope must be direct or all")
	}
	if _, err := c.GetPerson(ctx, id); err != nil {
		return nil, "", err
	}
	if Scoped(ctx) { // "all" would add the person's messages in chats outside the scope
		scope = "direct"
	}
	rows, next, err := c.st.PersonMessages(ctx, id, scope, cursor, limit)
	if err != nil {
		if cursor != "" && err.Error() == "bad cursor" {
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

// SuggestPersons lists unlinked contacts that share a phone across accounts.
func (c *Core) SuggestPersons(ctx context.Context) ([]model.PersonSuggestion, error) {
	return c.st.SuggestPersons(ctx)
}

// PersonScope resolves a person into an event filter.
func (c *Core) PersonScope(ctx context.Context, id string) (*store.PersonScope, error) {
	if _, err := c.GetPerson(ctx, id); err != nil {
		return nil, err
	}
	links, err := c.st.PersonLinks(ctx, id)
	if err != nil {
		return nil, err
	}
	return &store.PersonScope{ID: id, Links: links}, nil
}

// emitPerson emits person.updated and contact.updated for each contact whose person_id changed.
func (c *Core) emitPerson(ctx context.Context, tx *store.Store, p model.Person, touched []store.LinkRef) error {
	if err := emit(ctx, tx, "", model.EvPersonUpdated, p); err != nil {
		return err
	}
	return c.emitContacts(ctx, tx, touched)
}

func (c *Core) emitContacts(ctx context.Context, tx *store.Store, refs []store.LinkRef) error {
	for _, l := range refs {
		ct, err := tx.GetContact(ctx, l.AccountID, l.UserID)
		if err != nil {
			continue
		}
		if err := emit(ctx, tx, l.AccountID, model.EvContactUpdated, ct); err != nil {
			return err
		}
	}
	return nil
}

// autoLink joins a contact to a person by phone (storage.md §3): when exactly one person is
// involved among the other accounts' contacts with the same phone, join it; when none is, create
// one with them; when several are, leave it to /persons/suggest.
func (c *Core) autoLink(ctx context.Context, tx *store.Store, accountID, userID string) error {
	if !c.autoLinkPhone {
		return nil
	}
	me := store.LinkRef{AccountID: accountID, UserID: userID}
	if pid, err := tx.PersonOf(ctx, me); err != nil || pid != "" {
		return err
	}
	matches, err := tx.PhoneMatches(ctx, me)
	if err != nil || len(matches) == 0 {
		return err
	}
	ct, err := tx.GetContact(ctx, accountID, userID)
	if err != nil || ct.IsSelf {
		return err
	}
	persons := map[string]bool{}
	var unlinked []store.LinkRef
	for _, m := range matches {
		if m.PersonID != "" {
			persons[m.PersonID] = true
		} else {
			unlinked = append(unlinked, m.LinkRef)
		}
	}
	touched := []store.LinkRef{me}
	var personID string
	switch len(persons) {
	case 0:
		p, err := tx.CreatePerson(ctx, model.Person{Name: ct.Name})
		if err != nil {
			return err
		}
		personID = p.ID
		for _, u := range unlinked {
			if err := tx.LinkContact(ctx, personID, u, model.LinkPhone); err != nil {
				return err
			}
			touched = append(touched, u)
		}
	case 1:
		for id := range persons {
			personID = id
		}
	default:
		return nil
	}
	if err := tx.LinkContact(ctx, personID, me, model.LinkPhone); err != nil {
		return err
	}
	p, err := tx.GetPerson(ctx, personID)
	if err != nil {
		return err
	}
	return c.emitPerson(ctx, tx, p, touched)
}
