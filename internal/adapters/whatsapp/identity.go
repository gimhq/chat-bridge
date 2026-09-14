package whatsapp

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow/types"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

// Identity follows mautrix-whatsapp: a user (and the direct chat with them) is addressed by
// their LID whenever whatsmeow's LID map knows it, and by the phone JID otherwise; the phone
// number stays a contact attribute. When a phone JID turns out to have a LID, the adapter emits
// an identity event so the core moves what it stored under the phone form.

var _ adapter.IdentityResolver = (*Adapter)(nil)

const lookupTimeout = 5 * time.Second

func isUser(j types.JID) bool {
	return j.Server == types.DefaultUserServer || j.Server == types.HiddenUserServer
}

// userJID returns the id of a user; alt is the other form when the event carries one.
func (acc *account) userJID(j, alt types.JID) types.JID {
	j, alt = j.ToNonAD(), alt.ToNonAD()
	switch {
	case j.Server == types.HiddenUserServer:
		if alt.Server == types.DefaultUserServer {
			acc.learn(alt, j)
		}
		return j
	case j.Server != types.DefaultUserServer:
		return j
	case alt.Server == types.HiddenUserServer:
		acc.learn(j, alt)
		return alt
	}
	st := acc.cli.Store
	if st.ID != nil && st.ID.User == j.User {
		if lid := st.GetLID().ToNonAD(); !lid.IsEmpty() {
			acc.learn(j, lid)
			return lid
		}
		return j
	}
	if st.LIDs == nil {
		return j
	}
	ctx, cancel := base.Timeout(lookupTimeout)
	defer cancel()
	lid, err := st.LIDs.GetLIDForPN(ctx, j)
	if err != nil || lid.IsEmpty() {
		return j
	}
	lid = lid.ToNonAD()
	acc.learn(j, lid)
	return lid
}

// canonID maps a chat or user JID: users through userJID, groups and others unchanged.
func (acc *account) canonID(j types.JID) types.JID {
	if isUser(j) {
		return acc.userJID(j, types.EmptyJID)
	}
	return j.ToNonAD()
}

// chatJID is the chat a message belongs to (mautrix-whatsapp's portal key).
func (acc *account) chatJID(src types.MessageSource) types.JID {
	if src.IsGroup {
		return src.Chat
	}
	alt := src.RecipientAlt
	if !src.IsFromMe && src.Chat.ToNonAD() == src.Sender.ToNonAD() {
		alt = src.SenderAlt
	}
	return acc.userJID(src.Chat, alt)
}

// phoneJID returns the phone form of a user (either form, alt optional), or an empty JID.
func (acc *account) phoneJID(j, alt types.JID) types.JID {
	j, alt = j.ToNonAD(), alt.ToNonAD()
	switch {
	case j.Server == types.DefaultUserServer:
		return j
	case alt.Server == types.DefaultUserServer:
		return alt
	case j.Server != types.HiddenUserServer:
		return types.EmptyJID
	}
	st := acc.cli.Store
	if st.ID != nil && st.GetLID().User == j.User {
		return st.ID.ToNonAD()
	}
	if st.LIDs == nil {
		return types.EmptyJID
	}
	ctx, cancel := base.Timeout(lookupTimeout)
	defer cancel()
	pn, err := st.LIDs.GetPNForLID(ctx, j)
	if err != nil {
		return types.EmptyJID
	}
	return pn.ToNonAD()
}

// isOwn reports whether j is the account itself in either form.
func (acc *account) isOwn(j types.JID) bool {
	j, st := j.ToNonAD(), acc.cli.Store
	if st.ID == nil {
		return false
	}
	return (j.Server == types.DefaultUserServer && j.User == st.ID.User) ||
		(j.Server == types.HiddenUserServer && j.User == st.GetLID().User)
}

// learn records that pn is addressed as lid; the first sighting queues an identity event.
func (acc *account) learn(pn, lid types.JID) {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	key := pn.String()
	if acc.known[key] {
		return
	}
	if acc.known == nil {
		acc.known = map[string]bool{}
	}
	acc.known[key] = true
	acc.pending = append(acc.pending, adapter.Event{Kind: adapter.EvIdentity, UserID: key, NewID: lid.String()})
}

