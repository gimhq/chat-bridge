// Package connector hosts mautrix bridgev2 network connectors as chat-bridge adapters. The
// bridgev2 "Matrix side" is a virtual homeserver (matrix.go) that maps portals to chats, ghosts to
// contacts, and Matrix events to adapter events, so any bridgev2 connector (Signal, Meta, Slack, …)
// runs unmodified behind adapter.Adapter.
package connector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"gopkg.in/yaml.v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	_ "modernc.org/sqlite" // pure-Go driver for the bridgev2 database

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/adapters/matrixcontent"
	"gimhq/chat-bridge/internal/model"
)

const (
	serverName  = "chat-bridge"
	dbFile      = "bridgev2.db"
	sendTimeout = 90 * time.Second
)

// Factory builds a fresh connector instance; bridgev2 binds one connector to one bridge, and the
// host runs one bridge per account.
type Factory func() bridgev2.NetworkConnector

// Adapter is the host.
type Adapter struct {
	log         *slog.Logger
	platform    string
	instance    string
	factory     Factory
	probe       bridgev2.NetworkAPI
	defaults    string // connector YAML overlaid on its example config
	maxFileSize int64  // 0 = no host limit
	sink        adapter.Sink
	accounts    base.Accounts[*account]
}

// Compile-time contract checks: the host is a full adapter with every optional interface; the
// capabilities it advertises depend on the hosted connector.
var (
	_ adapter.Adapter  = (*Adapter)(nil)
	_ adapter.Resolver = (*Adapter)(nil)
	_ adapter.Editor   = (*Adapter)(nil)
	_ adapter.Deleter  = (*Adapter)(nil)
	_ adapter.Reactor  = (*Adapter)(nil)
	_ adapter.Reader   = (*Adapter)(nil)
	_ adapter.Typer    = (*Adapter)(nil)
)

// Option tunes the host.
type Option func(*Adapter)

// WithProbe supplies a zero-value client of the connector so capabilities (edit, react, …) are
// known before any account has logged in; bridgev2 declares them on the per-login client.
func WithProbe(client bridgev2.NetworkAPI) Option {
	return func(a *Adapter) { a.probe = client }
}

// WithInstance names the adapter instance, so a hosted connector can serve a platform next to
// another adapter of it (the core defaults an empty instance to "local").
func WithInstance(instance string) Option {
	return func(a *Adapter) { a.instance = instance }
}

// WithNetworkDefaults overlays server-wide connector YAML on the connector's example config;
// each account's `config.network` still wins.
func WithNetworkDefaults(yamlDoc string) Option {
	return func(a *Adapter) { a.defaults = yamlDoc }
}

// WithMaxFileSize caps media the connector uploads to the virtual homeserver. Without it the host
// imposes no limit and the connector's own capabilities decide.
func WithMaxFileSize(bytes int64) Option {
	return func(a *Adapter) { a.maxFileSize = bytes }
}

