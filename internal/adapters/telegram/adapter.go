// Package telegram is the Telegram adapter, built on gotd/td (MTProto user account).
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

const (
	platform  = "telegram"
	flowPhone = "phone"
	flowQR    = "qr"
	seenCap   = 5000
)

type config struct {
	AppID      int    `json:"api_id"`
	AppHash    string `json:"api_hash"`
	DeviceName string `json:"device_name"`
}

// Adapter hosts one gotd client per account.
type Adapter struct {
	log      *slog.Logger
	sink     adapter.Sink
	defaults Defaults
	accounts base.Accounts[*account]
}

// New returns an unstarted adapter. d supplies api_id/api_hash for accounts that omit them.
func New(log *slog.Logger, d Defaults) *Adapter { return &Adapter{log: log, defaults: d} }

// Info declares Telegram capabilities.
func (a *Adapter) Info() adapter.Info {
	return adapter.Info{
		Platform: platform, Name: "Telegram (gotd)", Version: "0.1.0",
		Capabilities: []string{
			adapter.CapSendText, adapter.CapSendMedia, adapter.CapSendLocation, adapter.CapSendContact, adapter.CapReply,
			adapter.CapEdit, adapter.CapDelete, adapter.CapReaction, adapter.CapChatRead, adapter.CapChatTyping,
			adapter.CapChatResolve, adapter.CapChatMembers, adapter.CapPresence, adapter.CapReceipts, adapter.CapMarkdown,
			adapter.CapChatCreate, adapter.CapHistory, adapter.CapSelfUpdate,
		},
		LoginFlows: []model.LoginFlow{
			{ID: flowPhone, Name: "Phone number + code"},
			{ID: flowQR, Name: "Scan QR from Telegram app"},
		},
		ConfigSchema: json.RawMessage(`{"type":"object","properties":{
			"api_id":{"type":"integer","description":"overrides adapters.telegram.api_id"},
			"api_hash":{"type":"string","x-secret":true,"description":"overrides adapters.telegram.api_hash"},
			"device_name":{"type":"string","default":"chat-bridge"}}}`),
	}
}

// Start records the sink; accounts are added afterwards.
func (a *Adapter) Start(_ context.Context, sink adapter.Sink) error { a.sink = sink; return nil }

// Stop stops the client of every account.
func (a *Adapter) Stop(context.Context) error {
	a.accounts.Each(func(_ string, acc *account) { acc.stop() })
	return nil
}

// AddAccount builds the client and starts its run loop.
func (a *Adapter) AddAccount(_ context.Context, accountID string, cfg json.RawMessage, dataDir string) error {
	c, err := resolveConfig(a.defaults, cfg)
	if err != nil {
		return err
	}
	if acc, err := a.accounts.Get(accountID); err == nil {
		acc.cfg = c
		return nil
	}
	state, err := newFileState(statePath(dataDir))
	if err != nil {
		return fmt.Errorf("updates state: %w", err)
	}
	peerStore, err := newFilePeers(peersPath(dataDir))
	if err != nil {
		return fmt.Errorf("peers state: %w", err)
	}
	acc := &account{id: accountID, dir: dataDir, cfg: c, rep: base.Reporter{Sink: a.sink, ID: accountID, Log: a.log.With("account", accountID)},
		seen: map[int]string{}, state: state, peerStore: peerStore}
	a.accounts.Put(accountID, acc)
	acc.start()
	return nil
}

// RemoveAccount logs the session out, stops the client and deletes the account's directory.
func (a *Adapter) RemoveAccount(ctx context.Context, accountID string) error {
	acc, ok := a.accounts.Delete(accountID)
	if !ok {
		return nil
	}
	if cli := acc.client(); cli != nil && acc.isOnline() {
		_, _ = cli.API().AuthLogOut(ctx)
	}
	acc.stop()
	return os.RemoveAll(acc.dir)
}

// Reconnect restarts the client's run loop.
func (a *Adapter) Reconnect(_ context.Context, accountID string) error {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return err
	}
	acc.stop()
	acc.start()
	return nil
}

// Logout ends the session on Telegram, deletes the session file and restarts the client unauthorized.
func (a *Adapter) Logout(ctx context.Context, accountID string) error {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return err
	}
	acc.login.Cancel()
	if cli := acc.client(); cli != nil && acc.isOnline() {
		if _, err := cli.API().AuthLogOut(ctx); err != nil {
			acc.rep.Log.Warn("logout", "err", err)
		}
	}
	acc.stop()
	_ = os.Remove(acc.sessionPath())
	acc.start()
	return nil
}

// --- login ---

