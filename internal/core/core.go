// Package core owns everything a consumer sees: accounts, the login machine, the store, the
// event log, media policy, and webhook delivery. Adapters plug in underneath (docs/adapter-protocol.md).
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/media"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// Error is an API-facing failure (docs/chat-api-spec.md §2.1).
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Error constructors.
func errInvalid(format string, a ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: fmt.Sprintf(format, a...)}
}
func errNotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: what + " not found"}
}
func errNotReady(status string) *Error {
	return &Error{Status: http.StatusConflict, Code: "account_not_ready", Message: "account is " + status}
}
func errConflict(msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: "conflict", Message: msg}
}
func errUnsupported(capability string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "unsupported", Message: "platform lacks " + capability,
		Details: map[string]any{"capability": capability}}
}

// AsError converts any error into an *Error, mapping adapter and store errors.
func AsError(err error) *Error {
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	var ae *adapter.Error
	if errors.As(err, &ae) {
		out := &Error{Code: ae.Code, Message: ae.Message, Details: ae.Details}
		switch ae.Code {
		case adapter.ErrNotConnected:
			out.Status, out.Code = http.StatusConflict, "account_not_ready"
		case adapter.ErrInvalidTarget:
			out.Status, out.Code = http.StatusNotFound, "not_found"
		case adapter.ErrInvalidInput:
			out.Status, out.Code = http.StatusBadRequest, "invalid_request"
		case adapter.ErrUnsupported:
			out.Status = http.StatusUnprocessableEntity
		case adapter.ErrRateLimited:
			out.Status = http.StatusTooManyRequests
		default:
			out.Status, out.Code = http.StatusBadGateway, "platform_error"
		}
		return out
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errNotFound("resource")
	case errors.Is(err, store.ErrConflict):
		return errConflict(err.Error())
	}
	return &Error{Status: http.StatusInternalServerError, Code: "internal", Message: err.Error()}
}

// MediaPolicy decides which inbound attachments are downloaded eagerly.
type MediaPolicy struct {
	AutoDownload []string // content types: image, voice, sticker, ...
	MaxBytes     int64
}

func (p MediaPolicy) wants(contentType string, size int64) bool {
	if p.MaxBytes > 0 && size > p.MaxBytes {
		return false
	}
	for _, t := range p.AutoDownload {
		if t == contentType {
			return true
		}
	}
	return false
}

// Options configures the core.
type Options struct {
	Store   *store.Store
	Blobs   *media.Blobs
	DataDir string
	Logger  *slog.Logger
	Media   MediaPolicy
	// EventRetention bounds the event log; zero means 7 days.
	EventRetention time.Duration
}

// Core is the service layer behind the HTTP API.
type Core struct {
	st       *store.Store
	blobs    *media.Blobs
	dataDir  string
	log      *slog.Logger
	policy   MediaPolicy
	retain   time.Duration
	amu      sync.RWMutex
	adapters map[string]adapter.Adapter
	bus      *bus
	httpc    *http.Client

	mu     sync.Mutex
	logins map[string]*loginState
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// New builds a Core; call Register for each adapter, then Start.
func New(opts Options) *Core {
	if opts.EventRetention == 0 {
		opts.EventRetention = 7 * 24 * time.Hour
	}
	return &Core{
		st: opts.Store, blobs: opts.Blobs, dataDir: opts.DataDir, log: opts.Logger, policy: opts.Media, retain: opts.EventRetention,
		adapters: map[string]adapter.Adapter{}, bus: newBus(), httpc: &http.Client{Timeout: 10 * time.Second},
		logins: map[string]*loginState{}, stopCh: make(chan struct{}),
	}
}

// Register adds an in-process platform adapter. Must be called before Start; remote adapters
// join later through Attach.
func (c *Core) Register(a adapter.Adapter) {
	c.amu.Lock()
	c.adapters[registryKey(a.Info())] = a
	c.amu.Unlock()
}

// instanceOf returns the adapter instance id, defaulting to "local".
func instanceOf(info adapter.Info) string {
	if info.Instance == "" {
		return "local"
	}
	return info.Instance
}

func registryKey(info adapter.Info) string { return info.Platform + "/" + instanceOf(info) }

// adapter returns the registered adapter for a platform instance.
func (c *Core) adapter(platform, instance string) (adapter.Adapter, bool) {
	c.amu.RLock()
	defer c.amu.RUnlock()
	a, ok := c.adapters[platform+"/"+instance]
	return a, ok
}

// instances lists the adapters serving a platform.
func (c *Core) instances(platform string) []adapter.Adapter {
	c.amu.RLock()
	defer c.amu.RUnlock()
	var out []adapter.Adapter
	for _, a := range c.adapters {
		if a.Info().Platform == platform {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return instanceOf(out[i].Info()) < instanceOf(out[j].Info()) })
	return out
}

// resolve picks the adapter for an account: its bound instance, or the only instance of the
// platform when it is not bound yet.
func (c *Core) resolve(row store.AccountRow) (adapter.Adapter, error) {
	if row.Adapter != "" {
		a, ok := c.adapter(row.Platform, row.Adapter)
		if !ok {
			return nil, &Error{Status: http.StatusConflict, Code: "account_not_ready", Message: "adapter " + row.Platform + "/" + row.Adapter + " is not connected"}
		}
		return a, nil
	}
	switch all := c.instances(row.Platform); len(all) {
	case 0:
		return nil, &Error{Status: http.StatusConflict, Code: "account_not_ready", Message: "no adapter connected for " + row.Platform}
	case 1:
		return all[0], nil
	default:
		return nil, &Error{Status: http.StatusConflict, Code: "account_not_ready", Message: "several " + row.Platform + " adapters are connected; bind the account with PATCH {\"adapter\": ...}"}
	}
}

// Start launches adapters, hands them their accounts, and starts background workers.
func (c *Core) Start(ctx context.Context) error {
	c.amu.RLock()
	registered := make([]adapter.Adapter, 0, len(c.adapters))
	for _, a := range c.adapters {
		registered = append(registered, a)
	}
	c.amu.RUnlock()
	for _, a := range registered {
		if err := a.Start(ctx, &sink{c: c}); err != nil {
			return fmt.Errorf("start %s adapter: %w", a.Info().Platform, err)
		}
		c.attachAccounts(ctx, a)
	}
	rows, err := c.st.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := c.resolve(row); err != nil {
			c.log.Warn("no adapter for account", "account", row.ID, "platform", row.Platform, "adapter", row.Adapter)
			_ = c.st.SetAccountStatus(ctx, row.ID, model.StatusError, "", nil, &model.Error{Code: "adapter_offline", Message: AsError(err).Message})
		}
	}
	c.wg.Add(2)
	go c.webhookLoop()
	go c.gcLoop()
	return nil
}