// New returns a host for connectors produced by factory, published as platform.
func New(log *slog.Logger, platform string, factory Factory, opts ...Option) *Adapter {
	a := &Adapter{log: log, platform: platform, factory: factory}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Info derives login flows from a fresh connector and capabilities from the probe client and
// the connected logins' optional interfaces.
func (a *Adapter) Info() adapter.Info {
	probe := a.factory()
	name := probe.GetName()
	flows := make([]model.LoginFlow, 0)
	for _, f := range probe.GetLoginFlows() {
		flows = append(flows, model.LoginFlow{ID: f.ID, Name: f.Name})
	}
	caps := []string{adapter.CapSendText, adapter.CapSendMedia, adapter.CapSendLocation, adapter.CapReply, adapter.CapChatMembers}
	if a.probe != nil {
		caps = append(caps, capsOf(a.probe)...)
	}
	a.accounts.Each(func(_ string, acc *account) {
		if cl := acc.client(); cl != nil {
			caps = append(caps, capsOf(cl)...)
		}
	})
	return adapter.Info{Platform: a.platform, Instance: a.instance, Name: name.DisplayName + " (mautrix bridgev2)", Version: "bridgev2",
		Capabilities: dedupe(caps), LoginFlows: flows,
		ConfigSchema: json.RawMessage(`{"type":"object","properties":{"network":{"type":"string","description":"connector YAML config, see the connector's example"}}}`)}
}

func capsOf(cl bridgev2.NetworkAPI) []string {
	var out []string
	if _, ok := cl.(bridgev2.EditHandlingNetworkAPI); ok {
		out = append(out, adapter.CapEdit)
	}
	if _, ok := cl.(bridgev2.RedactionHandlingNetworkAPI); ok {
		out = append(out, adapter.CapDelete)
	}
	if _, ok := cl.(bridgev2.ReactionHandlingNetworkAPI); ok {
		out = append(out, adapter.CapReaction)
	}
	if _, ok := cl.(bridgev2.ReadReceiptHandlingNetworkAPI); ok {
		out = append(out, adapter.CapChatRead, adapter.CapReceipts)
	}
	if _, ok := cl.(bridgev2.TypingHandlingNetworkAPI); ok {
		out = append(out, adapter.CapChatTyping)
	}
	if _, ok := cl.(bridgev2.IdentifierResolvingNetworkAPI); ok {
		out = append(out, adapter.CapChatResolve)
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Start records the sink; accounts are added afterwards.
func (a *Adapter) Start(_ context.Context, sink adapter.Sink) error { a.sink = sink; return nil }

// Stop stops the bridge of every account.
func (a *Adapter) Stop(context.Context) error {
	a.accounts.Each(func(_ string, acc *account) { acc.stop() })
	return nil
}

// AddAccount opens the account's bridgev2 database, starts a bridge with a fresh connector, and
// reconnects any stored login.
func (a *Adapter) AddAccount(ctx context.Context, accountID string, cfg json.RawMessage, dataDir string) error {
	if acc, err := a.accounts.Get(accountID); err == nil {
		acc.cfg = cfg
		return nil
	}
	acc := &account{id: accountID, dir: dataDir, cfg: cfg, host: a,
		rep:     base.Reporter{Sink: a.sink, ID: accountID, Log: a.log.With("account", accountID)},
		pending: map[id.EventID]chan sendOutcome{}, uploads: map[string]adapter.MediaSource{}, stored: map[string]model.Attachment{}}
	if err := acc.start(ctx); err != nil {
		return err
	}
	a.accounts.Put(accountID, acc)
	return nil
}

// RemoveAccount logs the account out on the network, stops its bridge and deletes its directory.
func (a *Adapter) RemoveAccount(ctx context.Context, accountID string) error {
	acc, ok := a.accounts.Delete(accountID)
	if !ok {
		return nil
	}
	if ul := acc.userLogin(); ul != nil {
		ul.Logout(ctx)
	}
	acc.stop()
	return os.RemoveAll(acc.dir)
}

// Reconnect drops the network connection of a logged-in account and opens it again.
func (a *Adapter) Reconnect(_ context.Context, accountID string) error {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return err
	}
	ul := acc.userLogin()
	if ul == nil || ul.Client == nil {
		return adapter.Errorf(adapter.ErrNotConnected, "account is not logged in")
	}
	ul.Disconnect()
	ul.Client.Connect(acc.ctx) // the bridge context, not the request's: the connection outlives it
	return nil
}

// Logout cancels a login in progress and logs the stored login out on the network.
func (a *Adapter) Logout(ctx context.Context, accountID string) error {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return err
	}
	acc.login.Cancel()
	if ul := acc.userLogin(); ul != nil {
		ul.Logout(ctx)
		acc.setLogin(nil)
	}
	return nil
}

// --- login ---

// LoginStart creates the connector's login process for the flow and returns its first step.
func (a *Adapter) LoginStart(ctx context.Context, accountID, flow string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return model.LoginStep{}, err
	}
	if acc.userLogin() != nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "already logged in; logout first")
	}
	proc, err := acc.bridge.Network.CreateLogin(ctx, acc.user, flow)
	if err != nil {
		return model.LoginStep{}, base.PlatformErr("create login", err)
	}
	acc.login.Start(flow, proc.Cancel)
	acc.setProc(proc)
	step, err := proc.Start(ctx)
	if err != nil {
		acc.login.Cancel()
		return model.LoginStep{}, base.PlatformErr("login start", err)
	}
	return acc.applyStep(ctx, step)
}

// LoginSubmit passes the fields to the login process, which must be waiting for user input.
func (a *Adapter) LoginSubmit(ctx context.Context, accountID string, fields map[string]string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return model.LoginStep{}, err
	}
	proc := acc.currentProc()
	if proc == nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "no login in progress")
	}
	ui, ok := proc.(bridgev2.LoginProcessUserInput)
	if !ok {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "this login step takes no input")
	}
	step, err := ui.SubmitUserInput(ctx, fields)
	if err != nil {
		acc.login.Cancel()
		return base.Failed(acc.flowName(), err.Error()), nil
	}
	return acc.applyStep(ctx, step)
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
	acc.setProc(nil)
	return nil
}

// --- chats and messages ---

func (a *Adapter) online(accountID string) (*account, *bridgev2.UserLogin, error) {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return nil, nil, err
	}
	ul := acc.userLogin()
	if ul == nil || ul.Client == nil {
		return nil, nil, adapter.Errorf(adapter.ErrNotConnected, "account is not logged in")
	}
	return acc, ul, nil
}

