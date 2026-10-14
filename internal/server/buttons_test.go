package server

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/core"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

var twoRows = []any{
	[]any{map[string]any{"text": "A", "data": "d:ev1:0"}, map[string]any{"text": "B", "data": "d:ev1:1"}},
	[]any{map[string]any{"text": "Docs", "url": "https://example.org/docs"}},
}

func buttonsOf(t *testing.T, out map[string]any) []any {
	t.Helper()
	b, _ := out["content"].(map[string]any)["buttons"].([]any)
	return b
}

func TestSendButtons(t *testing.T) {
	e := newEnv(t)
	e.fake.Buttons = true
	e.connected("a1")
	rec, out := e.do("POST", "/v1/accounts/a1/chats/u1@fake/messages", map[string]any{
		"content": map[string]any{"type": "text", "text": "Pick one", "buttons": twoRows}})
	if rec.Code != 201 || len(buttonsOf(t, out)) != 2 {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	want := [][]model.Button{{{Text: "A", Data: "d:ev1:0"}, {Text: "B", Data: "d:ev1:1"}}, {{Text: "Docs", URL: "https://example.org/docs"}}}
	if got := e.fake.Sent[0].Content.Buttons; !reflect.DeepEqual(got, want) {
		t.Fatalf("adapter got %+v", got)
	}
	if rec, got := e.do("GET", "/v1/accounts/a1/messages/"+out["id"].(string), nil); rec.Code != 200 || len(buttonsOf(t, got)) != 2 {
		t.Fatalf("stored: %d %s", rec.Code, rec.Body)
	}
}

func TestSendButtonsValidation(t *testing.T) {
	e := newEnv(t)
	e.fake.Buttons = true
	e.connected("a1")
	long := strings.Repeat("x", 65)
	for name, content := range map[string]map[string]any{
		"voice":      {"type": "voice", "attachments": []any{map[string]any{"media_id": "x"}}, "buttons": twoRows},
		"no text":    {"type": "text", "text": "q", "buttons": []any{[]any{map[string]any{"data": "x"}}}},
		"both":       {"type": "text", "text": "q", "buttons": []any{[]any{map[string]any{"text": "a", "data": "x", "url": "https://a"}}}},
		"neither":    {"type": "text", "text": "q", "buttons": []any{[]any{map[string]any{"text": "a"}}}},
		"long data":  {"type": "text", "text": "q", "buttons": []any{[]any{map[string]any{"text": "a", "data": long}}}},
		"bad scheme": {"type": "text", "text": "q", "buttons": []any{[]any{map[string]any{"text": "a", "url": "javascript:alert(1)"}}}},
		"empty row":  {"type": "text", "text": "q", "buttons": []any{[]any{}}},
	} {
		if rec, out := e.do("POST", "/v1/accounts/a1/chats/u1@fake/messages", map[string]any{"content": content}); rec.Code != 400 || errCode(out) != "invalid_request" {
			t.Errorf("%s: got %d %v, want 400", name, rec.Code, out)
		}
	}
	if len(e.fake.Sent) != 0 {
		t.Fatalf("invalid buttons reached the adapter: %d", len(e.fake.Sent))
	}
}

func TestButtonsNeedCapability(t *testing.T) {
	e := newEnv(t)
	e.connected("a1")
	body := map[string]any{"content": map[string]any{"type": "text", "text": "Pick", "buttons": twoRows}}
	rec, out := e.do("POST", "/v1/accounts/a1/chats/u1@fake/messages", body)
	if rec.Code != 422 || errCode(out) != "unsupported" {
		t.Fatalf("send: %d %v", rec.Code, out)
	}
	if err := e.fake.Push(context.Background(), "a1", adapter.Event{Kind: adapter.EvMessage,
		Message: &model.Message{ID: "own", ChatID: "u1@fake", Sender: model.Sender{ID: "self@fake"}, FromMe: true, Timestamp: time.Now().UTC(),
			Content: model.Content{Type: "text", Text: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if rec, out := e.do("PATCH", "/v1/accounts/a1/messages/own", map[string]any{"buttons": []any{}}); rec.Code != 422 || errCode(out) != "unsupported" {
		t.Fatalf("patch: %d %v", rec.Code, out)
	}
}

// One adapter, two accounts: the per-account capability set decides.
func TestAccountCapabilities(t *testing.T) {
	e := newEnv(t)
	e.fake.AccountCaps = map[string][]string{"bot": {adapter.CapSendText, adapter.CapButtons}}
	e.connected("a1")
	e.connected("bot")
	rec, out := e.do("GET", "/v1/accounts/bot", nil)
	if rec.Code != 200 || !reflect.DeepEqual(out["capabilities"], []any{adapter.CapSendText, adapter.CapButtons}) {
		t.Fatalf("bot account: %d %v", rec.Code, out["capabilities"])
	}
	body := map[string]any{"content": map[string]any{"type": "text", "text": "Pick", "buttons": twoRows}}
	if rec, _ := e.do("POST", "/v1/accounts/a1/chats/u1@fake/messages", body); rec.Code != 422 {
		t.Fatalf("user account sent buttons: %d", rec.Code)
	}
	if rec, _ := e.do("POST", "/v1/accounts/bot/chats/u1@fake/messages", body); rec.Code != 201 {
		t.Fatalf("bot account: %d %s", rec.Code, rec.Body)
	}
	if rec, out := e.do("POST", "/v1/accounts/bot/chats/u1@fake/typing", map[string]any{"state": "typing"}); rec.Code != 422 || errCode(out) != "unsupported" {
		t.Fatalf("typing on bot: %d %v", rec.Code, out)
	}
}

func TestEditButtons(t *testing.T) {
	e := newEnv(t)
	e.fake.Buttons = true
	e.connected("a1")
	_, sent := e.do("POST", "/v1/accounts/a1/chats/u1@fake/messages", map[string]any{
		"content": map[string]any{"type": "text", "text": "Pick one", "buttons": twoRows}})
	path := "/v1/accounts/a1/messages/" + sent["id"].(string)

	confirm := []any{[]any{map[string]any{"text": "Confirm A", "data": "c:ev1:0"}}}
	rec, out := e.do("PATCH", path, map[string]any{"buttons": confirm})
	if rec.Code != 200 || len(buttonsOf(t, out)) != 1 || out["content"].(map[string]any)["text"] != "Pick one" || out["edited_at"] != nil {
		t.Fatalf("replace buttons: %d %s", rec.Code, rec.Body)
	}
	if last := e.fake.Edits[len(e.fake.Edits)-1]; last.Text != "Pick one" || len(last.Buttons) != 1 {
		t.Fatalf("adapter got %+v", last)
	}

	// A text edit keeps the keyboard.
	rec, out = e.do("PATCH", path, map[string]any{"content": map[string]any{"type": "text", "text": "Pick one (A?)"}})
	if rec.Code != 200 || len(buttonsOf(t, out)) != 1 || out["edited_at"] == nil {
		t.Fatalf("text edit: %d %s", rec.Code, rec.Body)
	}
	if last := e.fake.Edits[len(e.fake.Edits)-1]; len(last.Buttons) != 1 {
		t.Fatalf("text edit dropped the keyboard for the adapter: %+v", last)
	}

	rec, out = e.do("PATCH", path, map[string]any{"buttons": []any{}})
	if rec.Code != 200 || buttonsOf(t, out) != nil {
		t.Fatalf("remove buttons: %d %s", rec.Code, rec.Body)
	}
	if last := e.fake.Edits[len(e.fake.Edits)-1]; last.Buttons != nil || last.Text != "Pick one (A?)" {
		t.Fatalf("adapter got %+v", last)
	}

	if rec, out := e.do("PATCH", path, map[string]any{}); rec.Code != 400 || errCode(out) != "invalid_request" {
		t.Fatalf("empty patch: %d %v", rec.Code, out)
	}
	evs, _, err := e.core.ListEvents(context.Background(), "", eventTypes(model.EvMessageUpdated), 10)
	if err != nil || len(evs) != 3 {
		t.Fatalf("message.updated events: %d %v", len(evs), err)
	}
}

// Inbound keyboards are kept on the stored message, and an edit that changes them replaces them.
func TestInboundButtons(t *testing.T) {
	e := newEnv(t)
	e.connected("a1")
	kb := [][]model.Button{{{Text: "Yes", Data: "y"}}}
	ctx := context.Background()
	if err := e.fake.Push(ctx, "a1", adapter.Event{Kind: adapter.EvMessage, Message: &model.Message{ID: "in1", ChatID: "u1@fake",
		Sender: model.Sender{ID: "u1@fake"}, Timestamp: time.Now().UTC(), Content: model.Content{Type: "text", Text: "q", Buttons: kb}}}); err != nil {
		t.Fatal(err)
	}
	if rec, out := e.do("GET", "/v1/accounts/a1/messages/in1", nil); rec.Code != 200 || len(buttonsOf(t, out)) != 1 {
		t.Fatalf("inbound: %d %s", rec.Code, rec.Body)
	}
	if err := e.fake.Push(ctx, "a1", adapter.Event{Kind: adapter.EvMessageUpdate, ChatID: "u1@fake", MessageID: "in1",
		Content: &model.Content{Type: "text", Text: "q"}}); err != nil {
		t.Fatal(err)
	}
	if _, out := e.do("GET", "/v1/accounts/a1/messages/in1", nil); buttonsOf(t, out) != nil {
		t.Fatalf("keyboard not removed: %v", out["content"])
	}
}

func eventTypes(types ...string) store.EventFilter { return store.EventFilter{Types: types} }

func press(id, chat string) adapter.Event {
	return adapter.Event{Kind: adapter.EvButton, ChatID: chat, MessageID: chat + ":7", UserID: "u1@fake", Data: "d:ev1:0", PressID: id,
		Sender: &model.Contact{ID: "u1@fake", Names: model.Names{Profile: "Alice"}}, At: time.Date(2026, 10, 14, 5, 0, 0, 0, time.UTC)}
}

func TestButtonPress(t *testing.T) {
	e := newEnv(t)
	e.fake.Buttons = true
	e.connected("a1")
	if err := e.fake.Push(context.Background(), "a1", press("p1", "u1@fake")); err != nil {
		t.Fatal(err)
	}
	// Two consumers with their own cursors both read the press.
	for range 2 {
		rec, out := e.do("GET", "/v1/events?types=message.button", nil)
		evs, _ := out["events"].([]any)
		if rec.Code != 200 || len(evs) != 1 {
			t.Fatalf("events: %d %s", rec.Code, rec.Body)
		}
		data := evs[0].(map[string]any)["data"].(map[string]any)
		want := map[string]any{"chat_id": "u1@fake", "message_id": "u1@fake:7", "user_id": "u1@fake", "data": "d:ev1:0", "press_id": "p1",
			"pressed_at": "2026-10-14T05:00:00Z", "sender": map[string]any{"id": "u1@fake", "name": "Alice W"}}
		if !reflect.DeepEqual(data, want) {
			t.Fatalf("payload: %v", data)
		}
	}
	if rec, _ := e.do("GET", "/v1/accounts/a1/messages/u1@fake:7", nil); rec.Code != 404 {
		t.Fatalf("a press must not create a message: %d", rec.Code)
	}

	answer := "/v1/accounts/a1/presses/p1/answer"
	if rec, _ := e.do("POST", answer, map[string]any{"text": "Recorded", "alert": true}); rec.Code != 204 {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	if got := e.fake.AnsweredPresses(); !slices.Equal(got, []string{"p1:Recorded:true"}) {
		t.Fatalf("adapter answers: %v", got)
	}
	if rec, out := e.do("POST", answer, map[string]any{"text": "again"}); rec.Code != 409 || errCode(out) != "press_answered" {
		t.Fatalf("second answer: %d %v", rec.Code, out)
	}
	if rec, out := e.do("POST", "/v1/accounts/a1/presses/nope/answer", nil); rec.Code != 404 || errCode(out) != "not_found" {
		t.Fatalf("unknown press: %d %v", rec.Code, out)
	}
}

func TestButtonPressExpires(t *testing.T) {
	e := newEnv(t, func(o *core.Options) { o.PressTimeout = 20 * time.Millisecond })
	e.fake.Buttons = true
	e.connected("a1")
	if err := e.fake.Push(context.Background(), "a1", press("p2", "u1@fake")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(e.fake.AnsweredPresses(), []string{"p2::false"}) {
		if time.Now().After(deadline) {
			t.Fatalf("press not answered automatically: %v", e.fake.AnsweredPresses())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rec, out := e.do("POST", "/v1/accounts/a1/presses/p2/answer", map[string]any{"text": "late"}); rec.Code != 409 || errCode(out) != "press_expired" {
		t.Fatalf("late answer: %d %v", rec.Code, out)
	}
}

func TestScopedTokenPresses(t *testing.T) {
	e := seedScoped(t)
	e.fake.Buttons = true
	ctx := context.Background()
	if err := e.fake.Push(ctx, "a1", press("in", "u1@fake"), press("out", "u2@fake")); err != nil {
		t.Fatal(err)
	}
	alice := []any{map[string]any{"account_id": "a1", "user_id": "u1@fake"}}
	_, tok := e.mint("alice", map[string]any{"contacts": alice})
	rec, out := e.doAs(tok, "GET", "/v1/events?types=message.button", nil)
	evs, _ := out["events"].([]any)
	if rec.Code != 200 || len(evs) != 1 || evs[0].(map[string]any)["data"].(map[string]any)["press_id"] != "in" {
		t.Fatalf("scoped events: %d %s", rec.Code, rec.Body)
	}
	if rec, out := e.doAs(tok, "POST", "/v1/accounts/a1/presses/out/answer", nil); rec.Code != 404 || errCode(out) != "not_found" {
		t.Fatalf("answer outside scope: %d %v", rec.Code, out)
	}
	if rec, _ := e.doAs(tok, "POST", "/v1/accounts/a1/presses/in/answer", map[string]any{"text": "ok"}); rec.Code != 204 {
		t.Fatalf("answer inside scope: %d %s", rec.Code, rec.Body)
	}
	_, ro := e.mint("reader", map[string]any{"contacts": alice, "read_only": true})
	if err := e.fake.Push(ctx, "a1", press("in2", "u1@fake")); err != nil {
		t.Fatal(err)
	}
	if rec, out := e.doAs(ro, "POST", "/v1/accounts/a1/presses/in2/answer", nil); rec.Code != 403 || errCode(out) != "forbidden" {
		t.Fatalf("read-only answer: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(ro, "GET", "/v1/events?types=message.button", nil); rec.Code != 200 || len(out["events"].([]any)) != 2 {
		t.Fatalf("read-only sees presses: %d %s", rec.Code, rec.Body)
	}
}
