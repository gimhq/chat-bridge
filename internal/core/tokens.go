package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// tokenPrefix marks the secrets of scoped tokens.
const tokenPrefix = "cbt_"

type tokenKey struct{}

// WithToken marks ctx as acting for the scoped token id. A context without it is unrestricted:
// the admin token, adapters and background workers.
func WithToken(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, tokenKey{}, id)
}

// Scoped reports whether ctx acts for a scoped token.
func Scoped(ctx context.Context) bool {
	id, _ := ctx.Value(tokenKey{}).(string)
	return id != ""
}

func errForbidden(msg string) *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: msg}
}

func errUnauthorized() *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "invalid or missing bearer token"}
}

// scope is the resolved allowlist of a scoped token: what it may see right now.
type scope struct {
	persons  map[string]bool
	contacts map[store.LinkRef]bool
	chats    map[store.ChatRef]bool
	accounts map[string]bool
	readOnly bool
}

// scopeOf resolves the scope of the token ctx acts for; nil means unrestricted. It reads the
// token on every call, so a changed or revoked token takes effect at once, also on open streams.
func (c *Core) scopeOf(ctx context.Context) (*scope, error) {
	id, _ := ctx.Value(tokenKey{}).(string)
	if id == "" {
		return nil, nil
	}
	t, err := c.st.GetToken(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errUnauthorized()
	}
	if err != nil {
		return nil, err
	}
	sc := &scope{persons: map[string]bool{}, contacts: map[store.LinkRef]bool{}, chats: map[store.ChatRef]bool{}, accounts: map[string]bool{},
		readOnly: t.Scope.ReadOnly}
	for _, ct := range t.Scope.Contacts {
		sc.contacts[store.LinkRef{AccountID: ct.AccountID, UserID: ct.UserID}] = true
	}
	for _, p := range t.Scope.Persons {
		links, err := c.st.PersonLinks(ctx, p)
		if err != nil {
			return nil, err
		}
		sc.persons[p] = true
		for _, l := range links {
			sc.contacts[l] = true
		}
	}
	for l := range sc.contacts {
		direct, err := c.st.DirectChats(ctx, l)
		if err != nil {
			return nil, err
		}
		for _, chatID := range direct {
			sc.chats[store.ChatRef{AccountID: l.AccountID, ChatID: chatID}] = true
		}
		sc.accounts[l.AccountID] = true
	}
	for _, ch := range t.Scope.Chats {
		sc.chats[store.ChatRef{AccountID: ch.AccountID, ChatID: ch.ChatID}] = true
		sc.accounts[ch.AccountID] = true
	}
	return sc, nil
}

// chatIDs lists the allowed chats of one account.
func (sc *scope) chatIDs(accountID string) store.Only {
	only := store.Only{Set: true}
	for ref := range sc.chats {
		if ref.AccountID == accountID {
			only.IDs = append(only.IDs, ref.ChatID)
		}
	}
	sort.Strings(only.IDs)
	return only
}

// contactIDs lists the allowed contacts of one account.
func (sc *scope) contactIDs(accountID string) store.Only {
	only := store.Only{Set: true}
	for ref := range sc.contacts {
		if ref.AccountID == accountID {
			only.IDs = append(only.IDs, ref.UserID)
		}
	}
	sort.Strings(only.IDs)
	return only
}

// events renders the scope for the event log.
func (sc *scope) events() *store.EventScope {
	out := &store.EventScope{}
	for a := range sc.accounts {
		out.Accounts = append(out.Accounts, a)
	}
	for ref := range sc.chats {
		out.Chats = append(out.Chats, ref)
	}
	for ref := range sc.contacts {
		out.Contacts = append(out.Contacts, ref)
	}
	for p := range sc.persons {
		out.Persons = append(out.Persons, p)
	}
	return out
}

// allowWrite answers forbidden when ctx acts for a read-only token. It guards every call that
// reaches the platform on the token's behalf.
func (c *Core) allowWrite(ctx context.Context) error {
	sc, err := c.scopeOf(ctx)
	if err != nil || sc == nil {
		return err
	}
	if sc.readOnly {
		return errForbidden("this token is read-only")
	}
	return nil
}