// emit reports events, preceded by the identity changes learned while converting them.
func (acc *account) emit(evs ...adapter.Event) {
	acc.mu.Lock()
	pending := acc.pending
	acc.pending = nil
	acc.mu.Unlock()
	if len(pending)+len(evs) == 0 {
		return
	}
	acc.rep.Events(append(pending, evs...)...)
}

// contactFor builds a user's contact from whatsmeow's rows for both forms: address-book names
// are keyed by phone number, push names often by LID.
func (acc *account) contactFor(ctx context.Context, j types.JID) model.Contact {
	id := acc.canonID(j)
	pn := acc.phoneJID(id, j)
	c := model.Contact{ID: id.String(), Handle: phoneOf(pn), Phone: phoneOf(pn)}
	if acc.cli.Store.Contacts == nil {
		return c
	}
	for _, k := range []types.JID{pn, id} {
		if k.IsEmpty() {
			continue
		}
		ci, err := acc.cli.Store.Contacts.GetContact(ctx, k)
		if err != nil || !ci.Found {
			continue
		}
		c.Names.Alias = firstNonEmpty(c.Names.Alias, ci.FullName)
		c.Names.First = firstNonEmpty(c.Names.First, ci.FirstName)
		c.Names.Profile = firstNonEmpty(c.Names.Profile, ci.PushName, ci.BusinessName)
		c.IsContact = c.IsContact || ci.FullName != ""
	}
	return c
}

// allContacts is whatsmeow's contact store with one entry per user.
func (acc *account) allContacts(ctx context.Context) ([]model.Contact, error) {
	if acc.cli.Store.Contacts == nil {
		return nil, nil
	}
	all, err := acc.cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[types.JID]bool, len(all))
	out := make([]model.Contact, 0, len(all))
	for j := range all {
		if !isUser(j) {
			continue
		}
		id := acc.canonID(j)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, acc.contactFor(ctx, id))
	}
	return out, nil
}

// CanonicalIDs maps stored phone JIDs that now have a LID (the account's own included).
func (a *Adapter) CanonicalIDs(ctx context.Context, id string, ids []string) (map[string]string, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return nil, err
	}
	st := acc.cli.Store
	if st.ID == nil || st.LIDs == nil {
		return nil, nil
	}
	pns := make([]types.JID, 0, len(ids))
	for _, s := range ids {
		if j, err := types.ParseJID(s); err == nil && j.Server == types.DefaultUserServer {
			pns = append(pns, j.ToNonAD())
		}
	}
	lids, err := st.LIDs.GetManyLIDsForPNs(ctx, pns)
	if err != nil {
		return nil, base.PlatformErr("lid map", err)
	}
	out := make(map[string]string, len(lids)+1)
	for pn, lid := range lids {
		if !lid.IsEmpty() {
			out[pn.ToNonAD().String()] = lid.ToNonAD().String()
		}
	}
	if own, lid := st.ID.ToNonAD(), st.GetLID().ToNonAD(); !lid.IsEmpty() {
		for _, pn := range pns {
			if pn == own {
				out[own.String()] = lid.String()
			}
		}
	}
	acc.mu.Lock()
	if acc.known == nil {
		acc.known = map[string]bool{}
	}
	for pn := range out {
		acc.known[pn] = true
	}
	acc.mu.Unlock()
	acc.refreshPhones(ctx, ids)
	return out, nil
}

// refreshPhones re-emits stored LID users whose phone number the LID map knows, so a contact first
// stored without one (converted before the mapping arrived) gets it.
func (acc *account) refreshPhones(ctx context.Context, ids []string) {
	batch := make([]adapter.Event, 0, 100)
	for _, s := range ids {
		j, err := types.ParseJID(s)
		if err != nil || j.Server != types.HiddenUserServer || acc.isOwn(j) {
			continue
		}
		c := acc.contactFor(ctx, j)
		if c.Phone == "" {
			continue
		}
		batch = append(batch, adapter.Event{Kind: adapter.EvContact, Contact: &c})
		if len(batch) == cap(batch) {
			acc.emit(batch...)
			batch = make([]adapter.Event, 0, 100)
		}
	}
	acc.emit(batch...)
}
