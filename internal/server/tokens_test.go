package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

// seedScoped connects a1 and stores two direct chats (Alice u1, Bob u2) and a group Alice talks in.
func seedScoped(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.connected("a1")
	now := time.Now().UTC()
	msg := func(id, chat, sender, kind, text, name string) adapter.Event {
		return adapter.Event{Kind: adapter.EvMessage,
			Message: &model.Message{ID: id, ChatID: chat, Sender: model.Sender{ID: sender}, Timestamp: now, Content: model.Content{Type: "text", Text: text}},
			Chat:    &model.Chat{ID: chat, Kind: kind, Name: name},
			Sender:  &model.Contact{ID: sender, Names: model.Names{Profile: sender}}}
	}
	if err := e.fake.Push(context.Background(), "a1",
		msg("m1", "u1@fake", "u1@fake", model.ChatDirect, "hello from alice", ""),
		msg("m2", "u2@fake", "u2@fake", model.ChatDirect, "hello from bob", ""),
		msg("g1", "team@fake", "u1@fake", model.ChatGroup, "team talk", "Team"),
	); err != nil {
		t.Fatal(err)
	}
	return e
}

// mint creates a scoped token as the admin and returns its id and secret.
func (e *env) mint(name string, scope map[string]any) (string, string) {
	e.t.Helper()
	rec, out := e.do("POST", "/v1/tokens", map[string]any{"name": name, "scope": scope})
	if rec.Code != 201 || out["token"] == nil || out["id"] == nil {
		e.t.Fatalf("create token: %d %s", rec.Code, rec.Body)
	}
	return out["id"].(string), out["token"].(string)
}