// GetChat asks the connector for the chat's current info.
func (a *Adapter) GetChat(ctx context.Context, accountID, chatID string) (model.Chat, error) {
	acc, ul, err := a.online(accountID)
	if err != nil {
		return model.Chat{}, err
	}
	portal, err := acc.portalFor(ctx, chatID)
	if err != nil {
		return model.Chat{}, err
	}
	info, err := ul.Client.GetChatInfo(ctx, portal)
	if err != nil {
		return model.Chat{}, base.PlatformErr("chat info", err)
	}
	ch := acc.chatFromPortal(portal)
	if info.Name != nil {
		ch.Name = *info.Name
	}
	if info.Type != nil {
		ch.Kind = kindOf(*info.Type)
	}
	if info.Members != nil {
		for _, m := range chatMembers(info.Members) {
			if m.Membership != "" && m.Membership != event.MembershipJoin {
				continue
			}
			p := model.Participant{ID: string(m.Sender), Role: "member"}
			if m.PowerLevel != nil && *m.PowerLevel >= 50 {
				p.Role = "admin"
			}
			if m.UserInfo != nil && m.UserInfo.Name != nil {
				p.Name = *m.UserInfo.Name
			} else if g, err := acc.bridge.GetGhostByID(ctx, m.Sender); err == nil {
				p.Name = g.Name
			}
			ch.Participants = append(ch.Participants, p)
		}
	}
	return ch, nil
}

// ListContacts returns the connector's contact list, or nothing when it has none.
func (a *Adapter) ListContacts(ctx context.Context, accountID string) ([]model.Contact, error) {
	_, ul, err := a.online(accountID)
	if err != nil {
		return nil, err
	}
	cl, ok := ul.Client.(bridgev2.ContactListingNetworkAPI)
	if !ok {
		return nil, nil
	}
	list, err := cl.GetContactList(ctx)
	if err != nil {
		return nil, base.PlatformErr("contacts", err)
	}
	out := make([]model.Contact, 0, len(list))
	for _, r := range list {
		c := model.Contact{ID: string(r.UserID), IsContact: true}
		if r.UserInfo != nil {
			c = contactFromInfo(r.UserID, r.UserInfo)
			c.IsContact = true
		} else if r.Ghost != nil {
			c.Names.Profile = r.Ghost.Name
		}
		out = append(out, c)
	}
	return out, nil
}

// ResolveChat resolves a phone number or username through the connector, creating the chat if
// needed.
func (a *Adapter) ResolveChat(ctx context.Context, accountID, handle string) (model.ResolvedChat, error) {
	acc, ul, err := a.online(accountID)
	if err != nil {
		return model.ResolvedChat{}, err
	}
	r, ok := ul.Client.(bridgev2.IdentifierResolvingNetworkAPI)
	if !ok {
		return model.ResolvedChat{}, adapter.Errorf(adapter.ErrUnsupported, "connector cannot resolve identifiers")
	}
	res, err := r.ResolveIdentifier(ctx, handle, true)
	if err != nil {
		return model.ResolvedChat{}, adapter.Errorf(adapter.ErrInvalidTarget, "%s: %v", handle, err)
	}
	if res == nil || res.Chat == nil {
		return model.ResolvedChat{}, adapter.Errorf(adapter.ErrInvalidTarget, "%s not found", handle)
	}
	if res.Chat.Portal != nil && res.Chat.Portal.MXID == "" && res.Chat.PortalInfo != nil {
		_ = res.Chat.Portal.CreateMatrixRoom(ctx, ul, res.Chat.PortalInfo)
	}
	out := model.ResolvedChat{ChatID: string(res.Chat.PortalKey.ID), Kind: model.ChatDirect, UserID: string(res.UserID)}
	if acc != nil && res.Chat.PortalInfo != nil && res.Chat.PortalInfo.Type != nil {
		out.Kind = kindOf(*res.Chat.PortalInfo.Type)
	}
	return out, nil
}

// SendMessage injects a Matrix event for the account's user and waits for the connector's status.
func (a *Adapter) SendMessage(ctx context.Context, accountID string, req adapter.SendRequest) (model.Message, error) {
	acc, _, err := a.online(accountID)
	if err != nil {
		return model.Message{}, err
	}
	var mediaURL string
	var meta *model.Attachment
	if len(req.Content.Attachments) > 0 {
		att := req.Content.Attachments[0]
		acc.mu.Lock()
		acc.uploads[att.MediaID] = req.Media
		acc.mu.Unlock()
		defer func() {
			acc.mu.Lock()
			delete(acc.uploads, att.MediaID)
			acc.mu.Unlock()
		}()
		mediaURL, meta = "mxc://"+serverName+"/"+att.MediaID, &att
	}
	content, evType := matrixcontent.Build(req.Content, mediaURL, meta)
	if req.ReplyTo != "" {
		if mxid, ok := acc.mxidOf(ctx, req.ReplyTo); ok {
			content.RelatesTo = &event.RelatesTo{InReplyTo: &event.InReplyTo{EventID: mxid}}
		}
	}
	msg, err := acc.sendEvent(ctx, req.ChatID, evType, content)
	if err != nil {
		return model.Message{}, err
	}
	msg.Content = req.Content
	return msg, nil
}