// LoginStart begins the phone or QR flow; the client must already be connected to Telegram.
func (a *Adapter) LoginStart(ctx context.Context, accountID, flow string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return model.LoginStep{}, err
	}
	if acc.isOnline() {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "already logged in; logout first")
	}
	if acc.client() == nil || !acc.ready() {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrNotConnected, "telegram connection not established yet; retry shortly")
	}
	switch flow {
	case flowPhone:
		acc.login.Start(flow, nil)
		return acc.login.SetStep(base.Input(flow, model.LoginField{Name: "phone", Type: "phone", Label: "Phone number with country code", Pattern: `^\+?[0-9]{8,15}$`})), nil
	case flowQR:
		return acc.startQR(ctx)
	}
	return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "unknown flow %q", flow)
}

// LoginSubmit advances the login with the submitted phone, code or 2FA password.
func (a *Adapter) LoginSubmit(ctx context.Context, accountID string, fields map[string]string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return model.LoginStep{}, err
	}
	lf := acc.login.Current()
	if lf == nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "no login in progress")
	}
	cli := acc.client()
	if cli == nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrNotConnected, "not connected")
	}
	au := cli.Auth()
	if lf.Name == flowQR {
		if lf.Data["need_password"] == "" {
			return lf.Step, nil
		}
		if _, err := au.Password(ctx, fields["password"]); err != nil {
			acc.login.Cancel()
			return base.Failed(flowQR, err.Error()), nil
		}
		acc.login.Cancel()
		self, err := acc.online(ctx)
		if err != nil {
			return base.Failed(flowQR, err.Error()), nil
		}
		return model.LoginStep{Flow: flowQR, Step: model.StepDone, Self: self}, nil
	}
	switch {
	case lf.Data["hash"] == "":
		phone := strings.TrimSpace(fields["phone"])
		if phone == "" {
			return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "phone is required")
		}
		if !strings.HasPrefix(phone, "+") {
			phone = "+" + phone
		}
		sent, err := au.SendCode(ctx, phone, auth.SendCodeOptions{})
		if err != nil {
			return model.LoginStep{}, base.PlatformErr("send code", err)
		}
		code, ok := sent.(*tg.AuthSentCode)
		if !ok {
			return model.LoginStep{}, adapter.Errorf(adapter.ErrPlatform, "unexpected sent code %T", sent)
		}
		acc.login.Update(func(f *base.Flow) { f.Data["phone"], f.Data["hash"] = phone, code.PhoneCodeHash })
		return acc.login.SetStep(base.Input(flowPhone, model.LoginField{Name: "code", Type: "code", Label: "Code from Telegram", Pattern: `^[0-9]{4,8}$`})), nil
	case lf.Data["need_password"] == "":
		code := strings.TrimSpace(fields["code"])
		if code == "" {
			return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "code is required")
		}
		_, err := au.SignIn(ctx, lf.Data["phone"], code, lf.Data["hash"])
		if errors.Is(err, auth.ErrPasswordAuthNeeded) {
			acc.login.Update(func(f *base.Flow) { f.Data["need_password"] = "1" })
			return acc.login.SetStep(base.Input(flowPhone, model.LoginField{Name: "password", Type: "password", Label: "Two-step verification password"})), nil
		}
		if err != nil {
			acc.login.Cancel()
			return base.Failed(flowPhone, err.Error()), nil
		}
	default:
		if _, err := au.Password(ctx, fields["password"]); err != nil {
			acc.login.Cancel()
			return base.Failed(flowPhone, err.Error()), nil
		}
	}
	acc.login.Cancel()
	self, err := acc.online(ctx)
	if err != nil {
		return base.Failed(flowPhone, err.Error()), nil
	}
	return model.LoginStep{Flow: flowPhone, Step: model.StepDone, Self: self}, nil
}

// LoginRefresh returns the current step of the login in progress.
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

// LoginCancel cancels the login in progress.
func (a *Adapter) LoginCancel(_ context.Context, accountID string) error {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return err
	}
	acc.login.Cancel()
	return nil
}

// account is one Telegram session.
type account struct {
	id    string
	dir   string
	cfg   config
	rep   base.Reporter
	login base.Login

	mu         sync.Mutex
	cli        *telegram.Client
	dispatcher tg.UpdateDispatcher
	gaps       *updates.Manager
	peers      *peers.Manager
	self       *tg.User
	runCtx     context.Context
	runCancel  context.CancelFunc
	isReady    bool           // Run callback entered (socket up)
	onlineFlag bool           // authorized and updates running
	seen       map[int]string // message id → chat id, for deletions without a peer
	seenOrder  []int
	state      *fileState // updates pts/qts/seq, survives restarts
	peerStore  *filePeers // access hashes, survives restarts
}

func (acc *account) sessionPath() string { return filepath.Join(acc.dir, "session.json") }

