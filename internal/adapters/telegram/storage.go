package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"

	"gimhq/chat-bridge/internal/adapter"
)

// Defaults are the application credentials shared by every account of this adapter
// (config `adapters.telegram.*`); an account's own config may override them.
type Defaults struct {
	AppID   int
	AppHash string
}

// resolveConfig merges the account config over the adapter defaults.
func resolveConfig(d Defaults, raw json.RawMessage) (config, error) {
	c := config{AppID: d.AppID, AppHash: d.AppHash}
	if len(raw) > 0 {
		var over struct {
			AppID      *int    `json:"api_id"`
			AppHash    *string `json:"api_hash"`
			DeviceName string  `json:"device_name"`
		}
		if err := json.Unmarshal(raw, &over); err != nil {
			return config{}, adapter.Errorf(adapter.ErrInvalidInput, "config: %v", err)
		}
		if over.AppID != nil && *over.AppID != 0 {
			c.AppID = *over.AppID
		}
		if over.AppHash != nil && *over.AppHash != "" {
			c.AppHash = *over.AppHash
		}
		c.DeviceName = over.DeviceName
	}
	if c.DeviceName == "" {
		c.DeviceName = "chat-bridge"
	}
	if c.AppID == 0 || c.AppHash == "" {
		return config{}, adapter.Errorf(adapter.ErrInvalidInput,
			"telegram api_id/api_hash missing: set adapters.telegram.api_id and api_hash in the server config, or pass them in the account config")
	}
	return c, nil
}

const flushDelay = 2 * time.Second

// jsonFile persists one JSON document with coalesced, atomic writes.
type jsonFile struct {
	path string
	mu   sync.Mutex
	doc  any
	// dirty is set by writers; a flush is scheduled at most once per flushDelay.
	dirty     bool
	scheduled bool
}

func loadJSON(path string, into any) (*jsonFile, error) {
	f := &jsonFile{path: path, doc: into}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, into); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// touch marks the document dirty under the caller's lock and schedules a delayed flush.
func (f *jsonFile) touch() {
	f.dirty = true
	if !f.scheduled {
		f.scheduled = true
		time.AfterFunc(flushDelay, func() { _ = f.Flush() })
	}
}

// Flush writes the document if it changed since the last write.
func (f *jsonFile) Flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scheduled = false
	if !f.dirty {
		return nil
	}
	b, err := json.Marshal(f.doc)
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return err
	}
	f.dirty = false
	return nil
}

// --- updates state ---

type userState struct {
	State    *updates.State `json:"state,omitempty"`
	Channels map[string]int `json:"channels,omitempty"`
}

type stateDoc struct {
	Users map[string]*userState `json:"users"`
}

// fileState implements updates.StateStorage on a JSON file.
type fileState struct {
	*jsonFile
	doc *stateDoc
}

func newFileState(path string) (*fileState, error) {
	doc := &stateDoc{Users: map[string]*userState{}}
	f, err := loadJSON(path, doc)
	if err != nil {
		return nil, err
	}
	if doc.Users == nil {
		doc.Users = map[string]*userState{}
	}
	return &fileState{jsonFile: f, doc: doc}, nil
}

func (s *fileState) user(userID int64) *userState {
	k := strconv.FormatInt(userID, 10)
	u, ok := s.doc.Users[k]
	if !ok {
		u = &userState{Channels: map[string]int{}}
		s.doc.Users[k] = u
	}
	if u.Channels == nil {
		u.Channels = map[string]int{}
	}
	return u
}

func (s *fileState) GetState(_ context.Context, userID int64) (updates.State, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.doc.Users[strconv.FormatInt(userID, 10)]
	if !ok || u.State == nil {
		return updates.State{}, false, nil
	}
	return *u.State, true, nil
}

func (s *fileState) SetState(_ context.Context, userID int64, st updates.State) error {
	return s.update(userID, func(u *userState) { cp := st; u.State = &cp })
}

func (s *fileState) SetPts(_ context.Context, userID int64, pts int) error {
	return s.update(userID, func(u *userState) { u.state().Pts = pts })
}

func (s *fileState) SetQts(_ context.Context, userID int64, qts int) error {
	return s.update(userID, func(u *userState) { u.state().Qts = qts })
}

func (s *fileState) SetDate(_ context.Context, userID int64, date int) error {
	return s.update(userID, func(u *userState) { u.state().Date = date })
}