// multipartFile builds a one-file multipart body and returns it with its content type.
func multipartFile(t *testing.T, field, name, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

// ids joins the "id" of every object in out[key], in order.
func ids(out map[string]any, key string) string {
	list, _ := out[key].([]any)
	var got []string
	for _, v := range list {
		got = append(got, v.(map[string]any)["id"].(string))
	}
	return strings.Join(got, ",")
}

func TestScopedTokenSeesOnlyItsContacts(t *testing.T) {
	e := seedScoped(t)
	_, tok := e.mint("alice-bot", map[string]any{"contacts": []any{map[string]any{"account_id": "a1", "user_id": "u1@fake"}}})

	if rec, out := e.doAs(tok, "GET", "/v1/tokens/self", nil); rec.Code != 200 || out["name"] != "alice-bot" || out["token"] != nil {
		t.Fatalf("self: %d %v", rec.Code, out)
	}
	if rec, out := e.do("GET", "/v1/tokens/self", nil); rec.Code != 200 || out["admin"] != true {
		t.Fatalf("admin self: %d %v", rec.Code, out)
	}
	rec, out := e.doAs(tok, "GET", "/v1/accounts", nil)
	if rec.Code != 200 || ids(out, "accounts") != "a1" {
		t.Fatalf("accounts: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1", nil); rec.Code != 200 || out["config"] != nil || out["device"] != nil || out["stats"] != nil {
		t.Fatalf("account view must be the list view: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/chats", nil); rec.Code != 200 || ids(out, "chats") != "u1@fake" {
		t.Fatalf("chats: %d %v", rec.Code, out)
	}

	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/v1/accounts/a1/chats/u1@fake", nil, 200},
		{"GET", "/v1/accounts/a1/chats/u2@fake", nil, 404},
		{"GET", "/v1/accounts/a1/chats/team@fake", nil, 404},
		{"GET", "/v1/accounts/a1/chats/u1@fake/messages", nil, 200},
		{"GET", "/v1/accounts/a1/chats/u2@fake/messages", nil, 404},
		{"POST", "/v1/accounts/a1/chats/u1@fake/messages", map[string]any{"content": map[string]any{"type": "text", "text": "hi"}}, 201},
		{"POST", "/v1/accounts/a1/chats/u2@fake/messages", map[string]any{"content": map[string]any{"type": "text", "text": "hi"}}, 404},
		{"POST", "/v1/accounts/a1/chats/u2@fake/read", nil, 404},
		{"POST", "/v1/accounts/a1/chats/u2@fake/typing", map[string]any{"state": "typing"}, 404},
		{"GET", "/v1/accounts/a1/messages/m1", nil, 200},
		{"GET", "/v1/accounts/a1/messages/m2", nil, 404},
		{"GET", "/v1/accounts/a1/messages/g1", nil, 404},
		{"DELETE", "/v1/accounts/a1/messages/m2", nil, 404},
		{"PUT", "/v1/accounts/a1/messages/m2/reactions/x", nil, 404},
		{"GET", "/v1/accounts/a1/contacts/u1@fake", nil, 200},
		{"GET", "/v1/accounts/a1/contacts/u2@fake", nil, 404},
		{"GET", "/v1/accounts/a1/contacts/u2@fake/chats", nil, 404},
		{"GET", "/v1/accounts/a1/contacts/u2@fake/messages", nil, 404},
		{"GET", "/v1/accounts/nope/chats", nil, 404},
	} {
		if rec, _ := e.doAs(tok, c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s %s: got %d, want %d (%s)", c.method, c.path, rec.Code, c.want, rec.Body)
		}
	}
	if len(e.fake.Sent) != 1 || e.fake.Sent[0].ChatID != "u1@fake" {
		t.Fatalf("only the in-scope send may reach the adapter: %+v", e.fake.Sent)
	}

	// Search and the contact views stay inside the allowed chats.
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/messages/search?q=hello", nil); rec.Code != 200 || ids(out, "messages") != "m1" {
		t.Fatalf("search: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/messages/search?q=team", nil); rec.Code != 200 || ids(out, "messages") != "" {
		t.Fatalf("search must not reach the group: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/messages/search?q=team&chat=team@fake", nil); rec.Code != 200 || ids(out, "messages") != "" {
		t.Fatalf("search with an out-of-scope chat: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/contacts", nil); rec.Code != 200 || ids(out, "contacts") != "u1@fake" {
		t.Fatalf("contacts: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/contacts/u1@fake/chats", nil); rec.Code != 200 || ids(out, "chats") != "u1@fake" {
		t.Fatalf("contact chats: %d %v", rec.Code, out)
	}
	rec, out = e.doAs(tok, "GET", "/v1/accounts/a1/contacts/u1@fake/messages?scope=all", nil)
	if rec.Code != 200 || strings.Contains(ids(out, "messages"), "g1") {
		t.Fatalf("scope=all must not add group messages: %d %v", rec.Code, out)
	}

	// Events: nothing about Bob or the group, no account detail.
	rec, _ = e.doAs(tok, "GET", "/v1/events?limit=500", nil)
	if rec.Code != 200 {
		t.Fatalf("events: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"u2@fake", "team@fake", "hello from bob", `"config"`, `"device"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("events leak %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "hello from alice") {
		t.Fatalf("events must carry the in-scope message: %s", body)
	}

	// Media outside a visible message is hidden.
	up, ct := multipartFile(t, "file", "a.txt", "secret")
	rec, out = e.do("POST", "/v1/accounts/a1/media", up, ct)
	if rec.Code != 201 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	mediaID := out["media_id"].(string)
	for _, p := range []string{"/v1/media/" + mediaID, "/v1/media/" + mediaID + "/meta"} {
		if rec, _ := e.doAs(tok, "GET", p, nil); rec.Code != 404 {
			t.Errorf("GET %s: %d", p, rec.Code)
		}
	}
}

func TestScopedTokenChatsAndPersons(t *testing.T) {
	e := seedScoped(t)
	id, tok := e.mint("team-bot", map[string]any{"chats": []any{map[string]any{"account_id": "a1", "chat_id": "team@fake"}}})
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/chats", nil); rec.Code != 200 || ids(out, "chats") != "team@fake" {
		t.Fatalf("chats: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/messages/search?q=team", nil); rec.Code != 200 || ids(out, "messages") != "g1" {
		t.Fatalf("search: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/contacts", nil); rec.Code != 200 || ids(out, "contacts") != "" {
		t.Fatalf("a chat scope lists no contacts: %d %v", rec.Code, out)
	}

	// The admin widens the scope; the next request sees it.
	rec, out := e.do("PATCH", "/v1/tokens/"+id, map[string]any{"scope": map[string]any{
		"chats":    []any{map[string]any{"account_id": "a1", "chat_id": "team@fake"}},
		"contacts": []any{map[string]any{"account_id": "a1", "user_id": "u2@fake"}}}})
	if rec.Code != 200 || out["token"] != nil {
		t.Fatalf("patch: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/chats", nil); rec.Code != 200 || len(out["chats"].([]any)) != 2 {
		t.Fatalf("chats after patch: %d %v", rec.Code, out)
	}

	// Persons: a listed person brings its linked contacts and their direct chats.
	rec, out = e.do("POST", "/v1/persons", map[string]any{"name": "Bob", "links": []any{map[string]any{"account_id": "a1", "user_id": "u2@fake"}}})
	if rec.Code != 201 {
		t.Fatalf("person: %d %s", rec.Code, rec.Body)
	}
	bob := out["id"].(string)
	rec, out = e.do("POST", "/v1/persons", map[string]any{"name": "Alice", "links": []any{map[string]any{"account_id": "a1", "user_id": "u1@fake"}}})
	if rec.Code != 201 {
		t.Fatalf("person: %d %s", rec.Code, rec.Body)
	}
	alice := out["id"].(string)
	_, ptok := e.mint("bob-bot", map[string]any{"persons": []any{bob}})
	if rec, out := e.doAs(ptok, "GET", "/v1/accounts/a1/chats", nil); rec.Code != 200 || ids(out, "chats") != "u2@fake" {
		t.Fatalf("person chats: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(ptok, "GET", "/v1/persons", nil); rec.Code != 200 || ids(out, "persons") != bob {
		t.Fatalf("persons: %d %v", rec.Code, out)
	}
	for path, want := range map[string]int{
		"/v1/persons/" + bob: 200, "/v1/persons/" + bob + "/chats": 200, "/v1/persons/" + bob + "/messages": 200,
		"/v1/persons/" + alice: 404, "/v1/persons/" + alice + "/chats": 404, "/v1/persons/" + alice + "/messages": 404,
		"/v1/events?person=" + alice: 404,
	} {
		if rec, _ := e.doAs(ptok, "GET", path, nil); rec.Code != want {
			t.Errorf("GET %s: got %d, want %d", path, rec.Code, want)
		}
	}

	// Validation and lifecycle.
	if rec, _ := e.do("POST", "/v1/tokens", map[string]any{"scope": map[string]any{}}); rec.Code != 400 {
		t.Fatalf("name is required: %d", rec.Code)
	}
	if rec, _ := e.do("POST", "/v1/tokens", map[string]any{"name": "x", "scope": map[string]any{"persons": []any{"per_missing"}}}); rec.Code != 400 {
		t.Fatalf("unknown person: %d", rec.Code)
	}
	if rec, out := e.do("GET", "/v1/tokens", nil); rec.Code != 200 || len(out["tokens"].([]any)) != 2 || strings.Contains(rec.Body.String(), ptok) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := e.do("DELETE", "/v1/tokens/"+id, nil); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts", nil); rec.Code != 401 || errCode(out) != "unauthorized" {
		t.Fatalf("revoked token: %d %v", rec.Code, out)
	}
	if rec, _ := e.do("DELETE", "/v1/tokens/"+id, nil); rec.Code != 404 {
		t.Fatalf("delete twice: %d", rec.Code)
	}
}

// scopedRoutes are the only routes a scoped token may reach; every other /v1 route must answer
// 403. A route added to the router without a decision here fails the test.
var scopedRoutes = map[string]bool{
	"GET /v1/tokens/self":                                            true,
	"GET /v1/accounts":                                               true,
	"GET /v1/accounts/{account}/":                                    true,
	"GET /v1/accounts/{account}/chats":                               true,
	"POST /v1/accounts/{account}/chats/resolve":                      true,
	"GET /v1/accounts/{account}/chats/{chat}":                        true,
	"POST /v1/accounts/{account}/chats/{chat}/read":                  true,
	"POST /v1/accounts/{account}/chats/{chat}/typing":                true,
	"GET /v1/accounts/{account}/chats/{chat}/messages":               true,
	"POST /v1/accounts/{account}/chats/{chat}/messages":              true,
	"GET /v1/accounts/{account}/messages/search":                     true,
	"GET /v1/accounts/{account}/messages/{msg}":                      true,
	"PATCH /v1/accounts/{account}/messages/{msg}":                    true,
	"DELETE /v1/accounts/{account}/messages/{msg}":                   true,
	"PUT /v1/accounts/{account}/messages/{msg}/reactions/{emoji}":    true,
	"DELETE /v1/accounts/{account}/messages/{msg}/reactions/{emoji}": true,
	"POST /v1/accounts/{account}/media":                              true,
	"GET /v1/accounts/{account}/contacts":                            true,
	"GET /v1/accounts/{account}/contacts/{user}":                     true,
	"GET /v1/accounts/{account}/contacts/{user}/chats":               true,
	"GET /v1/accounts/{account}/contacts/{user}/messages":            true,
	"GET /v1/persons":                                                true,
	"GET /v1/persons/{person}/":                                      true,
	"GET /v1/persons/{person}/chats":                                 true,
	"GET /v1/persons/{person}/messages":                              true,
	"GET /v1/media/{id}":                                             true,
	"GET /v1/media/{id}/meta":                                        true,
	"POST /v1/media/{id}/fetch":                                      true,
	"GET /v1/events":                                                 true,
	"GET /v1/events/stream":                                          true,
}

func TestScopedTokenRouteAllowlist(t *testing.T) {
	e := seedScoped(t)
	_, tok := e.mint("walker", map[string]any{"contacts": []any{map[string]any{"account_id": "a1", "user_id": "u1@fake"}}})
	param := regexp.MustCompile(`\{[^}]+\}`)
	seen := map[string]bool{}
	err := chi.Walk(e.h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.ReplaceAll(route, "/*/", "/")
		if !strings.HasPrefix(route, "/v1/") {
			return nil
		}
		key := method + " " + route
		seen[key] = true
		if route == "/v1/events/stream" { // streams until the client leaves; covered by the allowlist entry
			return nil
		}
		rec, _ := e.doAs(tok, method, param.ReplaceAllString(route, "x"), nil)
		switch {
		case scopedRoutes[key] && rec.Code == http.StatusForbidden:
			t.Errorf("%s: a scoped route answered 403", key)
		case !scopedRoutes[key] && rec.Code != http.StatusForbidden:
			t.Errorf("%s: got %d, want 403 for a scoped token", key, rec.Code)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range scopedRoutes {
		if !seen[key] {
			t.Errorf("%s is in the allowlist but not registered", key)
		}
	}
}

func TestReadOnlyToken(t *testing.T) {
	e := seedScoped(t)
	alice := []any{map[string]any{"account_id": "a1", "user_id": "u1@fake"}}
	id, tok := e.mint("reader", map[string]any{"contacts": alice, "read_only": true})
	if rec, out := e.doAs(tok, "GET", "/v1/tokens/self", nil); rec.Code != 200 || out["scope"].(map[string]any)["read_only"] != true {
		t.Fatalf("self: %d %v", rec.Code, out)
	}

	text := map[string]any{"content": map[string]any{"type": "text", "text": "hi"}}
	up, ct := multipartFile(t, "file", "a.txt", "x")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/accounts/a1/chats/u1@fake/messages", text},
		{"PATCH", "/v1/accounts/a1/messages/m1", text},
		{"DELETE", "/v1/accounts/a1/messages/m1", nil},
		{"PUT", "/v1/accounts/a1/messages/m1/reactions/x", nil},
		{"DELETE", "/v1/accounts/a1/messages/m1/reactions/x", nil},
		{"POST", "/v1/accounts/a1/chats/u1@fake/typing", map[string]any{"state": "typing"}},
		{"POST", "/v1/accounts/a1/chats/u1@fake/read", nil},
		{"POST", "/v1/accounts/a1/chats/resolve", map[string]any{"handle": "u1@fake"}},
	} {
		if rec, out := e.doAs(tok, c.method, c.path, c.body); rec.Code != 403 || errCode(out) != "forbidden" {
			t.Errorf("%s %s: got %d %v, want 403 forbidden", c.method, c.path, rec.Code, out)
		}
	}
	if rec, out := e.doAs(tok, "POST", "/v1/accounts/a1/media", up, ct); rec.Code != 403 || errCode(out) != "forbidden" {
		t.Errorf("upload: got %d %v, want 403 forbidden", rec.Code, out)
	}
	if len(e.fake.Sent)+len(e.fake.Deleted)+len(e.fake.Reacted)+len(e.fake.Read) != 0 {
		t.Fatalf("a read-only token reached the adapter: %+v %v %v %v", e.fake.Sent, e.fake.Deleted, e.fake.Reacted, e.fake.Read)
	}
	// Reading is unchanged.
	for _, p := range []string{"/v1/accounts/a1/chats", "/v1/accounts/a1/chats/u1@fake/messages", "/v1/accounts/a1/messages/search?q=hello", "/v1/events"} {
		if rec, _ := e.doAs(tok, "GET", p, nil); rec.Code != 200 {
			t.Errorf("GET %s: %d", p, rec.Code)
		}
	}
	// Lifting the switch takes effect on the next request.
	if rec, _ := e.do("PATCH", "/v1/tokens/"+id, map[string]any{"scope": map[string]any{"contacts": alice}}); rec.Code != 200 {
		t.Fatalf("patch: %d", rec.Code)
	}
	if rec, _ := e.doAs(tok, "POST", "/v1/accounts/a1/chats/u1@fake/messages", text); rec.Code != 201 {
		t.Fatalf("send after lifting read_only: %d %s", rec.Code, rec.Body)
	}
}

func TestScopedTokenStartsChat(t *testing.T) {
	e := seedScoped(t)
	// Carol is known to the account (phone +5) but there is no chat with her yet; Dave is unknown.
	if err := e.fake.Push(context.Background(), "a1", adapter.Event{Kind: adapter.EvContact,
		Contact: &model.Contact{ID: "5@fake", Phone: "+5", Handle: "+5", Names: model.Names{Profile: "Carol"}}}); err != nil {
		t.Fatal(err)
	}
	_, tok := e.mint("carol-bot", map[string]any{"contacts": []any{
		map[string]any{"account_id": "a1", "user_id": "5@fake"}, map[string]any{"account_id": "a1", "user_id": "7@fake"}}})
	text := map[string]any{"content": map[string]any{"type": "text", "text": "hi"}}

	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/chats", nil); rec.Code != 200 || ids(out, "chats") != "" {
		t.Fatalf("no chat yet: %d %v", rec.Code, out)
	}
	if rec, _ := e.doAs(tok, "POST", "/v1/accounts/a1/chats/5@fake/messages", text); rec.Code != 404 {
		t.Fatalf("send before resolve: %d", rec.Code)
	}
	// Handles outside the scope are refused: another contact's phone, an unknown number.
	for _, h := range []string{"+1", "+99", "u2@fake", ""} {
		if rec, _ := e.doAs(tok, "POST", "/v1/accounts/a1/chats/resolve", map[string]any{"handle": h}); rec.Code != 404 && rec.Code != 400 {
			t.Errorf("resolve %q: %d", h, rec.Code)
		}
	}
	// The stored phone of an allowed contact resolves, and the chat becomes reachable.
	rec, out := e.doAs(tok, "POST", "/v1/accounts/a1/chats/resolve", map[string]any{"handle": "+5"})
	if rec.Code != 200 || out["chat_id"] != "5@fake" || out["user_id"] != "5@fake" {
		t.Fatalf("resolve: %d %v", rec.Code, out)
	}
	if rec, out := e.doAs(tok, "GET", "/v1/accounts/a1/chats", nil); rec.Code != 200 || ids(out, "chats") != "5@fake" {
		t.Fatalf("chats after resolve: %d %v", rec.Code, out)
	}
	if rec, _ := e.doAs(tok, "POST", "/v1/accounts/a1/chats/5@fake/messages", text); rec.Code != 201 {
		t.Fatalf("send after resolve: %d %s", rec.Code, rec.Body)
	}
	// A listed contact the store has never seen resolves by its user id only when the platform
	// answers with that same user: the fake maps "7@fake" elsewhere, so it stays hidden.
	if rec, _ := e.doAs(tok, "POST", "/v1/accounts/a1/chats/resolve", map[string]any{"handle": "7@fake"}); rec.Code != 404 {
		t.Fatalf("resolve to another user: %d", rec.Code)
	}
	// The admin token resolves anything, as before.
	if rec, out := e.do("POST", "/v1/accounts/a1/chats/resolve", map[string]any{"handle": "+99"}); rec.Code != 200 || out["chat_id"] != "99@fake" {
		t.Fatalf("admin resolve: %d %v", rec.Code, out)
	}
}
