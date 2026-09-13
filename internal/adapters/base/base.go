// Package base holds the scaffolding every in-process adapter shares: an account registry,
// a per-account reporter over the core sink, and the login-flow state.
package base

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

// Accounts is a mutex-guarded registry keyed by account id.
type Accounts[T any] struct {
	mu sync.Mutex
	m  map[string]T
}

// Get returns the account or an invalid_target error.
func (r *Accounts[T]) Get(id string) (T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.m[id]
	if !ok {
		var zero T
		return zero, adapter.Errorf(adapter.ErrInvalidTarget, "unknown account %s", id)
	}
	return v, nil
}

// Put stores the account.
func (r *Accounts[T]) Put(id string, v T) {
	r.mu.Lock()
	if r.m == nil {
		r.m = map[string]T{}
	}
	r.m[id] = v
	r.mu.Unlock()
}

// Delete removes and returns the account.
func (r *Accounts[T]) Delete(id string) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.m[id]
	delete(r.m, id)
	return v, ok
}

// Each calls fn for every account.
func (r *Accounts[T]) Each(fn func(id string, v T)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, v := range r.m {
		fn(id, v)
	}
}

// Reporter binds the core sink to one account with timeouts and logging.
type Reporter struct {
	Sink adapter.Sink
	ID   string
	Log  *slog.Logger
}

// Status reports an account status transition.
func (r Reporter) Status(st adapter.Status) {
	ctx, cancel := Timeout(10 * time.Second)
	defer cancel()
	if err := r.Sink.Status(ctx, r.ID, st); err != nil {
		r.Log.Warn("report status", "status", st.Status, "err", err)
	}
}

// Step pushes an unsolicited login step.
func (r Reporter) Step(step model.LoginStep) {
	ctx, cancel := Timeout(10 * time.Second)
	defer cancel()
	if err := r.Sink.LoginStep(ctx, r.ID, step); err != nil {
		r.Log.Warn("push login step", "err", err)
	}
}

// Events delivers a batch; empty batches are ignored.
func (r Reporter) Events(evs ...adapter.Event) {
	if len(evs) == 0 {
		return
	}
	ctx, cancel := Timeout(30 * time.Second)
	defer cancel()
	if err := r.Sink.Events(ctx, r.ID, evs); err != nil {
		r.Log.Error("deliver events", "count", len(evs), "err", err)
	}
}

// PutMedia stores downloaded bytes.
func (r Reporter) PutMedia(ctx context.Context, mediaID string, meta adapter.MediaMeta, rd io.Reader) (model.Attachment, error) {
	return r.Sink.PutMedia(ctx, r.ID, mediaID, meta, rd)
}

// Flow is one in-progress login.
type Flow struct {
	Name   string
	Step   model.LoginStep
	Cancel context.CancelFunc
	Data   map[string]string
}

// Login guards the current flow of one account.
type Login struct {
	mu  sync.Mutex
	cur *Flow
}

// Start cancels any previous flow and installs a new one.
func (l *Login) Start(name string, cancel context.CancelFunc) {
	l.Cancel()
	l.mu.Lock()
	l.cur = &Flow{Name: name, Cancel: cancel, Data: map[string]string{}}
	l.mu.Unlock()
}

// Current returns a copy of the flow, or nil.
func (l *Login) Current() *Flow {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur == nil {
		return nil
	}
	cp := *l.cur
	return &cp
}

// Update mutates the flow in place if one exists.
func (l *Login) Update(fn func(f *Flow)) {
	l.mu.Lock()
	if l.cur != nil {
		fn(l.cur)
	}
	l.mu.Unlock()
}

// SetStep records the latest step and stamps the flow name on it.
func (l *Login) SetStep(step model.LoginStep) model.LoginStep {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur != nil {
		if step.Flow == "" {
			step.Flow = l.cur.Name
		}
		l.cur.Step = step
	}
	return step
}

// Cancel aborts and clears the flow.
func (l *Login) Cancel() {
	l.mu.Lock()
	if l.cur != nil && l.cur.Cancel != nil {
		l.cur.Cancel()
	}
	l.cur = nil
	l.mu.Unlock()
}

// Failed builds a failed step with a platform error.
func Failed(flow, msg string) model.LoginStep {
	return model.LoginStep{Flow: flow, Step: model.StepFailed, Error: &model.Error{Code: "platform_error", Message: msg}}
}

// Input builds an input step.
func Input(flow string, fields ...model.LoginField) model.LoginStep {
	return model.LoginStep{Flow: flow, Step: model.StepInput, Input: &model.LoginInput{Fields: fields}}
}

// Display builds a display step.
func Display(flow, typ, data string, expires *time.Time) model.LoginStep {
	return model.LoginStep{Flow: flow, Step: model.StepDisplay, Display: &model.LoginDisplay{Type: typ, Data: data, ExpiresAt: expires}}
}

// MarkBackfill flags every message event in evs as replayed history.
func MarkBackfill(evs []adapter.Event) []adapter.Event {
	for i := range evs {
		if evs[i].Kind == adapter.EvMessage {
			evs[i].Backfill = true
		}
	}
	return evs
}

// PlatformErr wraps a library error as platform_error unless it already is an adapter error.
func PlatformErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var ae *adapter.Error
	if errors.As(err, &ae) {
		return err
	}
	return adapter.Errorf(adapter.ErrPlatform, "%s: %v", op, err)
}

// Timeout returns a background context with a deadline.
func Timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