// EditMessage sends an m.replace event for the connector to apply.
func (a *Adapter) EditMessage(ctx context.Context, accountID, chatID, msgID string, c model.Content) (model.Message, error) {
	acc, ul, err := a.online(accountID)
	if err != nil {
		return model.Message{}, err
	}
	if _, ok := ul.Client.(bridgev2.EditHandlingNetworkAPI); !ok {
		return model.Message{}, adapter.Errorf(adapter.ErrUnsupported, "connector cannot edit messages")
	}
	target, ok := acc.mxidOf(ctx, msgID)
	if !ok {
		return model.Message{}, adapter.Errorf(adapter.ErrInvalidTarget, "unknown message %s", msgID)
	}
	newContent, _ := matrixcontent.Build(c, "", nil)
	edit := &event.MessageEventContent{MsgType: event.MsgText, Body: "* " + newContent.Body, NewContent: newContent,
		RelatesTo: &event.RelatesTo{Type: event.RelReplace, EventID: target}}
	if _, err := acc.sendEvent(ctx, chatID, event.EventMessage, edit); err != nil {
		return model.Message{}, err
	}
	return model.Message{ID: msgID, ChatID: chatID, Content: c}, nil
}

// DeleteMessage sends a redaction for the connector to apply.
func (a *Adapter) DeleteMessage(ctx context.Context, accountID, chatID, msgID, _ string) error {
	acc, ul, err := a.online(accountID)
	if err != nil {
		return err
	}
	if _, ok := ul.Client.(bridgev2.RedactionHandlingNetworkAPI); !ok {
		return adapter.Errorf(adapter.ErrUnsupported, "connector cannot delete messages")
	}
	target, ok := acc.mxidOf(ctx, msgID)
	if !ok {
		return adapter.Errorf(adapter.ErrInvalidTarget, "unknown message %s", msgID)
	}
	_, err = acc.sendRaw(ctx, chatID, event.EventRedaction, &event.RedactionEventContent{Redacts: target}, target)
	return err
}

// React sends an annotation, or redacts our earlier one when removing.
func (a *Adapter) React(ctx context.Context, accountID, chatID, msgID, _, emoji string, remove bool) error {
	acc, ul, err := a.online(accountID)
	if err != nil {
		return err
	}
	if _, ok := ul.Client.(bridgev2.ReactionHandlingNetworkAPI); !ok {
		return adapter.Errorf(adapter.ErrUnsupported, "connector cannot react")
	}
	target, ok := acc.mxidOf(ctx, msgID)
	if !ok {
		return adapter.Errorf(adapter.ErrInvalidTarget, "unknown message %s", msgID)
	}
	if remove {
		own, err := acc.bridge.DB.Reaction.GetByID(ctx, ul.ID, networkid.MessageID(msgID), "", acc.selfID(ul), networkid.EmojiID(emoji))
		if err != nil || own == nil {
			return adapter.Errorf(adapter.ErrInvalidTarget, "no own reaction %s on %s", emoji, msgID)
		}
		_, err = acc.sendRaw(ctx, chatID, event.EventRedaction, &event.RedactionEventContent{Redacts: own.MXID}, own.MXID)
		return err
	}
	_, err = acc.sendRaw(ctx, chatID, event.EventReaction, &event.ReactionEventContent{RelatesTo: event.RelatesTo{Type: event.RelAnnotation, EventID: target, Key: emoji}}, "")
	return err
}

// MarkRead sends a read receipt up to the last id; connectors without receipts ignore it.
func (a *Adapter) MarkRead(ctx context.Context, accountID, chatID string, ids []string, _ string) error {
	acc, ul, err := a.online(accountID)
	if err != nil {
		return err
	}
	if _, ok := ul.Client.(bridgev2.ReadReceiptHandlingNetworkAPI); !ok || len(ids) == 0 {
		return nil
	}
	target, ok := acc.mxidOf(ctx, ids[len(ids)-1])
	if !ok {
		return nil
	}
	portal, err := acc.portalFor(ctx, chatID)
	if err != nil {
		return err
	}
	rc := event.ReceiptEventContent{}
	rc.Set(target, event.ReceiptTypeRead, acc.user.MXID, event.ReadReceipt{Timestamp: time.Now()})
	evt := &event.Event{Type: event.EphemeralEventReceipt, RoomID: portal.MXID, Content: event.Content{Parsed: &rc}}
	evt.Mautrix.EventSource = event.SourceEphemeral
	acc.bridge.QueueMatrixEvent(ctx, evt)
	return nil
}