// scopedHandle maps a handle a scoped token wants to resolve to the allowed contact it names: the
// contact's user id, or the handle or phone the store holds for it. Anything else is not_found,
// so a token cannot probe handles outside its scope through the platform.
func (c *Core) scopedHandle(ctx context.Context, sc *scope, accountID, handle string) (string, error) {
	for ref := range sc.contacts {
		if ref.AccountID != accountID {
			continue
		}
		if ref.UserID == handle {
			return ref.UserID, nil
		}
		ct, err := c.st.GetContact(ctx, accountID, ref.UserID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		if handle == ct.Handle || handle == ct.Phone {
			return ref.UserID, nil
		}
	}
	return "", errNotFound("chat")
}

// allowChat answers not_found for a chat outside the token's scope, so a scoped token cannot
// tell a hidden chat from a missing one.
func (c *Core) allowChat(ctx context.Context, accountID, chatID string) error {
	sc, err := c.scopeOf(ctx)
	if err != nil || sc == nil {
		return err
	}
	if !sc.chats[store.ChatRef{AccountID: accountID, ChatID: chatID}] {
		return errNotFound("chat")
	}
	return nil
}

// allowMessage is allowChat for the chat a stored message belongs to.
func (c *Core) allowMessage(ctx context.Context, m store.Stored) error {
	if err := c.allowChat(ctx, m.AccountID, m.ChatID); err != nil {
		if AsError(err).Code == "not_found" {
			return errNotFound("message")
		}
		return err
	}
	return nil
}

// allowContact answers not_found for a contact outside the token's scope.
func (c *Core) allowContact(ctx context.Context, accountID, userID string) error {
	sc, err := c.scopeOf(ctx)
	if err != nil || sc == nil {
		return err
	}
	if !sc.contacts[store.LinkRef{AccountID: accountID, UserID: userID}] {
		return errNotFound("contact")
	}
	return nil
}

// allowPerson answers not_found for a person the token's scope does not list.
func (c *Core) allowPerson(ctx context.Context, id string) error {
	sc, err := c.scopeOf(ctx)
	if err != nil || sc == nil {
		return err
	}
	if !sc.persons[id] {
		return errNotFound("person")
	}
	return nil
}

// allowAccount answers not_found for an account the token has nothing on.
func (c *Core) allowAccount(ctx context.Context, accountID string) error {
	sc, err := c.scopeOf(ctx)
	if err != nil || sc == nil {
		return err
	}
	if !sc.accounts[accountID] {
		return errNotFound("account")
	}
	return nil
}

// allowMedia lets a scoped token read media only through a message in an allowed chat. Media no
// message references (uploads, avatars) stays hidden.
func (c *Core) allowMedia(ctx context.Context, md store.Media) error {
	if !Scoped(ctx) {
		return nil
	}
	ref, ok, err := c.st.MediaChat(ctx, md)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("media")
	}
	if err := c.allowChat(ctx, ref.AccountID, ref.ChatID); err != nil {
		if AsError(err).Code == "not_found" {
			return errNotFound("media")
		}
		return err
	}
	return nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Authenticate maps a bearer secret to the id of its scoped token.
func (c *Core) Authenticate(ctx context.Context, secret string) (string, error) {
	if !strings.HasPrefix(secret, tokenPrefix) {
		return "", errUnauthorized()
	}
	t, err := c.st.TokenBySecret(ctx, hashSecret(secret))
	if errors.Is(err, store.ErrNotFound) {
		return "", errUnauthorized()
	}
	if err != nil {
		return "", err
	}
	if err := c.st.TouchToken(ctx, t.ID, time.Now()); err != nil {
		c.log.Warn("touch token", "token", t.ID, "err", err)
	}
	return t.ID, nil
}

// TokenInput is the body of POST /tokens.
type TokenInput struct {
	Name  string           `json:"name"`
	Scope model.TokenScope `json:"scope"`
}

// TokenPatch is the body of PATCH /tokens/{id}; nil leaves a field alone.
type TokenPatch struct {
	Name  *string           `json:"name"`
	Scope *model.TokenScope `json:"scope"`
}

// checkScope validates and normalises a scope: persons and accounts must exist, ids must be set.
func (c *Core) checkScope(ctx context.Context, sc *model.TokenScope) error {
	if sc.Persons == nil {
		sc.Persons = []string{}
	}
	if sc.Contacts == nil {
		sc.Contacts = []model.TokenContact{}
	}
	if sc.Chats == nil {
		sc.Chats = []model.TokenChat{}
	}
	for _, p := range sc.Persons {
		if _, err := c.st.GetPerson(ctx, p); errors.Is(err, store.ErrNotFound) {
			return errInvalid("scope.persons: person %q does not exist", p)
		} else if err != nil {
			return err
		}
	}
	account := func(field, id string) error {
		if _, err := c.st.GetAccount(ctx, id); errors.Is(err, store.ErrNotFound) {
			return errInvalid("%s: account %q does not exist", field, id)
		} else if err != nil {
			return err
		}
		return nil
	}
	for _, ct := range sc.Contacts {
		if ct.UserID == "" {
			return errInvalid("scope.contacts: user_id is required")
		}
		if err := account("scope.contacts", ct.AccountID); err != nil {
			return err
		}
	}
	for _, ch := range sc.Chats {
		if ch.ChatID == "" {
			return errInvalid("scope.chats: chat_id is required")
		}
		if err := account("scope.chats", ch.AccountID); err != nil {
			return err
		}
	}
	return nil
}

// CreateToken stores a scoped token and returns it with its secret, which is not kept.
func (c *Core) CreateToken(ctx context.Context, in TokenInput) (model.Token, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return model.Token{}, errInvalid("name is required")
	}
	if err := c.checkScope(ctx, &in.Scope); err != nil {
		return model.Token{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return model.Token{}, err
	}
	secret := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	t := model.Token{ID: "tok_" + uuid.NewString(), Name: in.Name, Scope: in.Scope, CreatedAt: time.Now().UTC().Truncate(time.Second)}
	if err := c.st.CreateToken(ctx, t, hashSecret(secret)); err != nil {
		return model.Token{}, err
	}
	t.Secret = secret
	return t, nil
}

// ListTokens returns every scoped token, without secrets.
func (c *Core) ListTokens(ctx context.Context) ([]model.Token, error) {
	return c.st.ListTokens(ctx)
}

// GetToken returns one scoped token.
func (c *Core) GetToken(ctx context.Context, id string) (model.Token, error) {
	t, err := c.st.GetToken(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Token{}, errNotFound("token")
	}
	return t, err
}

// PatchToken renames a token or replaces its scope.
func (c *Core) PatchToken(ctx context.Context, id string, p TokenPatch) (model.Token, error) {
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return model.Token{}, errInvalid("name must not be empty")
		}
		p.Name = &name
	}
	if p.Scope != nil {
		if err := c.checkScope(ctx, p.Scope); err != nil {
			return model.Token{}, err
		}
	}
	if err := c.st.UpdateToken(ctx, id, p.Name, p.Scope); errors.Is(err, store.ErrNotFound) {
		return model.Token{}, errNotFound("token")
	} else if err != nil {
		return model.Token{}, err
	}
	return c.GetToken(ctx, id)
}

// DeleteToken revokes a token; requests and streams using it fail from then on.
func (c *Core) DeleteToken(ctx context.Context, id string) error {
	if err := c.st.DeleteToken(ctx, id); errors.Is(err, store.ErrNotFound) {
		return errNotFound("token")
	} else if err != nil {
		return err
	}
	c.bus.notify() // wake open streams so they notice the revocation
	return nil
}

// TokenSelf returns the scoped token ctx acts for; ok is false for the admin token.
func (c *Core) TokenSelf(ctx context.Context) (model.Token, bool, error) {
	id, _ := ctx.Value(tokenKey{}).(string)
	if id == "" {
		return model.Token{}, false, nil
	}
	t, err := c.st.GetToken(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Token{}, true, errUnauthorized()
	}
	return t, true, err
}