func (acc *account) client() *telegram.Client {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	return acc.cli
}

func (acc *account) ready() bool {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	return acc.isReady
}

func (acc *account) isOnline() bool {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	return acc.onlineFlag
}

// start builds a client and runs it until stop.
func (acc *account) start() {
	dispatcher := tg.NewUpdateDispatcher()
	gaps := updates.New(updates.Config{Handler: dispatcher, Storage: acc.state})
	cli := telegram.NewClient(acc.cfg.AppID, acc.cfg.AppHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: acc.sessionPath()},
		UpdateHandler:  gaps,
		Device:         telegram.DeviceConfig{DeviceModel: acc.cfg.DeviceName, SystemVersion: "chat-bridge", AppVersion: "0.1.0"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	acc.mu.Lock()
	acc.cli, acc.dispatcher, acc.gaps, acc.runCtx, acc.runCancel = cli, dispatcher, gaps, ctx, cancel
	acc.peers = peers.Options{Storage: acc.peerStore}.Build(cli.API())
	acc.isReady, acc.onlineFlag, acc.self = false, false, nil
	acc.mu.Unlock()
	acc.wireHandlers()
	acc.rep.Status(adapter.Status{Status: model.StatusConnecting})
	go func() {
		for {
			err := cli.Run(ctx, func(ctx context.Context) error {
				acc.mu.Lock()
				acc.isReady = true
				acc.mu.Unlock()
				st, err := cli.Auth().Status(ctx)
				if err != nil {
					return err
				}
				if st.Authorized {
					if _, err := acc.online(ctx); err != nil {
						acc.rep.Log.Warn("go online", "err", err)
					}
				} else {
					acc.rep.Status(adapter.Status{Status: model.StatusUnpaired})
				}
				<-ctx.Done()
				return ctx.Err()
			})
			acc.mu.Lock()
			acc.isReady, acc.onlineFlag = false, false
			acc.mu.Unlock()
			if ctx.Err() != nil {
				return
			}
			if auth.IsUnauthorized(err) {
				_ = os.Remove(acc.sessionPath())
				acc.rep.Status(adapter.Status{Status: model.StatusUnpaired, Error: &model.Error{Code: "logged_out_remotely", Message: err.Error()}})
			} else {
				acc.rep.Status(adapter.Status{Status: model.StatusDisconnected, Error: &model.Error{Code: "network", Message: fmt.Sprint(err)}})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

func (acc *account) stop() {
	acc.mu.Lock()
	if acc.runCancel != nil {
		acc.runCancel()
	}
	acc.runCancel, acc.onlineFlag, acc.isReady = nil, false, false
	acc.mu.Unlock()
	if acc.state != nil {
		_ = acc.state.Flush()
	}
	if acc.peerStore != nil {
		_ = acc.peerStore.Flush()
	}
}

// online resolves self, starts the updates manager, loads dialogs, and reports connected.
func (acc *account) online(ctx context.Context) (*model.Contact, error) {
	cli := acc.client()
	self, err := cli.Self(ctx)
	if err != nil {
		return nil, base.PlatformErr("self", err)
	}
	acc.mu.Lock()
	acc.self = self
	pm, gaps, runCtx := acc.peers, acc.gaps, acc.runCtx
	already := acc.onlineFlag
	acc.onlineFlag = true
	acc.mu.Unlock()
	if err := pm.Init(ctx); err != nil {
		acc.rep.Log.Warn("peers init", "err", err)
	}
	contact := userContact(self)
	contact.IsSelf, contact.IsContact = true, true
	if already {
		return contact, nil
	}
	go func() {
		if err := gaps.Run(runCtx, cli.API(), self.ID, updates.AuthOptions{OnStart: func(ctx context.Context) {
			acc.rep.Status(adapter.Status{Status: model.StatusConnected, Self: contact,
				Device: rawJSON(map[string]any{"user_id": self.ID, "device_name": acc.cfg.DeviceName})})
			acc.loadDialogs(ctx)
		}}); err != nil && runCtx.Err() == nil {
			acc.rep.Log.Warn("updates manager stopped", "err", err)
		}
	}()
	return contact, nil
}

// startQR exports a login token and keeps refreshing it until scanned.
func (acc *account) startQR(ctx context.Context) (model.LoginStep, error) {
	cli := acc.client()
	qctx, cancel := context.WithCancel(context.Background())
	acc.login.Start(flowQR, cancel)
	q := qrlogin.NewQR(cli.API(), acc.cfg.AppID, acc.cfg.AppHash, qrlogin.Options{})
	loggedIn := qrlogin.OnLoginToken(acc.dispatcher)
	first := make(chan model.LoginStep, 1)
	delivered := false
	go func() { //nolint:gosec // G118: the QR login outlives the request that started it
		_, err := q.Auth(qctx, loggedIn, func(_ context.Context, token qrlogin.Token) error {
			exp := token.Expires()
			step := acc.login.SetStep(base.Display(flowQR, "url", token.URL(), &exp))
			if !delivered {
				delivered = true
				first <- step
				return nil
			}
			acc.rep.Step(step)
			return nil
		})
		if qctx.Err() != nil {
			return
		}
		if errors.Is(err, auth.ErrPasswordAuthNeeded) {
			acc.login.Update(func(f *base.Flow) { f.Data["need_password"] = "1" })
			acc.rep.Step(acc.login.SetStep(base.Input(flowQR, model.LoginField{Name: "password", Type: "password", Label: "Two-step verification password"})))
			return
		}
		if err != nil {
			acc.login.Cancel()
			acc.rep.Step(base.Failed(flowQR, err.Error()))
			return
		}
		acc.login.Cancel()
		self, err := acc.online(context.Background())
		if err != nil {
			acc.rep.Step(base.Failed(flowQR, err.Error()))
			return
		}
		acc.rep.Step(model.LoginStep{Flow: flowQR, Step: model.StepDone, Self: self})
	}()
	select {
	case step := <-first:
		return step, nil
	case <-time.After(20 * time.Second):
		acc.login.Cancel()
		return model.LoginStep{}, adapter.Errorf(adapter.ErrPlatform, "no login token received")
	case <-ctx.Done():
		acc.login.Cancel()
		return model.LoginStep{}, ctx.Err()
	}
}

// --- ids ---

// peerID renders a tg peer as the TDLib-style id used in chat_id.
func peerID(p tg.PeerClass) string {
	var id constant.TDLibPeerID
	switch v := p.(type) {
	case *tg.PeerUser:
		id.User(v.UserID)
	case *tg.PeerChat:
		id.Chat(v.ChatID)
	case *tg.PeerChannel:
		id.Channel(v.ChannelID)
	default:
		return ""
	}
	return strconv.FormatInt(int64(id), 10)
}

func userID(id int64) string {
	var p constant.TDLibPeerID
	p.User(id)
	return strconv.FormatInt(int64(p), 10)
}

func chatIDOf(id int64) string {
	var p constant.TDLibPeerID
	p.Chat(id)
	return strconv.FormatInt(int64(p), 10)
}

func channelID(id int64) string {
	var p constant.TDLibPeerID
	p.Channel(id)
	return strconv.FormatInt(int64(p), 10)
}

// resolvePeer turns a chat id into a peer with a valid access hash.
func (acc *account) resolvePeer(ctx context.Context, chatID string) (peers.Peer, error) {
	n, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return nil, adapter.Errorf(adapter.ErrInvalidTarget, "chat id must be numeric")
	}
	acc.mu.Lock()
	pm := acc.peers
	acc.mu.Unlock()
	id := constant.TDLibPeerID(n)
	switch {
	case id.IsUser():
		return pm.ResolveUserID(ctx, id.ToPlain())
	case id.IsChat():
		return pm.ResolveChatID(ctx, id.ToPlain())
	case id.IsChannel():
		return pm.ResolveChannelID(ctx, id.ToPlain())
	}
	return nil, adapter.Errorf(adapter.ErrInvalidTarget, "unknown peer id %s", chatID)
}

// splitMessageID parses "<chat>:<msg>".
func splitMessageID(s string) (string, int, error) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		n, err := strconv.Atoi(s)
		return "", n, err
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, adapter.Errorf(adapter.ErrInvalidTarget, "bad message id %q", s)
	}
	return s[:i], n, nil
}

func messageID(chatID string, msgID int) string { return chatID + ":" + strconv.Itoa(msgID) }

func (acc *account) remember(msgID int, chatID string) {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if _, ok := acc.seen[msgID]; !ok {
		acc.seenOrder = append(acc.seenOrder, msgID)
		if len(acc.seenOrder) > seenCap {
			delete(acc.seen, acc.seenOrder[0])
			acc.seenOrder = acc.seenOrder[1:]
		}
	}
	acc.seen[msgID] = chatID
}

func userContact(u *tg.User) *model.Contact {
	c := &model.Contact{ID: userID(u.ID), Names: model.Names{First: u.FirstName, Last: u.LastName, Username: u.Username}, IsContact: u.Contact}
	c.Names.Profile = strings.TrimSpace(u.FirstName + " " + u.LastName)
	if u.Phone != "" {
		c.Phone = "+" + u.Phone
	}
	switch {
	case u.Username != "":
		c.Handle = "@" + u.Username
	case c.Phone != "":
		c.Handle = c.Phone
	}
	return c
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
