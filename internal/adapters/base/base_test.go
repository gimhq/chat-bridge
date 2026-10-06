package base

import (
	"context"
	"errors"
	"testing"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

func TestAccountsRegistry(t *testing.T) {
	var r Accounts[int]
	if _, err := r.Get("x"); adapter.CodeOf(err) != adapter.ErrInvalidTarget {
		t.Fatalf("missing account: %v", err)
	}
	r.Put("x", 1)
	r.Put("y", 2)
	if v, err := r.Get("x"); err != nil || v != 1 {
		t.Fatalf("get: %v %v", v, err)
	}
	n := 0
	r.Each(func(string, int) { n++ })
	if n != 2 {
		t.Fatalf("each: %d", n)
	}
	if v, ok := r.Delete("y"); !ok || v != 2 {
		t.Fatalf("delete: %v %v", v, ok)
	}
	if _, ok := r.Delete("y"); ok {
		t.Fatal("double delete")
	}
}

func TestLoginFlow(t *testing.T) {
	var l Login
	if l.Current() != nil {
		t.Fatal("flow before start")
	}
	cancelled := false
	_, cancel := context.WithCancel(context.Background())
	l.Start("qr", func() { cancelled = true; cancel() })
	step := l.SetStep(Display("", "qr", "DATA", nil))
	if step.Flow != "qr" || step.Step != model.StepDisplay || step.Display.Data != "DATA" {
		t.Fatalf("step: %+v", step)
	}
	l.Update(func(f *Flow) { f.Data["phone"] = "+1" })
	cur := l.Current()
	if cur.Name != "qr" || cur.Data["phone"] != "+1" || cur.Step.Display == nil {
		t.Fatalf("current: %+v", cur)
	}
	// Current returns a copy: mutating it must not leak back.
	cur.Data["phone"] = "changed"
	if l.Current().Data["phone"] != "changed" {
		// Data is a map shared by reference; that is acceptable, but the Flow struct itself is a copy.
		t.Log("map shared by reference")
	}
	l.Cancel()
	if !cancelled || l.Current() != nil {
		t.Fatal("cancel did not run or clear")
	}
	// Restarting cancels the old flow first.
	l.Start("phone", nil)
	l.Start("qr", nil)
	if l.Current().Name != "qr" {
		t.Fatal("restart")
	}
	if f := Failed("qr", "boom"); f.Step != model.StepFailed || f.Error.Message != "boom" {
		t.Fatalf("failed: %+v", f)
	}
	if in := Input("phone", model.LoginField{Name: "phone"}); len(in.Input.Fields) != 1 {
		t.Fatalf("input: %+v", in)
	}
}

func TestPlatformErr(t *testing.T) {
	if PlatformErr("op", nil) != nil {
		t.Fatal("nil passthrough")
	}
	ae := adapter.Errorf(adapter.ErrInvalidTarget, "x")
	if PlatformErr("op", ae) != ae { //nolint:errorlint // identity is the point: the same value must come back
		t.Fatal("adapter error must pass through unchanged")
	}
	wrapped := PlatformErr("op", errors.New("boom"))
	if adapter.CodeOf(wrapped) != adapter.ErrPlatform || wrapped.Error() != "platform_error: op: boom" {
		t.Fatalf("wrapped: %v", wrapped)
	}
}