// Typing sends a typing notification; connectors without typing ignore it.
func (a *Adapter) Typing(ctx context.Context, accountID, chatID, state string) error {
	acc, ul, err := a.online(accountID)
	if err != nil {
		return err
	}
	if _, ok := ul.Client.(bridgev2.TypingHandlingNetworkAPI); !ok {
		return nil
	}
	portal, err := acc.portalFor(ctx, chatID)
	if err != nil {
		return err
	}
	tc := event.TypingEventContent{}
	if state == "typing" {
		tc.UserIDs = []id.UserID{acc.user.MXID}
	}
	evt := &event.Event{Type: event.EphemeralEventTyping, RoomID: portal.MXID, Content: event.Content{Parsed: &tc}}
	evt.Mautrix.EventSource = event.SourceEphemeral
	acc.bridge.QueueMatrixEvent(ctx, evt)
	return nil
}

// FetchMedia is never needed: the connector downloads attachments during conversion and the
// virtual homeserver stores them through the sink at once.
func (a *Adapter) FetchMedia(context.Context, string, string, json.RawMessage, io.Writer) (adapter.MediaMeta, error) {
	return adapter.MediaMeta{}, adapter.Errorf(adapter.ErrUnsupported, "hosted connectors store media eagerly")
}

// --- account ---

type sendOutcome struct {
	ok  bool
	err error
}

type account struct {
	id, dir string
	cfg     json.RawMessage
	host    *Adapter
	rep     base.Reporter
	login   base.Login

	bridge *bridgev2.Bridge
	db     *dbutil.Database
	raw    *sql.DB
	user   *bridgev2.User

	mu       sync.Mutex
	ul       *bridgev2.UserLogin
	reported bool // a bridge state reached the sink (guards the post-start fallback)
	proc     bridgev2.LoginProcess
	pending  map[id.EventID]chan sendOutcome
	uploads  map[string]adapter.MediaSource // outbound attachments the connector may download
	stored   map[string]model.Attachment    // inbound attachments already written through the sink
	ctx      context.Context                // bridge lifetime
	cancel   context.CancelFunc
}

// uploadLimit is handed to connectors that size-check attachments (mautrix takes it from the
// homeserver's media config); without WithMaxFileSize it is unbounded.
func (a *Adapter) uploadLimit() int64 {
	if a.maxFileSize > 0 {
		return a.maxFileSize
	}
	return math.MaxInt64
}

// checkSize rejects media above WithMaxFileSize the way a homeserver rejects oversized uploads.
func (a *Adapter) checkSize(n int64) error {
	if a.maxFileSize > 0 && n > a.maxFileSize {
		return fmt.Errorf("%w (%d > %d bytes)", bridgev2.ErrMediaTooLarge, n, a.maxFileSize)
	}
	return nil
}

func (acc *account) start(ctx context.Context) error {
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(acc.dir, dbFile)+"?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("open bridgev2 db: %w", err)
	}
	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		_ = raw.Close()
		return err
	}
	zl := zerolog.New(os.Stderr).Level(zerolog.WarnLevel).With().Str("adapter", acc.host.platform).Str("account", acc.id).Logger()
	cfg := &bridgeconfig.BridgeConfig{
		CommandPrefix:         "!" + serverName,
		PrivateChatPortalMeta: true,
		Permissions:           bridgeconfig.PermissionConfig{"*": {SendEvents: true, Commands: true, Login: true, Admin: true}},
	}
	net := acc.host.factory()
	if err := loadNetworkConfig(net, acc.host.defaults, acc.cfg); err != nil {
		_ = raw.Close()
		return err
	}
	// Connectors may type-assert the command processor to mautrix's own, so hand them the real one.
	acc.bridge = bridgev2.NewBridge(networkid.BridgeID(serverName+"-"+acc.id), db, zl, cfg, &virtualMatrix{acc: acc}, net, commands.NewProcessor)
	acc.db, acc.raw = db, raw
	acc.ctx, acc.cancel = context.WithCancel(context.Background())
	if m, ok := net.(bridgev2.MaxFileSizeingNetwork); ok {
		m.SetMaxFileSize(acc.host.uploadLimit())
	}
	if err := acc.bridge.Start(acc.ctx); err != nil {
		acc.cancel()
		_ = raw.Close()
		return fmt.Errorf("start bridge: %w", err)
	}
	acc.user, err = acc.bridge.GetUserByMXID(ctx, id.NewUserID(acc.id, serverName))
	if err != nil {
		return fmt.Errorf("bridge user: %w", err)
	}
	if logins := acc.user.GetUserLogins(); len(logins) > 0 {
		ul := logins[0]
		// StartLogins already connected the client and its bridge state may have been reported
		// (bridgeStatus resolves the login by id, so Self was attached). Only fill the gap when
		// nothing was reported yet; never overwrite a state the queue delivered meanwhile.
		acc.mu.Lock()
		acc.ul = ul
		silent := !acc.reported
		acc.mu.Unlock()
		if silent {
			acc.rep.Status(adapter.Status{Status: model.StatusConnecting, Self: acc.selfContact(ul)})
		}
		return nil
	}
	acc.rep.Status(adapter.Status{Status: model.StatusUnpaired})
	return nil
}

