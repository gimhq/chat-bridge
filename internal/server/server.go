// Package server exposes the core over HTTP (docs/api.md).
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"gimhq/chat-bridge/internal/core"
	"gimhq/chat-bridge/internal/model"
)

const (
	defaultLimit = 50
	maxLimit     = 500
	maxWait      = 60 * time.Second
)

// Options wires dependencies into the router.
type Options struct {
	Core           *core.Core
	Token          string
	Version        string
	MaxUploadBytes int64
	Logger         *slog.Logger
	// AdapterHub, when set, is mounted at /adapter/v1 for out-of-process adapters. It does its own auth.
	AdapterHub http.Handler
	// UI is the built management app served at /ui/; nil serves a "not built" notice.
	UI fs.FS
}

type handlers struct {
	core      *core.Core
	version   string
	maxUpload int64
	log       *slog.Logger
}

// New returns the configured router.
func New(opts Options) http.Handler {
	h := &handlers{core: opts.Core, version: opts.Version, maxUpload: opts.MaxUploadBytes, log: opts.Logger}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(requestLogger(opts.Logger))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := opts.Core.Ready(r.Context()); err != nil {
			opts.Logger.Error("readyz", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	if opts.AdapterHub != nil {
		r.Mount("/adapter/v1", opts.AdapterHub)
	}
	r.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ui/", http.StatusFound) })
	r.Get("/ui", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ui/", http.StatusMovedPermanently) })
	r.Get("/ui/*", uiHandler(opts.UI))

	r.Route("/v1", func(r chi.Router) {
		r.Use(bearerAuth(opts.Token, opts.Core))
		r.Get("/status", adminOnly(h.status))
		r.Get("/platforms", adminOnly(h.platforms))

		r.Get("/tokens", adminOnly(h.listTokens))
		r.Post("/tokens", adminOnly(h.createToken))
		r.Get("/tokens/self", h.tokenSelf)
		r.Patch("/tokens/{token}", adminOnly(h.patchToken))
		r.Delete("/tokens/{token}", adminOnly(h.deleteToken))

		r.Get("/persons", h.listPersons)
		r.Post("/persons", adminOnly(h.createPerson))
		r.Get("/persons/suggest", adminOnly(h.suggestPersons))
		r.Route("/persons/{person}", func(r chi.Router) {
			r.Get("/", h.getPerson)
			r.Patch("/", adminOnly(h.patchPerson))
			r.Delete("/", adminOnly(h.deletePerson))
			r.Post("/links", adminOnly(h.linkPerson))
			r.Delete("/links/{linkAccount}/{linkUser}", adminOnly(h.unlinkPerson))
			r.Post("/merge", adminOnly(h.mergePersons))
			r.Get("/chats", h.personChats)
			r.Get("/messages", h.personMessages)
		})

		r.Get("/accounts", h.listAccounts)
		r.Post("/accounts", adminOnly(h.createAccount))
		r.Route("/accounts/{account}", func(r chi.Router) {
			r.Get("/", h.getAccount)
			r.Patch("/", adminOnly(h.patchAccount))
			r.Delete("/", adminOnly(h.deleteAccount))
			r.Patch("/self", adminOnly(h.patchSelf))
			r.Post("/logout", adminOnly(h.logout))
			r.Post("/reconnect", adminOnly(h.reconnect))

			r.Post("/login", adminOnly(h.loginStart))
			r.Get("/login", adminOnly(h.loginGet))
			r.Post("/login/submit", adminOnly(h.loginSubmit))
			r.Delete("/login", adminOnly(h.loginCancel))

			r.Get("/chats", h.listChats)
			r.Post("/chats", adminOnly(h.createChat))
			r.Post("/chats/resolve", h.resolveChat)
			r.Get("/chats/{chat}", h.getChat)
			r.Patch("/chats/{chat}", adminOnly(h.patchChat))
			r.Post("/chats/{chat}/read", h.markRead)
			r.Post("/chats/{chat}/typing", h.typing)
			r.Get("/chats/{chat}/messages", h.listMessages)
			r.Post("/chats/{chat}/messages", h.sendMessage)

			r.Get("/messages/search", h.searchMessages)
			r.Get("/messages/{msg}", h.getMessage)
			r.Patch("/messages/{msg}", h.editMessage)
			r.Delete("/messages/{msg}", h.deleteMessage)
			r.Put("/messages/{msg}/reactions/{emoji}", h.react(false))
			r.Delete("/messages/{msg}/reactions/{emoji}", h.react(true))

			r.Post("/media", h.upload)

			r.Get("/keys", adminOnly(h.keysStatus))
			r.Post("/keys/verify", adminOnly(h.keysVerify))
			r.Post("/keys/export", adminOnly(h.keysExport))
			r.Post("/keys/import", adminOnly(h.keysImport))

			r.Get("/requests", adminOnly(h.listRequests))
			r.Get("/requests/{req}", adminOnly(h.getRequest))
			r.Post("/requests/{req}/accept", adminOnly(h.answerRequest(model.ActionAccept)))
			r.Post("/requests/{req}/reject", adminOnly(h.answerRequest(model.ActionReject)))
			r.Post("/requests/{req}/ignore", adminOnly(h.answerRequest(model.ActionIgnore)))

			r.Get("/contacts", h.listContacts)
			r.Get("/contacts/{user}", h.getContact)
			r.Get("/contacts/{user}/chats", h.contactChats)
			r.Get("/contacts/{user}/messages", h.contactMessages)
			r.Patch("/contacts/{user}", adminOnly(h.patchContact))
		})

		r.Get("/media/{id}", h.getMedia)
		r.Get("/media/{id}/meta", h.getMediaMeta)
		r.Post("/media/{id}/fetch", h.fetchMedia)

		r.Get("/events", h.events)
		r.Get("/events/stream", h.eventStream)
		r.Get("/webhooks", adminOnly(h.listWebhooks))
		r.Post("/webhooks", adminOnly(h.createWebhook))
		r.Delete("/webhooks/{id}", adminOnly(h.deleteWebhook))
	})
	return r
}

// bearerAuth accepts the admin token from the configuration, which is unrestricted, or the secret
// of a scoped token, whose id then travels in the request context (core.WithToken).
func bearerAuth(admin string, c *core.Core) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(admin)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			id, err := c.Authenticate(r.Context(), got)
			if err != nil {
				writeErr(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(core.WithToken(r.Context(), id)))
		})
	}
}

