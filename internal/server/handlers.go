package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/core"
	"gimhq/chat-bridge/internal/model"
	"gimhq/chat-bridge/internal/store"
)

// --- meta ---

func (h *handlers) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.core.Status(r.Context(), h.version)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *handlers) platforms(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"platforms": h.core.Platforms()})
}

// --- accounts ---

func (h *handlers) listAccounts(w http.ResponseWriter, r *http.Request) {
	accs, err := h.core.ListAccounts(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if accs == nil {
		accs = []model.Account{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accs})
}

func (h *handlers) createAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string          `json:"id"`
		Platform string          `json:"platform"`
		Adapter  string          `json:"adapter"`
		Config   json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	acc, err := h.core.CreateAccount(r.Context(), req.ID, req.Platform, req.Adapter, req.Config)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, acc)
}

func (h *handlers) getAccount(w http.ResponseWriter, r *http.Request) {
	acc, err := h.core.GetAccount(r.Context(), param(r, "account"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}

func (h *handlers) patchAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config  json.RawMessage `json:"config"`
		Adapter string          `json:"adapter"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Config) == 0 && req.Adapter == "" {
		writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "config or adapter is required"})
		return
	}
	var acc model.Account
	var err error
	if req.Adapter != "" {
		if acc, err = h.core.Rebind(r.Context(), param(r, "account"), req.Adapter); err != nil {
			writeErr(w, err)
			return
		}
	}
	if len(req.Config) > 0 {
		if acc, err = h.core.UpdateConfig(r.Context(), param(r, "account"), req.Config); err != nil {
			writeErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, acc)
}

func (h *handlers) patchSelf(w http.ResponseWriter, r *http.Request) {
	var req core.SelfPatch
	if !decodeJSON(w, r, &req) {
		return
	}
	acc, err := h.core.UpdateSelf(r.Context(), param(r, "account"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}

func (h *handlers) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := h.core.DeleteAccount(r.Context(), param(r, "account")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	acc, err := h.core.Logout(r.Context(), param(r, "account"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}

func (h *handlers) reconnect(w http.ResponseWriter, r *http.Request) {
	acc, err := h.core.Reconnect(r.Context(), param(r, "account"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}

// --- login ---

func (h *handlers) loginStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Flow string `json:"flow"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	step, err := h.core.LoginStart(r.Context(), param(r, "account"), req.Flow)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, step)
}

func (h *handlers) loginGet(w http.ResponseWriter, r *http.Request) {
	step, err := h.core.LoginGet(r.Context(), param(r, "account"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, step)
}

func (h *handlers) loginSubmit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Fields map[string]string `json:"fields"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	step, err := h.core.LoginSubmit(r.Context(), param(r, "account"), req.Fields)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, step)
}

func (h *handlers) loginCancel(w http.ResponseWriter, r *http.Request) {
	if err := h.core.LoginCancel(r.Context(), param(r, "account")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- chats ---

func (h *handlers) listChats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.ChatFilter{Kind: q.Get("kind"), Tag: q.Get("tag"), Person: q.Get("person")}
	if v := q.Get("archived"); v != "" {
		b := v == "1" || v == "true"
		f.Archived = &b
	}
	chats, next, err := h.core.ListChats(r.Context(), param(r, "account"), f, q.Get("cursor"), parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeList(w, "chats", chats, next)
}

func (h *handlers) createChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string   `json:"kind"`
		Name    string   `json:"name"`
		Members []string `json:"members"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, err := h.core.CreateChat(r.Context(), param(r, "account"), adapter.CreateChatRequest{Kind: req.Kind, Name: req.Name, Members: req.Members})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ch)
}

func (h *handlers) searchMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	msgs, next, err := h.core.SearchMessages(r.Context(), param(r, "account"), core.SearchQuery{Q: q.Get("q"), ChatID: q.Get("chat"), Cursor: q.Get("cursor"), Limit: parseLimit(r)})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeList(w, "messages", msgs, next)
}