func (acc *account) stop() {
	acc.login.Cancel()
	if acc.cancel != nil {
		acc.cancel()
	}
	if acc.bridge != nil {
		acc.bridge.Stop()
	}
	if acc.raw != nil {
		_ = acc.raw.Close()
	}
}

func (acc *account) userLogin() *bridgev2.UserLogin {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	return acc.ul
}

func (acc *account) setLogin(ul *bridgev2.UserLogin) {
	acc.mu.Lock()
	acc.ul = ul
	acc.mu.Unlock()
}

func (acc *account) client() bridgev2.NetworkAPI {
	if ul := acc.userLogin(); ul != nil {
		return ul.Client
	}
	return nil
}

func (acc *account) setProc(p bridgev2.LoginProcess) {
	acc.mu.Lock()
	acc.proc = p
	acc.mu.Unlock()
}

func (acc *account) currentProc() bridgev2.LoginProcess {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	return acc.proc
}

func (acc *account) flowName() string {
	if lf := acc.login.Current(); lf != nil {
		return lf.Name
	}
	return ""
}

func (acc *account) selfID(ul *bridgev2.UserLogin) networkid.UserID {
	if ul != nil && ul.Client != nil {
		if w, ok := ul.Client.(bridgev2.NetworkAPIWithUserID); ok {
			return w.GetUserID()
		}
	}
	if ul != nil {
		return networkid.UserID(ul.ID)
	}
	return ""
}

func (acc *account) selfContact(ul *bridgev2.UserLogin) *model.Contact {
	if ul == nil {
		return nil
	}
	p := ul.RemoteProfile
	c := &model.Contact{ID: string(acc.selfID(ul)), Name: p.Name, Handle: ul.RemoteName, Phone: p.Phone, Email: p.Email,
		Names: model.Names{Profile: p.Name, Username: p.Username}, IsSelf: true, IsContact: true}
	if c.Handle == "" {
		c.Handle = c.ID
	}
	return c
}

// applyStep converts a bridgev2 login step and drives display_and_wait steps to completion.
func (acc *account) applyStep(_ context.Context, step *bridgev2.LoginStep) (model.LoginStep, error) {
	flow := acc.flowName()
	switch step.Type {
	case bridgev2.LoginStepTypeUserInput:
		fields := make([]model.LoginField, 0, len(step.UserInputParams.Fields))
		for _, f := range step.UserInputParams.Fields {
			fields = append(fields, model.LoginField{Name: f.ID, Type: fieldType(f.Type), Label: f.Name, Pattern: f.Pattern})
		}
		return acc.login.SetStep(base.Input(flow, fields...)), nil
	case bridgev2.LoginStepTypeDisplayAndWait:
		p := step.DisplayAndWaitParams
		typ := "text"
		switch p.Type {
		case bridgev2.LoginDisplayTypeQR:
			typ = "qr"
		case bridgev2.LoginDisplayTypeCode:
			typ = "code"
		}
		out := acc.login.SetStep(base.Display(flow, typ, p.Data, nil))
		if w, ok := acc.currentProc().(bridgev2.LoginProcessDisplayAndWait); ok {
			go acc.waitStep(w)
		}
		return out, nil
	case bridgev2.LoginStepTypeComplete:
		ul := step.CompleteParams.UserLogin
		if ul == nil {
			ul = acc.bridge.GetCachedUserLoginByID(step.CompleteParams.UserLoginID)
		}
		acc.login.Cancel()
		acc.setProc(nil)
		acc.setLogin(ul)
		return model.LoginStep{Flow: flow, Step: model.StepDone, Self: acc.selfContact(ul)}, nil
	default:
		acc.login.Cancel()
		acc.setProc(nil)
		return base.Failed(flow, "login step type "+string(step.Type)+" is not supported by this bridge"), nil
	}
}