func (s *fileState) SetSeq(_ context.Context, userID int64, seq int) error {
	return s.update(userID, func(u *userState) { u.state().Seq = seq })
}

func (s *fileState) SetDateSeq(_ context.Context, userID int64, date, seq int) error {
	return s.update(userID, func(u *userState) { st := u.state(); st.Date, st.Seq = date, seq })
}

func (s *fileState) GetChannelPts(_ context.Context, userID, channelID int64) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.doc.Users[strconv.FormatInt(userID, 10)]
	if !ok {
		return 0, false, nil
	}
	pts, ok := u.Channels[strconv.FormatInt(channelID, 10)]
	return pts, ok, nil
}

func (s *fileState) SetChannelPts(_ context.Context, userID, channelID int64, pts int) error {
	return s.update(userID, func(u *userState) { u.Channels[strconv.FormatInt(channelID, 10)] = pts })
}

func (s *fileState) ForEachChannels(ctx context.Context, userID int64, f func(ctx context.Context, channelID int64, pts int) error) error {
	s.mu.Lock()
	u, ok := s.doc.Users[strconv.FormatInt(userID, 10)]
	channels := map[string]int{}
	if ok {
		for k, v := range u.Channels {
			channels[k] = v
		}
	}
	s.mu.Unlock()
	for k, pts := range channels {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			continue
		}
		if err := f(ctx, id, pts); err != nil {
			return err
		}
	}
	return nil
}

func (s *fileState) update(userID int64, fn func(u *userState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.user(userID))
	s.touch()
	return nil
}

func (u *userState) state() *updates.State {
	if u.State == nil {
		u.State = &updates.State{}
	}
	return u.State
}

// --- peers ---

type peersDoc struct {
	Peers        map[string]int64  `json:"peers"`  // "prefix:id" → access hash
	Phones       map[string]string `json:"phones"` // phone → "prefix:id"
	ContactsHash int64             `json:"contacts_hash"`
}

// filePeers implements peers.Storage on a JSON file.
type filePeers struct {
	*jsonFile
	doc *peersDoc
}

func newFilePeers(path string) (*filePeers, error) {
	doc := &peersDoc{Peers: map[string]int64{}, Phones: map[string]string{}}
	f, err := loadJSON(path, doc)
	if err != nil {
		return nil, err
	}
	if doc.Peers == nil {
		doc.Peers = map[string]int64{}
	}
	if doc.Phones == nil {
		doc.Phones = map[string]string{}
	}
	return &filePeers{jsonFile: f, doc: doc}, nil
}

func peerKey(k peers.Key) string { return k.Prefix + ":" + strconv.FormatInt(k.ID, 10) }

func parsePeerKey(s string) (peers.Key, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			id, err := strconv.ParseInt(s[i+1:], 10, 64)
			return peers.Key{Prefix: s[:i], ID: id}, err == nil
		}
	}
	return peers.Key{}, false
}

func (p *filePeers) Save(_ context.Context, key peers.Key, value peers.Value) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doc.Peers[peerKey(key)] = value.AccessHash
	p.touch()
	return nil
}

func (p *filePeers) Find(_ context.Context, key peers.Key) (peers.Value, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.doc.Peers[peerKey(key)]
	return peers.Value{AccessHash: h}, ok, nil
}

func (p *filePeers) SavePhone(_ context.Context, phone string, key peers.Key) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doc.Phones[phone] = peerKey(key)
	p.touch()
	return nil
}

func (p *filePeers) FindPhone(_ context.Context, phone string) (peers.Key, peers.Value, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, ok := p.doc.Phones[phone]
	if !ok {
		return peers.Key{}, peers.Value{}, false, nil
	}
	key, ok := parsePeerKey(k)
	if !ok {
		return peers.Key{}, peers.Value{}, false, nil
	}
	h, ok := p.doc.Peers[k]
	return key, peers.Value{AccessHash: h}, ok, nil
}

func (p *filePeers) GetContactsHash(context.Context) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doc.ContactsHash, nil
}

func (p *filePeers) SaveContactsHash(_ context.Context, hash int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doc.ContactsHash = hash
	p.touch()
	return nil
}

func statePath(dir string) string { return filepath.Join(dir, "updates.json") }
func peersPath(dir string) string { return filepath.Join(dir, "peers.json") }