func (h *handlers) resolveChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Handle string `json:"handle"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.core.ResolveChat(r.Context(), param(r, "account"), req.Handle)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handlers) getChat(w http.ResponseWriter, r *http.Request) {
	ch, err := h.core.GetChat(r.Context(), param(r, "account"), param(r, "chat"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *handlers) patchChat(w http.ResponseWriter, r *http.Request) {
	var req core.ChatPatch
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, err := h.core.PatchChat(r.Context(), param(r, "account"), param(r, "chat"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *handlers) markRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UpTo string `json:"up_to"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	if err := h.core.MarkRead(r.Context(), param(r, "account"), param(r, "chat"), req.UpTo); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) typing(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.core.Typing(r.Context(), param(r, "account"), param(r, "chat"), req.State); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- messages ---

func (h *handlers) listMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mq := core.MessageQuery{Cursor: q.Get("cursor"), Limit: parseLimit(r), Backfill: q.Get("backfill") == "1" || q.Get("backfill") == "true"}
	var err error
	if mq.Before, err = parseTime(q.Get("before")); err != nil {
		writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "before: " + err.Error()})
		return
	}
	if mq.After, err = parseTime(q.Get("after")); err != nil {
		writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "after: " + err.Error()})
		return
	}
	msgs, next, err := h.core.ListMessages(r.Context(), param(r, "account"), param(r, "chat"), mq)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeList(w, "messages", msgs, next)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(n, 0), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be RFC 3339 or unix seconds")
	}
	return t, nil
}