func (acc *account) waitStep(w bridgev2.LoginProcessDisplayAndWait) {
	ctx, cancel := base.Timeout(10 * time.Minute)
	defer cancel()
	next, err := w.Wait(ctx)
	if acc.login.Current() == nil {
		return // cancelled meanwhile
	}
	if err != nil {
		acc.login.Cancel()
		acc.setProc(nil)
		acc.rep.Step(base.Failed(acc.flowName(), err.Error()))
		return
	}
	step, _ := acc.applyStep(ctx, next)
	acc.rep.Step(step)
}

func fieldType(t bridgev2.LoginInputFieldType) string {
	switch t {
	case bridgev2.LoginInputFieldTypePhoneNumber:
		return "phone"
	case bridgev2.LoginInputFieldTypePassword, bridgev2.LoginInputFieldTypeToken:
		return "password"
	case bridgev2.LoginInputFieldType2FACode:
		return "code"
	case bridgev2.LoginInputFieldTypeURL:
		return "url"
	}
	return "text"
}

// portalFor finds the portal for a chat id, trying the login-scoped key first.
func (acc *account) portalFor(ctx context.Context, chatID string) (*bridgev2.Portal, error) {
	keys := []networkid.PortalKey{{ID: networkid.PortalID(chatID)}}
	if ul := acc.userLogin(); ul != nil {
		keys = append([]networkid.PortalKey{{ID: networkid.PortalID(chatID), Receiver: ul.ID}}, keys...)
	}
	for _, key := range keys {
		p, err := acc.bridge.GetExistingPortalByKey(ctx, key)
		if err != nil {
			return nil, err
		}
		if p != nil {
			return p, nil
		}
	}
	return nil, adapter.Errorf(adapter.ErrInvalidTarget, "unknown chat %s", chatID)
}

func (acc *account) chatFromPortal(p *bridgev2.Portal) model.Chat {
	return model.Chat{ID: string(p.ID), Kind: kindOf(p.RoomType), Name: p.Name}
}

func kindOf(t database.RoomType) string {
	switch t {
	case database.RoomTypeDM:
		return model.ChatDirect
	case database.RoomTypeSpace:
		return model.ChatGroup
	}
	return model.ChatGroup
}

// mxidOf maps our message id (the network id) to the Matrix event id bridgev2 stored.
func (acc *account) mxidOf(ctx context.Context, msgID string) (id.EventID, bool) {
	ul := acc.userLogin()
	if ul == nil {
		return "", false
	}
	m, err := acc.bridge.DB.Message.GetFirstPartByID(ctx, ul.ID, networkid.MessageID(msgID))
	if err != nil || m == nil {
		return "", false
	}
	return m.MXID, true
}

// sendEvent queues a message event and returns the stored message once the connector accepted it.
func (acc *account) sendEvent(ctx context.Context, chatID string, evType event.Type, content *event.MessageEventContent) (model.Message, error) {
	evtID, err := acc.sendRaw(ctx, chatID, evType, content, "")
	if err != nil {
		return model.Message{}, err
	}
	m, err := acc.bridge.DB.Message.GetPartByMXID(ctx, evtID)
	if err != nil || m == nil {
		return model.Message{}, adapter.Errorf(adapter.ErrPlatform, "message accepted but not recorded")
	}
	return model.Message{ID: string(m.ID), ChatID: chatID, Timestamp: m.Timestamp.UTC(), Status: model.MsgSent}, nil
}

