// Package matrix is the Matrix adapter, built on mautrix-go (no end-to-end encryption).
package matrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

const (
	platform      = "matrix"
	flowPassword  = "password"
	flowToken     = "token"
	sessionFile   = "session.json"
	typingTimeout = 30 * time.Second
)

type config struct {
	Homeserver string `json:"homeserver"`
	DeviceName string `json:"device_name"`
}

// session is the persisted login.
type session struct {
	UserID      id.UserID   `json:"user_id"`
	DeviceID    id.DeviceID `json:"device_id"`
	AccessToken string      `json:"access_token"`
	NextBatch   string      `json:"next_batch"`
	FilterID    string      `json:"filter_id"`
	PickleKey   []byte      `json:"pickle_key,omitempty"` // encrypts the E2EE store at rest
}

// Adapter hosts one mautrix client per account.
type Adapter struct {
	log      *slog.Logger
	sink     adapter.Sink
	accounts base.Accounts[*account]
}

// New returns an unstarted adapter.
func New(log *slog.Logger) *Adapter { return &Adapter{log: log} }

// Info declares Matrix capabilities.
func (a *Adapter) Info() adapter.Info {
	return adapter.Info{
		Platform: platform, Name: "Matrix (mautrix-go)", Version: "0.1.0",
		Capabilities: []string{
			adapter.CapSendText, adapter.CapSendMedia, adapter.CapSendLocation, adapter.CapReply, adapter.CapThread,
			adapter.CapEdit, adapter.CapDelete, adapter.CapReaction, adapter.CapChatRead, adapter.CapChatTyping,
			adapter.CapChatResolve, adapter.CapChatMembers, adapter.CapPresence, adapter.CapReceipts,
			adapter.CapMarkdown, adapter.CapHTML, adapter.CapKeys,
			adapter.CapChatCreate, adapter.CapHistory, adapter.CapSelfUpdate,
		},
		LoginFlows: []model.LoginFlow{
			{ID: flowPassword, Name: "Username and password"},
			{ID: flowToken, Name: "Access token"},
		},
		ConfigSchema: json.RawMessage(`{"type":"object","required":["homeserver"],"properties":{
			"homeserver":{"type":"string"},"device_name":{"type":"string","default":"chat-bridge"}}}`),
	}
}

func (a *Adapter) Start(_ context.Context, sink adapter.Sink) error { a.sink = sink; return nil }

func (a *Adapter) Stop(context.Context) error {
	a.accounts.Each(func(_ string, acc *account) { acc.stopSync() })
	return nil
}

// AddAccount loads the session file and starts syncing if logged in.
func (a *Adapter) AddAccount(_ context.Context, accountID string, cfg json.RawMessage, dataDir string) error {
	var c config
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &c); err != nil {
			return adapter.Errorf(adapter.ErrInvalidInput, "config: %v", err)
		}
	}
	if c.DeviceName == "" {
		c.DeviceName = "chat-bridge"
	}
	if acc, err := a.accounts.Get(accountID); err == nil {
		acc.cfg = c
		return nil
	}
	if c.Homeserver == "" {
		return adapter.Errorf(adapter.ErrInvalidInput, "config.homeserver is required")
	}
	acc := &account{id: accountID, dir: dataDir, cfg: c, rep: base.Reporter{Sink: a.sink, ID: accountID, Log: a.log.With("account", accountID)},
		roomKind: map[id.RoomID]string{}, reactions: map[string]id.EventID{}}
	a.accounts.Put(accountID, acc)
	if s, err := acc.loadSession(); err == nil && s.AccessToken != "" {
		if err := acc.open(s); err != nil {
			acc.rep.Status(adapter.Status{Status: model.StatusError, Error: &model.Error{Code: "adapter_error", Message: err.Error()}})
			return nil
		}
		acc.rep.Status(adapter.Status{Status: model.StatusConnecting})
		acc.startSync()
		return nil
	}
	acc.rep.Status(adapter.Status{Status: model.StatusUnpaired})
	return nil
}