func (h *handlers) sendMessage(w http.ResponseWriter, r *http.Request) {
	var req model.SendRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	msg, replay, err := h.core.Send(r.Context(), param(r, "account"), param(r, "chat"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	writeJSON(w, status, msg)
}

func (h *handlers) getMessage(w http.ResponseWriter, r *http.Request) {
	msg, err := h.core.GetMessage(r.Context(), param(r, "account"), param(r, "msg"), r.URL.Query().Get("raw") == "1")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (h *handlers) editMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content model.Content `json:"content"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	msg, err := h.core.Edit(r.Context(), param(r, "account"), param(r, "msg"), req.Content)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (h *handlers) deleteMessage(w http.ResponseWriter, r *http.Request) {
	msg, err := h.core.Delete(r.Context(), param(r, "account"), param(r, "msg"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (h *handlers) react(remove bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h.core.React(r.Context(), param(r, "account"), param(r, "msg"), param(r, "emoji"), remove); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- media ---

func (h *handlers) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, &core.Error{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "invalid multipart form or file too large"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "file is required"})
		return
	}
	defer func() { _ = file.Close() }()
	att, err := h.core.Upload(r.Context(), param(r, "account"), file, adapter.MediaMeta{
		Mime: header.Header.Get("Content-Type"), FileName: header.Filename,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

func (h *handlers) getMedia(w http.ResponseWriter, r *http.Request) {
	mf, err := h.core.GetMedia(r.Context(), param(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if mf.Path == "" {
		writeErr(w, &core.Error{Status: http.StatusConflict, Code: "conflict", Message: "media is " + mf.Attachment.State,
			Details: map[string]any{"state": mf.Attachment.State}})
		return
	}
	w.Header().Set("Content-Type", mf.Attachment.Mime)
	w.Header().Set("ETag", `"`+mf.Attachment.SHA256+`"`)
	if mf.Attachment.FileName != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(mf.Attachment.FileName, `"`, "")+`"`)
	}
	http.ServeFile(w, r, mf.Path)
}

func (h *handlers) getMediaMeta(w http.ResponseWriter, r *http.Request) {
	mf, err := h.core.GetMedia(r.Context(), param(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mf.Attachment)
}

func (h *handlers) fetchMedia(w http.ResponseWriter, r *http.Request) {
	att, err := h.core.FetchMedia(r.Context(), param(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, att)
}

// --- keys ---

func (h *handlers) keysStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.core.KeysStatus(r.Context(), param(r, "account"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *handlers) keysVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RecoveryKey string `json:"recovery_key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := h.core.KeysVerify(r.Context(), param(r, "account"), req.RecoveryKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handlers) keysExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Passphrase string `json:"passphrase"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	data, err := h.core.KeysExport(r.Context(), param(r, "account"), req.Passphrase)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+param(r, "account")+`-keys.txt"`)
	_, _ = w.Write(data)
}

func (h *handlers) keysImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "multipart form with file and passphrase expected"})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "file is required"})
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		writeErr(w, err)
		return
	}
	n, err := h.core.KeysImport(r.Context(), param(r, "account"), r.FormValue("passphrase"), data)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"sessions_imported": n})
}

// --- contacts ---

func (h *handlers) listContacts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cs, next, err := h.core.ListContacts(r.Context(), param(r, "account"), q.Get("q"), q.Get("cursor"), parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if cs == nil {
		cs = []model.Contact{}
	}
	writeList(w, "contacts", cs, next)
}

func (h *handlers) getContact(w http.ResponseWriter, r *http.Request) {
	c, err := h.core.GetContact(r.Context(), param(r, "account"), param(r, "user"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *handlers) patchContact(w http.ResponseWriter, r *http.Request) {
	var req core.ContactPatch
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := h.core.PatchContact(r.Context(), param(r, "account"), param(r, "user"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// --- persons ---

func (h *handlers) listPersons(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, next, err := h.core.ListPersons(r.Context(), store.PersonFilter{Tag: q.Get("tag"), Q: q.Get("q")}, q.Get("cursor"), parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeList(w, "persons", out, next)
}

func (h *handlers) createPerson(w http.ResponseWriter, r *http.Request) {
	var in core.PersonInput
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := h.core.CreatePerson(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *handlers) suggestPersons(w http.ResponseWriter, r *http.Request) {
	out, err := h.core.SuggestPersons(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

func (h *handlers) getPerson(w http.ResponseWriter, r *http.Request) {
	p, err := h.core.GetPerson(r.Context(), param(r, "person"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handlers) patchPerson(w http.ResponseWriter, r *http.Request) {
	var in core.PersonPatch
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := h.core.PatchPerson(r.Context(), param(r, "person"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handlers) deletePerson(w http.ResponseWriter, r *http.Request) {
	if err := h.core.DeletePerson(r.Context(), param(r, "person")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) linkPerson(w http.ResponseWriter, r *http.Request) {
	var l store.LinkRef
	if !decodeJSON(w, r, &l) {
		return
	}
	if l.AccountID == "" || l.UserID == "" {
		writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "account_id and user_id are required"})
		return
	}
	p, err := h.core.LinkPerson(r.Context(), param(r, "person"), l)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handlers) unlinkPerson(w http.ResponseWriter, r *http.Request) {
	p, err := h.core.UnlinkPerson(r.Context(), param(r, "person"), store.LinkRef{AccountID: param(r, "linkAccount"), UserID: param(r, "linkUser")})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handlers) mergePersons(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From []string `json:"from"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := h.core.MergePersons(r.Context(), param(r, "person"), in.From)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handlers) personChats(w http.ResponseWriter, r *http.Request) {
	out, err := h.core.PersonChats(r.Context(), param(r, "person"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": out})
}

func (h *handlers) personMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, next, err := h.core.PersonMessages(r.Context(), param(r, "person"), q.Get("scope"), q.Get("cursor"), parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeList(w, "messages", out, next)
}

// --- requests ---

func (h *handlers) listRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, next, err := h.core.ListRequests(r.Context(), param(r, "account"), store.RequestFilter{Kind: q.Get("kind"), State: q.Get("state")}, q.Get("cursor"), parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeList(w, "requests", out, next)
}

func (h *handlers) getRequest(w http.ResponseWriter, r *http.Request) {
	out, err := h.core.GetRequest(r.Context(), param(r, "account"), param(r, "req"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handlers) answerRequest(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		if r.ContentLength > 0 && !decodeJSON(w, r, &body) {
			return
		}
		out, err := h.core.AnswerRequest(r.Context(), param(r, "account"), param(r, "req"), action, body.Reason)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// --- events ---

func (h *handlers) eventFilter(r *http.Request) (store.EventFilter, error) {
	q := r.URL.Query()
	f := store.EventFilter{AccountID: q.Get("account")}
	if t := q.Get("types"); t != "" {
		f.Types = strings.Split(t, ",")
	}
	if p := q.Get("person"); p != "" {
		scope, err := h.core.PersonScope(r.Context(), p)
		if err != nil {
			return f, err
		}
		f.Person = scope
	}
	return f, nil
}

func (h *handlers) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wait := time.Duration(0)
	if s := q.Get("wait"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "wait must be seconds"})
			return
		}
		wait = min(time.Duration(n)*time.Second, maxWait)
	}
	filter, err := h.eventFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	evs, next, err := h.core.WaitEvents(r.Context(), q.Get("cursor"), filter, parseLimit(r), wait)
	if err != nil {
		writeErr(w, err)
		return
	}
	if evs == nil {
		evs = []model.Event{}
	}
	writeList(w, "events", evs, next)
}

func (h *handlers) eventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, &core.Error{Status: http.StatusInternalServerError, Code: "internal", Message: "streaming unsupported"})
		return
	}
	filter, err := h.eventFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		cursor = last
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	}()
	defer close(done)
	_ = h.core.Stream(ctx, cursor, filter, func(ev model.Event) error {
		b, _ := json.Marshal(ev)
		if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
}

// --- webhooks ---

func (h *handlers) listWebhooks(w http.ResponseWriter, r *http.Request) {
	ws, err := h.core.ListWebhooks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": ws})
}

func (h *handlers) createWebhook(w http.ResponseWriter, r *http.Request) {
	var req core.WebhookInput
	if !decodeJSON(w, r, &req) {
		return
	}
	wh, err := h.core.CreateWebhook(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wh)
}

func (h *handlers) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if err := h.core.DeleteWebhook(r.Context(), param(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