// sendRaw injects one Matrix event from the account's user and waits for the send status.
func (acc *account) sendRaw(ctx context.Context, chatID string, evType event.Type, content any, redacts id.EventID) (id.EventID, error) {
	portal, err := acc.portalFor(ctx, chatID)
	if err != nil {
		return "", err
	}
	if portal.MXID == "" {
		return "", adapter.Errorf(adapter.ErrInvalidTarget, "chat %s has no room yet", chatID)
	}
	evtID := id.EventID("$out-" + uuid.NewString())
	evt := &event.Event{ID: evtID, RoomID: portal.MXID, Sender: acc.user.MXID, Type: evType, Timestamp: time.Now().UnixMilli(),
		Content: event.Content{Parsed: content}, Redacts: redacts}
	ch := make(chan sendOutcome, 1)
	acc.mu.Lock()
	acc.pending[evtID] = ch
	acc.mu.Unlock()
	defer func() {
		acc.mu.Lock()
		delete(acc.pending, evtID)
		acc.mu.Unlock()
	}()
	res := acc.bridge.QueueMatrixEvent(ctx, evt)
	if !res.Success && !res.Queued {
		if res.Error != nil {
			return "", base.PlatformErr("send", res.Error)
		}
		return "", adapter.Errorf(adapter.ErrPlatform, "send rejected by bridge")
	}
	select {
	case out := <-ch:
		if !out.ok {
			return "", base.PlatformErr("send", out.err)
		}
		return evtID, nil
	case <-time.After(sendTimeout):
		return "", adapter.Errorf(adapter.ErrPlatform, "send timed out")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// resolveSend completes a pending send from SendMessageStatus.
func (acc *account) resolveSend(evtID id.EventID, ok bool, err error) {
	acc.mu.Lock()
	ch := acc.pending[evtID]
	acc.mu.Unlock()
	if ch != nil {
		select {
		case ch <- sendOutcome{ok: ok, err: err}:
		default:
		}
	}
}

// bridgeStatus maps bridgev2 state events onto account statuses.
func (acc *account) bridgeStatus(st *status.BridgeState) {
	acc.mu.Lock()
	acc.reported = true
	if acc.ul == nil && st.RemoteID != "" { // states sent while StartLogins runs, before start() saw the login
		acc.ul = acc.bridge.GetCachedUserLoginByID(networkid.UserLoginID(st.RemoteID))
	}
	acc.mu.Unlock()
	out := adapter.Status{}
	switch st.StateEvent {
	case status.StateConnected:
		out.Status = model.StatusConnected
	case status.StateConnecting, status.StateStarting:
		out.Status = model.StatusConnecting
	case status.StateTransientDisconnect:
		out.Status = model.StatusDisconnected
		out.Error = &model.Error{Code: "network", Message: st.Message}
	case status.StateBadCredentials, status.StateLoggedOut:
		out.Status = model.StatusUnpaired
		out.Error = &model.Error{Code: "logged_out_remotely", Message: string(st.Error) + " " + st.Message}
		acc.setLogin(nil)
	case status.StateUnknownError:
		out.Status = model.StatusError
		out.Error = &model.Error{Code: "platform_error", Message: string(st.Error) + " " + st.Message}
	default:
		return
	}
	if ul := acc.userLogin(); ul != nil && out.Status != model.StatusUnpaired {
		out.Self = acc.selfContact(ul)
	}
	acc.rep.Status(out)
}

func contactFromInfo(uid networkid.UserID, info *bridgev2.UserInfo) model.Contact {
	c := model.Contact{ID: string(uid)}
	if info.Name != nil {
		c.Names.Profile = *info.Name
	}
	for _, ident := range info.Identifiers {
		switch {
		case len(ident) > 4 && ident[:4] == "tel:":
			c.Phone, c.Handle = ident[4:], ident[4:]
		case len(ident) > 7 && ident[:7] == "mailto:":
			c.Email = ident[7:]
		}
	}
	if c.Handle == "" {
		c.Handle = c.ID
	}
	return c
}

// loadNetworkConfig fills the connector's config struct from its example YAML, overlays the
// host's defaults, then the account's `network` field (a YAML string or a JSON object with the
// same keys).
func loadNetworkConfig(net bridgev2.NetworkConnector, defaults string, accountCfg json.RawMessage) error {
	example, target, _ := net.GetConfig()
	if target == nil {
		return nil
	}
	if err := yaml.Unmarshal([]byte(example), target); err != nil {
		return fmt.Errorf("connector example config: %w", err)
	}
	if err := yaml.Unmarshal([]byte(defaults), target); err != nil {
		return fmt.Errorf("connector default config: %w", err)
	}
	if len(accountCfg) == 0 {
		return nil
	}
	var wrapper struct {
		Network json.RawMessage `json:"network"`
	}
	if err := json.Unmarshal(accountCfg, &wrapper); err != nil || len(wrapper.Network) == 0 {
		return nil
	}
	doc := wrapper.Network
	var str string
	if json.Unmarshal(wrapper.Network, &str) == nil {
		doc = []byte(str)
	}
	if err := yaml.Unmarshal(doc, target); err != nil {
		return adapter.Errorf(adapter.ErrInvalidInput, "network config: %v", err)
	}
	return nil
}

var errNotImplemented = errors.New("not implemented by the virtual homeserver")

// chatMembers returns the members a connector reported, in a stable order. Connectors fill
// MemberMap; older ones still fill the list.
func chatMembers(l *bridgev2.ChatMemberList) []bridgev2.ChatMember {
	if l.MemberMap == nil {
		return l.Members //nolint:staticcheck // SA1019: connectors that predate MemberMap only fill the list
	}
	out := make([]bridgev2.ChatMember, 0, len(l.MemberMap))
	for _, m := range l.MemberMap {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sender < out[j].Sender })
	return out
}