func (a *Adapter) RemoveAccount(ctx context.Context, accountID string) error {
	acc, ok := a.accounts.Delete(accountID)
	if !ok {
		return nil
	}
	acc.stopSync()
	acc.closeCrypto()
	if acc.cli != nil {
		_, _ = acc.cli.Logout(ctx)
	}
	return os.RemoveAll(acc.dir)
}

func (a *Adapter) Reconnect(_ context.Context, accountID string) error {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return err
	}
	if acc.cli == nil {
		return adapter.Errorf(adapter.ErrNotConnected, "account is not logged in")
	}
	acc.stopSync()
	acc.rep.Status(adapter.Status{Status: model.StatusConnecting})
	acc.startSync()
	return nil
}

func (a *Adapter) Logout(ctx context.Context, accountID string) error {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return err
	}
	acc.login.Cancel()
	acc.stopSync()
	acc.removeCrypto()
	if acc.cli != nil {
		_, _ = acc.cli.Logout(ctx)
		acc.cli = nil
	}
	return os.Remove(filepath.Join(acc.dir, sessionFile))
}

// --- login ---

func (a *Adapter) LoginStart(_ context.Context, accountID, flow string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return model.LoginStep{}, err
	}
	if acc.cli != nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "already logged in; logout first")
	}
	acc.login.Start(flow, nil)
	switch flow {
	case flowPassword:
		return acc.login.SetStep(base.Input(flow,
			model.LoginField{Name: "user", Type: "text", Label: "Matrix user id or localpart"},
			model.LoginField{Name: "password", Type: "password", Label: "Password"})), nil
	case flowToken:
		return acc.login.SetStep(base.Input(flow,
			model.LoginField{Name: "user", Type: "text", Label: "Matrix user id"},
			model.LoginField{Name: "token", Type: "password", Label: "Access token"})), nil
	}
	acc.login.Cancel()
	return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "unknown flow %q", flow)
}

func (a *Adapter) LoginSubmit(ctx context.Context, accountID string, fields map[string]string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return model.LoginStep{}, err
	}
	lf := acc.login.Current()
	if lf == nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "no login in progress")
	}
	user := strings.TrimSpace(fields["user"])
	if user == "" {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "user is required")
	}
	var s session
	switch lf.Name {
	case flowPassword:
		cli, err := mautrix.NewClient(acc.cfg.Homeserver, "", "")
		if err != nil {
			return model.LoginStep{}, base.PlatformErr("client", err)
		}
		resp, err := cli.Login(ctx, &mautrix.ReqLogin{
			Type: mautrix.AuthTypePassword, Identifier: mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: user},
			Password: fields["password"], InitialDeviceDisplayName: acc.cfg.DeviceName,
		})
		if err != nil {
			acc.login.Cancel()
			return base.Failed(lf.Name, err.Error()), nil
		}
		s = session{UserID: resp.UserID, DeviceID: resp.DeviceID, AccessToken: resp.AccessToken}
	case flowToken:
		if fields["token"] == "" {
			return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "token is required")
		}
		cli, err := mautrix.NewClient(acc.cfg.Homeserver, id.UserID(user), fields["token"])
		if err != nil {
			return model.LoginStep{}, base.PlatformErr("client", err)
		}
		who, err := cli.Whoami(ctx)
		if err != nil {
			acc.login.Cancel()
			return base.Failed(lf.Name, err.Error()), nil
		}
		s = session{UserID: who.UserID, DeviceID: who.DeviceID, AccessToken: fields["token"]}
	}
	if err := acc.saveSession(s); err != nil {
		return model.LoginStep{}, err
	}
	if err := acc.open(s); err != nil {
		return model.LoginStep{}, base.PlatformErr("client", err)
	}
	self := acc.selfContact(ctx)
	acc.login.Cancel()
	acc.startSync()
	return model.LoginStep{Flow: lf.Name, Step: model.StepDone, Self: self}, nil
}

func (a *Adapter) LoginRefresh(_ context.Context, accountID string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return model.LoginStep{}, err
	}
	lf := acc.login.Current()
	if lf == nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "no login in progress")
	}
	return lf.Step, nil
}

func (a *Adapter) LoginCancel(_ context.Context, accountID string) error {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return err
	}
	acc.login.Cancel()
	return nil
}

