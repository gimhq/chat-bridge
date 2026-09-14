// Package whatsapp is the WhatsApp adapter, built on whatsmeow (linked-device protocol).
package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

const (
	platform    = "whatsapp"
	loginFlowQR = "qr"
	loginPhone  = "phone"
	qrWait      = 20 * time.Second
)

// Adapter hosts one whatsmeow client per account.
type Adapter struct {
	log      *slog.Logger
	sink     adapter.Sink
	accounts base.Accounts[*account]
}

type config struct {
	DeviceName string `json:"device_name"`
}

// New returns an unstarted adapter.
func New(log *slog.Logger) *Adapter { return &Adapter{log: log} }

// Info declares the WhatsApp capabilities.
func (a *Adapter) Info() adapter.Info {
	return adapter.Info{
		Platform: platform, Name: "WhatsApp (whatsmeow)", Version: "0.1.0",
		Capabilities: []string{
			adapter.CapSendText, adapter.CapSendMedia, adapter.CapSendLocation, adapter.CapSendContact,
			adapter.CapReply, adapter.CapEdit, adapter.CapDelete, adapter.CapReaction,
			adapter.CapChatRead, adapter.CapChatTyping, adapter.CapChatResolve, adapter.CapChatMembers,
			adapter.CapPresence, adapter.CapReceipts, adapter.CapMarkdown,
			adapter.CapChatCreate, adapter.CapSelfUpdate,
		},
		LoginFlows: []model.LoginFlow{
			{ID: loginFlowQR, Name: "Scan QR from WhatsApp > Linked devices"},
			{ID: loginPhone, Name: "Phone number pairing code"},
		},
		ConfigSchema: json.RawMessage(`{"type":"object","properties":{"device_name":{"type":"string","default":"chat-bridge"}}}`),
	}
}

// Start records the sink; accounts are added afterwards.
func (a *Adapter) Start(_ context.Context, sink adapter.Sink) error {
	a.sink = sink
	return nil
}

// Stop disconnects every account.
func (a *Adapter) Stop(_ context.Context) error {
	a.accounts.Each(func(_ string, acc *account) {
		acc.cli.Disconnect()
		_ = acc.container.Close()
	})
	return nil
}

// AddAccount opens (or reuses) the device store and connects if the device is paired.
func (a *Adapter) AddAccount(ctx context.Context, id string, cfg json.RawMessage, dataDir string) error {
	var c config
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &c); err != nil {
			return adapter.Errorf(adapter.ErrInvalidInput, "config: %v", err)
		}
	}
	if c.DeviceName == "" {
		c.DeviceName = "chat-bridge"
	}
	if acc, err := a.accounts.Get(id); err == nil {
		acc.cfg = c
		return nil
	}

	rep := base.Reporter{Sink: a.sink, ID: id, Log: a.log.With("account", id)}
	dsn := "file:" + filepath.Join(dataDir, "whatsmeow.db") + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	container, err := sqlstore.New(ctx, "sqlite", dsn, newWALogger(rep.Log.With("component", "wa-store")))
	if err != nil {
		return fmt.Errorf("open device store: %w", err)
	}
	acc := &account{id: id, dir: dataDir, cfg: c, rep: rep, container: container}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return fmt.Errorf("get device: %w", err)
	}
	acc.attach(device)
	a.accounts.Put(id, acc)

	if acc.cli.Store.ID != nil {
		acc.setStatus(model.StatusConnecting, nil)
		go acc.connect()
	} else {
		acc.setStatus(model.StatusUnpaired, nil)
	}
	return nil
}

// RemoveAccount logs out, closes the store, and deletes the account's files.
func (a *Adapter) RemoveAccount(ctx context.Context, id string) error {
	acc, ok := a.accounts.Delete(id)
	if !ok {
		return nil
	}
	if acc.cli.IsLoggedIn() {
		_ = acc.cli.Logout(ctx)
	}
	acc.cli.Disconnect()
	_ = acc.container.Close()
	return os.RemoveAll(acc.dir)
}

