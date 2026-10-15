package telegram

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

func TestInlineMarkupRoundTrip(t *testing.T) {
	rows := [][]model.Button{{{Text: "A", Data: "d:ev1:0"}, {Text: "B", Data: "d:ev1:1"}}, {{Text: "Docs", URL: "https://example.org"}}}
	kb := inlineMarkup(rows)
	if cb, ok := kb.Rows[0].Buttons[0].Type.(*tg.InlineButtonTypeCallback); !ok || string(cb.Data) != "d:ev1:0" || kb.Rows[0].Buttons[0].Text != "A" {
		t.Fatalf("callback button: %#v", kb.Rows[0].Buttons[0])
	}
	if u, ok := kb.Rows[1].Buttons[0].Type.(*tg.InlineButtonTypeURL); !ok || u.URL != "https://example.org" {
		t.Fatalf("url button: %#v", kb.Rows[1].Buttons[0])
	}
	if got := inlineButtons(kb); !reflect.DeepEqual(got, rows) {
		t.Fatalf("round trip: %+v", got)
	}
	if inlineButtons(&tg.ReplyKeyboardHide{}) != nil {
		t.Fatal("non-inline markup mapped to buttons")
	}
	mixed := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{{Buttons: []tg.KeyboardInlineButton{{Text: "s", Type: &tg.InlineButtonTypeSwitchInline{}}}}}}
	if inlineButtons(mixed) != nil {
		t.Fatal("unsupported button kinds must be skipped")
	}
	c := convertContent(&tg.Message{Message: "q", ReplyMarkup: kb}, "m")
	if !reflect.DeepEqual(c.Buttons, rows) {
		t.Fatalf("inbound message buttons: %+v", c.Buttons)
	}
}

func TestPressEvent(t *testing.T) {
	at := time.Date(2026, 10, 14, 5, 0, 0, 0, time.UTC)
	e := tg.Entities{Users: map[int64]*tg.User{42: {ID: 42, FirstName: "Alice"}}}
	ev := pressEvent(&tg.UpdateBotCallbackQuery{QueryID: 987654321, UserID: 42, Peer: &tg.PeerChat{ChatID: 7}, MsgID: 99, Data: []byte("d:ev1:0")}, e, at)
	want := adapter.Event{Kind: adapter.EvButton, ChatID: "-7", MessageID: "-7:99", UserID: "42", Data: "d:ev1:0", PressID: "987654321", At: at}
	if ev.Sender == nil || ev.Sender.Names.First != "Alice" {
		t.Fatalf("sender hint: %+v", ev.Sender)
	}
	ev.Sender = nil
	if !reflect.DeepEqual(ev, want) {
		t.Fatalf("event: %+v", ev)
	}
}

func TestBotCapabilities(t *testing.T) {
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Defaults{})
	dir := t.TempDir()
	acc := &account{id: "b", dir: dir, rep: base.Reporter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	a.accounts.Put("b", acc)
	if got := a.AccountCapabilities("b"); !slices.Equal(got, userCaps) {
		t.Fatalf("user account: %v", got)
	}
	acc.markBot(true)
	if _, err := os.Stat(filepath.Join(dir, "bot")); err != nil {
		t.Fatal(err)
	}
	got := a.AccountCapabilities("b")
	if !slices.Contains(got, adapter.CapButtons) {
		t.Fatalf("bot lacks buttons: %v", got)
	}
	for _, c := range []string{adapter.CapHistory, adapter.CapChatRead, adapter.CapChatMembers, adapter.CapChatCreate, adapter.CapSelfUpdate, adapter.CapPresence, adapter.CapReceipts} {
		if slices.Contains(got, c) {
			t.Errorf("bot advertises %s", c)
		}
	}
	acc.self = &tg.User{ID: 1} // the logged-in identity wins over the marker
	if slices.Contains(a.AccountCapabilities("b"), adapter.CapButtons) {
		t.Fatal("a user identity kept bot capabilities")
	}
	if a.AccountCapabilities("missing") != nil {
		t.Fatal("unknown account must fall back to Info")
	}
	if !slices.Contains(a.Info().Capabilities, adapter.CapButtons) {
		t.Fatal("platform must advertise message.buttons")
	}
}