// account is one Matrix session.
type account struct {
	id    string
	dir   string
	cfg   config
	rep   base.Reporter
	login base.Login

	mu        sync.Mutex
	cli       *mautrix.Client
	crypto    *cryptohelper.CryptoHelper
	cryptoDB  *dbutil.Database
	cancel    context.CancelFunc
	roomKind  map[id.RoomID]string
	direct    map[id.UserID]id.RoomID
	reactions map[string]id.EventID // "<event>|<emoji>" → our reaction event
	connected bool

	invites     map[id.RoomID]*adapter.Request // pending invites reported this session
	inviteNames map[id.RoomID]string           // room names from invite state
}

func (acc *account) loadSession() (session, error) {
	var s session
	b, err := os.ReadFile(filepath.Join(acc.dir, sessionFile))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func (acc *account) saveSession(s session) error {
	b, _ := json.Marshal(s)
	return os.WriteFile(filepath.Join(acc.dir, sessionFile), b, 0o600)
}

// open builds the client and wires sync handlers.
func (acc *account) open(s session) error {
	cli, err := mautrix.NewClient(acc.cfg.Homeserver, s.UserID, s.AccessToken)
	if err != nil {
		return err
	}
	cli.DeviceID = s.DeviceID
	cli.Store = &fileSyncStore{acc: acc}
	syncer := mautrix.NewDefaultSyncer()
	syncer.OnEventType(event.EventMessage, acc.onMessage)
	syncer.OnEventType(event.EventSticker, acc.onMessage)
	syncer.OnEventType(event.EventReaction, acc.onReaction)
	syncer.OnEventType(event.EventRedaction, acc.onRedaction)
	syncer.OnEventType(event.StateMember, acc.onMember)
	syncer.OnEventType(event.StateRoomName, acc.onRoomName)
	syncer.OnEventType(event.EphemeralEventTyping, acc.onTyping)
	syncer.OnEventType(event.EphemeralEventReceipt, acc.onReceipt)
	syncer.OnEventType(event.EphemeralEventPresence, acc.onPresence)
	syncer.OnEventType(event.CallInvite, acc.onCallInvite)
	syncer.OnEventType(event.CallHangup, acc.onCallEnd)
	syncer.OnEventType(event.CallReject, acc.onCallEnd)
	syncer.OnEventType(event.CallAnswer, acc.onCallEnd)
	syncer.OnSync(func(context.Context, *mautrix.RespSync, string) bool {
		acc.mu.Lock()
		first := !acc.connected
		acc.connected = true
		acc.mu.Unlock()
		if first {
			ctx, cancel := base.Timeout(20 * time.Second)
			defer cancel()
			acc.loadDirect(ctx)
			acc.rep.Status(adapter.Status{Status: model.StatusConnected, Self: acc.selfContact(ctx),
				Device: rawJSON(map[string]any{"device_id": s.DeviceID, "homeserver": acc.cfg.Homeserver})})
		}
		return true
	})
	cli.Syncer = syncer
	acc.mu.Lock()
	acc.cli = cli
	acc.mu.Unlock()
	// The helper decrypts m.room.encrypted and re-dispatches the plaintext through the syncer,
	// so the handlers above see encrypted rooms exactly like plain ones.
	if err := acc.openCrypto(&s); err != nil {
		return fmt.Errorf("e2ee: %w", err)
	}
	return nil
}

func (acc *account) startSync() {
	ctx, cancel := context.WithCancel(context.Background())
	acc.mu.Lock()
	acc.cancel = cancel
	acc.connected = false
	cli := acc.cli
	acc.mu.Unlock()
	go func() {
		if err := acc.initCrypto(ctx); err != nil {
			acc.rep.Log.Error("e2ee init", "err", err)
			acc.rep.Status(adapter.Status{Status: model.StatusError, Error: &model.Error{Code: "adapter_error", Message: "e2ee init: " + err.Error()}})
			return
		}
		for {
			err := cli.SyncWithContext(ctx)
			if ctx.Err() != nil {
				return
			}
			acc.mu.Lock()
			acc.connected = false
			acc.mu.Unlock()
			if errors.Is(err, mautrix.MUnknownToken) {
				acc.rep.Log.Warn("access token revoked")
				_ = os.Remove(filepath.Join(acc.dir, sessionFile))
				acc.mu.Lock()
				acc.cli = nil
				acc.mu.Unlock()
				acc.rep.Status(adapter.Status{Status: model.StatusUnpaired, Error: &model.Error{Code: "logged_out_remotely", Message: err.Error()}})
				return
			}
			acc.rep.Log.Warn("sync stopped; retrying", "err", err)
			acc.rep.Status(adapter.Status{Status: model.StatusDisconnected, Error: &model.Error{Code: "network", Message: fmt.Sprint(err)}})
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

func (acc *account) stopSync() {
	acc.mu.Lock()
	if acc.cancel != nil {
		acc.cancel()
		acc.cancel = nil
	}
	acc.mu.Unlock()
}

func (acc *account) client() (*mautrix.Client, error) {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if acc.cli == nil {
		return nil, adapter.Errorf(adapter.ErrNotConnected, "account is not logged in")
	}
	return acc.cli, nil
}

func (acc *account) selfContact(ctx context.Context) *model.Contact {
	cli, err := acc.client()
	if err != nil {
		return nil
	}
	c := userContact(cli.UserID, "")
	c.IsSelf, c.IsContact = true, true
	if p, err := cli.GetProfile(ctx, cli.UserID); err == nil {
		c.Names.Profile = p.DisplayName
		c.Name = p.DisplayName
	}
	return c
}

// loadDirect reads m.direct so DM rooms are classified as direct chats.
func (acc *account) loadDirect(ctx context.Context) {
	cli, err := acc.client()
	if err != nil {
		return
	}
	var direct map[id.UserID][]id.RoomID
	if err := cli.GetAccountData(ctx, "m.direct", &direct); err != nil {
		return
	}
	acc.mu.Lock()
	acc.direct = map[id.UserID]id.RoomID{}
	for user, rooms := range direct {
		for _, r := range rooms {
			acc.roomKind[r] = model.ChatDirect
			if _, ok := acc.direct[user]; !ok {
				acc.direct[user] = r
			}
		}
	}
	acc.mu.Unlock()
}

func (acc *account) kindOf(room id.RoomID) string {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if k, ok := acc.roomKind[room]; ok {
		return k
	}
	return model.ChatGroup
}

// fileSyncStore persists next_batch and the filter id in the session file.
type fileSyncStore struct {
	acc *account
}

func (s *fileSyncStore) SaveFilterID(_ context.Context, _ id.UserID, filterID string) error {
	sess, _ := s.acc.loadSession()
	sess.FilterID = filterID
	return s.acc.saveSession(sess)
}

func (s *fileSyncStore) LoadFilterID(context.Context, id.UserID) (string, error) {
	sess, _ := s.acc.loadSession()
	return sess.FilterID, nil
}

func (s *fileSyncStore) SaveNextBatch(_ context.Context, _ id.UserID, next string) error {
	sess, _ := s.acc.loadSession()
	sess.NextBatch = next
	return s.acc.saveSession(sess)
}

func (s *fileSyncStore) LoadNextBatch(context.Context, id.UserID) (string, error) {
	sess, _ := s.acc.loadSession()
	return sess.NextBatch, nil
}

func userContact(user id.UserID, displayName string) *model.Contact {
	local, _, _ := user.Parse()
	return &model.Contact{ID: user.String(), Handle: user.String(), Names: model.Names{Username: local, Profile: displayName}}
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func httpStatus(err error) int {
	var he mautrix.HTTPError
	if errors.As(err, &he) {
		return he.Response.StatusCode
	}
	return 0
}

func mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	switch httpStatus(err) {
	case http.StatusNotFound, http.StatusForbidden:
		return adapter.Errorf(adapter.ErrInvalidTarget, "%s: %v", op, err)
	case http.StatusTooManyRequests:
		return adapter.Errorf(adapter.ErrRateLimited, "%s: %v", op, err)
	}
	return base.PlatformErr(op, err)
}

func drain(rc io.ReadCloser) { _, _ = io.Copy(io.Discard, rc); _ = rc.Close() }