// Reconnect drops and re-establishes the socket.
func (a *Adapter) Reconnect(_ context.Context, id string) error {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return err
	}
	if acc.cli.Store.ID == nil {
		return adapter.Errorf(adapter.ErrNotConnected, "account is not paired")
	}
	acc.cli.Disconnect()
	acc.setStatus(model.StatusConnecting, nil)
	go acc.connect()
	return nil
}

// Logout unpairs the device and prepares a fresh one for the next login.
func (a *Adapter) Logout(ctx context.Context, id string) error {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return err
	}
	acc.login.Cancel()
	if acc.cli.Store.ID != nil {
		if err := acc.cli.Logout(ctx); err != nil {
			acc.rep.Log.Warn("logout", "err", err)
		}
	}
	acc.resetClient(nil)
	return nil
}

// --- login flows ---

// LoginStart begins QR or phone pairing.
func (a *Adapter) LoginStart(_ context.Context, id, flow string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return model.LoginStep{}, err
	}
	if acc.cli.Store.ID != nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "device is already paired; logout first")
	}
	switch flow {
	case loginFlowQR:
		return acc.startQR()
	case loginPhone:
		acc.login.Start(loginPhone, nil)
		return acc.login.SetStep(base.Input(loginPhone,
			model.LoginField{Name: "phone", Type: "phone", Label: "Phone number with country code", Pattern: `^\+?[0-9]{8,15}$`})), nil
	default:
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "unknown flow %q", flow)
	}
}

// LoginSubmit handles the phone number for the pairing-code flow.
func (a *Adapter) LoginSubmit(ctx context.Context, id string, fields map[string]string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return model.LoginStep{}, err
	}
	lf := acc.login.Current()
	if lf == nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "no login in progress")
	}
	if lf.Name != loginPhone {
		return lf.Step, nil
	}
	phone := strings.TrimLeft(strings.TrimSpace(fields["phone"]), "+")
	if strings.Trim(phone, "0123456789") != "" || len(phone) < 8 {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "phone must be digits with country code")
	}
	if !acc.cli.IsConnected() {
		if err := acc.cli.Connect(); err != nil {
			return model.LoginStep{}, adapter.Errorf(adapter.ErrPlatform, "connect: %v", err)
		}
	}
	code, err := acc.cli.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrPlatform, "pair phone: %v", err)
	}
	exp := time.Now().Add(3 * time.Minute)
	return acc.login.SetStep(base.Display(loginPhone, "code", code, &exp)), nil
}

// LoginRefresh returns the current step (latest QR).
func (a *Adapter) LoginRefresh(_ context.Context, id string) (model.LoginStep, error) {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return model.LoginStep{}, err
	}
	lf := acc.login.Current()
	if lf == nil {
		return model.LoginStep{}, adapter.Errorf(adapter.ErrInvalidInput, "no login in progress")
	}
	return lf.Step, nil
}

// LoginCancel aborts pairing.
func (a *Adapter) LoginCancel(_ context.Context, id string) error {
	acc, err := a.accounts.Get(id)
	if err != nil {
		return err
	}
	acc.login.Cancel()
	acc.cli.Disconnect()
	return nil
}

// account is one paired (or pairing) device.
type account struct {
	id        string
	dir       string
	cfg       config
	rep       base.Reporter
	login     base.Login
	container *sqlstore.Container
	cli       *whatsmeow.Client

	// Phone JIDs already reported as moved to their LID, and identity events waiting for emit.
	mu      sync.Mutex
	known   map[string]bool
	pending []adapter.Event
}

func (acc *account) attach(device *wastore.Device) {
	acc.cli = whatsmeow.NewClient(device, newWALogger(acc.rep.Log.With("component", "whatsmeow")))
	acc.cli.AddEventHandler(acc.handleEvent)
}

// resetClient discards the current device and prepares an unpaired one.
func (acc *account) resetClient(e *model.Error) {
	acc.cli.Disconnect()
	acc.cli.RemoveEventHandlers()
	acc.attach(acc.container.NewDevice())
	acc.setStatus(model.StatusUnpaired, e)
}

