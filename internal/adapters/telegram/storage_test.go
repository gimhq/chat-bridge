package telegram

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
)

func TestFileStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "updates.json")
	st, err := newFileState(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.GetState(ctx, 1); err != nil || found {
		t.Fatalf("empty state: %v %v", found, err)
	}
	if err := st.SetState(ctx, 1, updates.State{Pts: 10, Qts: 2, Date: 3, Seq: 4}); err != nil {
		t.Fatal(err)
	}
	_ = st.SetPts(ctx, 1, 11)
	_ = st.SetDateSeq(ctx, 1, 30, 5)
	_ = st.SetChannelPts(ctx, 1, 777, 99)
	_ = st.SetChannelPts(ctx, 1, 778, 100)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	// A fresh instance reads what was flushed.
	st2, err := newFileState(path)
	if err != nil {
		t.Fatal(err)
	}
	s, found, _ := st2.GetState(ctx, 1)
	if !found || s.Pts != 11 || s.Qts != 2 || s.Date != 30 || s.Seq != 5 {
		t.Fatalf("state: %+v %v", s, found)
	}
	if pts, found, _ := st2.GetChannelPts(ctx, 1, 777); !found || pts != 99 {
		t.Fatalf("channel pts: %d %v", pts, found)
	}
	n := 0
	_ = st2.ForEachChannels(ctx, 1, func(context.Context, int64, int) error { n++; return nil })
	if n != 2 {
		t.Fatalf("channels: %d", n)
	}
	// Other users are isolated.
	if _, found, _ := st2.GetState(ctx, 2); found {
		t.Fatal("user 2 leaked")
	}
	// Corrupt file is reported, not silently reset.
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newFileState(path); err == nil {
		t.Fatal("corrupt file accepted")
	}
}

func TestFilePeersRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "peers.json")
	ps, err := newFilePeers(path)
	if err != nil {
		t.Fatal(err)
	}
	key := peers.Key{Prefix: "user", ID: 42}
	if _, found, err := ps.Find(ctx, key); err != nil || found {
		t.Fatalf("empty: %v %v", found, err)
	}
	if err := ps.Save(ctx, key, peers.Value{AccessHash: 1234}); err != nil {
		t.Fatal(err)
	}
	if err := ps.SavePhone(ctx, "+1", key); err != nil {
		t.Fatal(err)
	}
	if err := ps.SaveContactsHash(ctx, 55); err != nil {
		t.Fatal(err)
	}
	if err := ps.Flush(); err != nil {
		t.Fatal(err)
	}
	ps2, err := newFilePeers(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, found, _ := ps2.Find(ctx, key); !found || v.AccessHash != 1234 {
		t.Fatalf("find: %+v %v", v, found)
	}
	if k, v, found, _ := ps2.FindPhone(ctx, "+1"); !found || k != key || v.AccessHash != 1234 {
		t.Fatalf("phone: %+v %+v %v", k, v, found)
	}
	if h, _ := ps2.GetContactsHash(ctx); h != 55 {
		t.Fatalf("contacts hash: %d", h)
	}
	// Writes are coalesced: nothing hits the disk until Flush, and Flush is idempotent.
	_ = ps2.Save(ctx, peers.Key{Prefix: "chat", ID: 7}, peers.Value{AccessHash: 9})
	before, _ := os.ReadFile(path)
	if err := ps2.Flush(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) == string(after) {
		t.Fatal("flush did not write the pending save")
	}
	if err := ps2.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveConfigDefaults(t *testing.T) {
	d := Defaults{AppID: 100, AppHash: "global"}
	c, err := resolveConfig(d, nil)
	if err != nil || c.AppID != 100 || c.AppHash != "global" || c.DeviceName != "chat-bridge" {
		t.Fatalf("defaults only: %+v %v", c, err)
	}
	c, err = resolveConfig(d, []byte(`{"api_id":5,"api_hash":"mine","device_name":"x"}`))
	if err != nil || c.AppID != 5 || c.AppHash != "mine" || c.DeviceName != "x" {
		t.Fatalf("account overrides: %+v %v", c, err)
	}
	if _, err := resolveConfig(Defaults{}, []byte(`{}`)); err == nil {
		t.Fatal("no credentials anywhere must fail")
	}
	if _, err := resolveConfig(d, []byte(`{"api_id":"nope"}`)); err == nil {
		t.Fatal("bad json accepted")
	}
}