// adminOnly answers 403 to scoped tokens. Every route is wrapped in it unless a scoped token is
// meant to reach it; the core then limits what such a route returns.
func adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if core.Scoped(r.Context()) {
			writeErr(w, &core.Error{Status: http.StatusForbidden, Code: "forbidden", Message: "this route needs the admin token"})
			return
		}
		next(w, r)
	}
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"bytes", ww.BytesWritten(), "duration_ms", time.Since(start).Milliseconds(), "request_id", middleware.GetReqID(r.Context()))
		})
	}
}

// param returns a decoded path parameter (chat ids carry '@' and may arrive percent-encoded).
func param(r *http.Request, name string) string {
	v := chi.URLParam(r, name)
	if u, err := url.PathUnescape(v); err == nil {
		return u
	}
	return v
}

func parseLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeErr(w, &core.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "invalid json: " + err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	ce := core.AsError(err)
	if ce.Status >= 500 {
		var raw *core.Error
		if !errors.As(err, &raw) {
			ce.Message = "internal error"
		}
	}
	if ce.Status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "5")
	}
	writeJSON(w, ce.Status, map[string]any{"error": ce})
}

func writeList(w http.ResponseWriter, key string, items any, next string) {
	body := map[string]any{key: items}
	if next != "" {
		body["next_cursor"] = next
	}
	writeJSON(w, http.StatusOK, body)
}