func (acc *account) connect() {
	if err := acc.cli.Connect(); err != nil {
		acc.rep.Log.Error("connect", "err", err)
		acc.setStatus(model.StatusError, &model.Error{Code: "network", Message: err.Error()})
	}
}

func (acc *account) startQR() (model.LoginStep, error) {
	if acc.cli.IsConnected() {
		acc.cli.Disconnect()
	}
	qctx, cancel := context.WithCancel(context.Background())
	ch, err := acc.cli.GetQRChannel(qctx)
	if err != nil {
		cancel()
		return model.LoginStep{}, adapter.Errorf(adapter.ErrPlatform, "qr channel: %v", err)
	}
	acc.login.Start(loginFlowQR, cancel)
	if err := acc.cli.Connect(); err != nil {
		acc.login.Cancel()
		return model.LoginStep{}, adapter.Errorf(adapter.ErrPlatform, "connect: %v", err)
	}
	first := make(chan model.LoginStep, 1)
	go acc.consumeQR(ch, first)
	select {
	case step := <-first:
		return step, nil
	case <-time.After(qrWait):
		return model.LoginStep{}, adapter.Errorf(adapter.ErrPlatform, "no QR code received from WhatsApp")
	}
}

func (acc *account) consumeQR(ch <-chan whatsmeow.QRChannelItem, first chan<- model.LoginStep) {
	delivered := false
	for item := range ch {
		var step model.LoginStep
		switch item.Event {
		case "code":
			exp := time.Now().Add(item.Timeout)
			step = base.Display(loginFlowQR, "qr", item.Code, &exp)
		case "success":
			continue // PairSuccess carries the identity
		case "timeout":
			step = base.Failed(loginFlowQR, "QR pairing timed out")
		case "error":
			msg := "QR pairing failed"
			if item.Error != nil {
				msg = item.Error.Error()
			}
			step = base.Failed(loginFlowQR, msg)
		default:
			continue
		}
		step = acc.login.SetStep(step)
		if !delivered {
			delivered = true
			first <- step
			continue
		}
		acc.rep.Step(step)
		if step.Step == model.StepFailed {
			acc.login.Cancel()
		}
	}
}

func (acc *account) setStatus(status string, e *model.Error) {
	st := adapter.Status{Status: status, Error: e}
	if acc.cli.Store.ID != nil {
		st.Self = acc.selfContact()
		st.Device, _ = json.Marshal(map[string]any{
			"jid": acc.cli.Store.ID.String(), "platform": acc.cli.Store.Platform, "device_name": acc.cfg.DeviceName,
		})
	}
	acc.rep.Status(st)
}

func (acc *account) selfContact() *model.Contact {
	id := acc.cli.Store.ID
	if id == nil {
		return nil
	}
	phone := "+" + id.User
	self := id.ToNonAD()
	if lid := acc.cli.Store.GetLID().ToNonAD(); !lid.IsEmpty() {
		self = lid
	}
	return &model.Contact{
		ID: self.String(), Handle: phone, Phone: phone, Names: model.Names{Profile: acc.cli.Store.PushName},
		IsSelf: true, IsContact: true,
	}
}

func (acc *account) requireLogin() error {
	if !acc.cli.IsLoggedIn() {
		return adapter.Errorf(adapter.ErrNotConnected, "device is not logged in")
	}
	return nil
}

// parseJID accepts a JID or bare phone number.
func parseJID(s string) (types.JID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return types.JID{}, adapter.Errorf(adapter.ErrInvalidInput, "chat id is required")
	}
	if !strings.Contains(s, "@") {
		digits := strings.TrimLeft(s, "+")
		if strings.Trim(digits, "0123456789") != "" {
			return types.JID{}, adapter.Errorf(adapter.ErrInvalidTarget, "invalid phone number %q", s)
		}
		return types.NewJID(digits, types.DefaultUserServer), nil
	}
	jid, err := types.ParseJID(s)
	if err != nil || jid.User == "" {
		return types.JID{}, adapter.Errorf(adapter.ErrInvalidTarget, "invalid jid %q", s)
	}
	return jid, nil
}