// Stop halts workers and adapters.
func (c *Core) Stop(ctx context.Context) {
	close(c.stopCh)
	c.amu.RLock()
	for _, a := range c.adapters {
		_ = a.Stop(ctx)
	}
	c.amu.RUnlock()
	c.wg.Wait()
}

func (c *Core) accountDir(id string) string {
	dir := filepath.Join(c.dataDir, "accounts", id)
	_ = os.MkdirAll(dir, 0o750)
	return dir
}

func (c *Core) adapterFor(ctx context.Context, accountID string) (store.AccountRow, adapter.Adapter, error) {
	row, err := c.st.GetAccount(ctx, accountID)
	if errors.Is(err, store.ErrNotFound) {
		return row, nil, errNotFound("account")
	}
	if err != nil {
		return row, nil, err
	}
	a, err := c.resolve(row)
	if err != nil {
		return row, nil, err
	}
	return row, a, nil
}

// connected returns the row and adapter, or account_not_ready.
func (c *Core) connected(ctx context.Context, accountID string) (store.AccountRow, adapter.Adapter, error) {
	row, a, err := c.adapterFor(ctx, accountID)
	if err != nil {
		return row, nil, err
	}
	if row.Status != model.StatusConnected {
		return row, nil, errNotReady(row.Status)
	}
	return row, a, nil
}

func requireCap(a adapter.Adapter, capability string) error {
	if !a.Info().Has(capability) {
		return errUnsupported(capability)
	}
	return nil
}

// Platforms lists registered adapters for GET /platforms.
func (c *Core) Platforms() []model.Platform {
	c.amu.RLock()
	defer c.amu.RUnlock()
	byPlatform := map[string]*model.Platform{}
	for _, a := range c.adapters {
		info := a.Info()
		p, ok := byPlatform[info.Platform]
		if !ok {
			p = &model.Platform{ID: info.Platform, Name: info.Name, Capabilities: info.Capabilities, LoginFlows: info.LoginFlows, ConfigSchema: info.ConfigSchema}
			byPlatform[info.Platform] = p
		}
		_, remote := a.(interface{ Remote() bool })
		p.Instances = append(p.Instances, model.PlatformInstance{ID: instanceOf(info), Name: info.Name, Version: info.Version, Remote: remote, Capabilities: info.Capabilities})
	}
	out := make([]model.Platform, 0, len(byPlatform))
	for _, p := range byPlatform {
		sort.Slice(p.Instances, func(i, j int) bool { return p.Instances[i].ID < p.Instances[j].ID })
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// tx runs fn in a transaction and wakes event listeners after commit.
func (c *Core) tx(ctx context.Context, fn func(tx *store.Store) error) error {
	if err := c.st.Tx(ctx, fn); err != nil {
		return err
	}
	c.bus.notify()
	return nil
}

// emit appends an event inside tx.
func emit(ctx context.Context, tx *store.Store, accountID, typ string, data any) error {
	_, err := tx.AppendEvent(ctx, accountID, typ, data)
	return err
}

// redactConfig blanks fields the schema marks x-secret.
func redactConfig(cfg, schema json.RawMessage) json.RawMessage {
	if len(cfg) == 0 {
		return json.RawMessage("{}")
	}
	var sch struct {
		Properties map[string]struct {
			Secret bool `json:"x-secret"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(schema, &sch)
	var m map[string]any
	if json.Unmarshal(cfg, &m) != nil {
		return cfg
	}
	for k, p := range sch.Properties {
		if p.Secret {
			if _, ok := m[k]; ok {
				m[k] = "***"
			}
		}
	}
	b, _ := json.Marshal(m)
	return b
}
