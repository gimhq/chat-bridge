// Package server exposes the core over HTTP (docs/chat-api-spec.md).
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"gimhq/chat-bridge/internal/core"
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

	if opts.AdapterHub != nil {
		r.Mount("/adapter/v1", opts.AdapterHub)
	}

	r.Route("/v1", func(r chi.Router) {
		r.Use(bearerAuth(opts.Token))
		r.Get("/status", h.status)
		r.Get("/platforms", h.platforms)

		r.Get("/accounts", h.listAccounts)
		r.Post("/accounts", h.createAccount)
		r.Route("/accounts/{account}", func(r chi.Router) {
			r.Get("/", h.getAccount)
			r.Patch("/", h.patchAccount)
			r.Delete("/", h.deleteAccount)
			r.Patch("/self", h.patchSelf)
			r.Post("/logout", h.logout)
			r.Post("/reconnect", h.reconnect)

			r.Post("/login", h.loginStart)
			r.Get("/login", h.loginGet)
			r.Post("/login/submit", h.loginSubmit)
			r.Delete("/login", h.loginCancel)

			r.Get("/chats", h.listChats)
			r.Post("/chats/resolve", h.resolveChat)
			r.Get("/chats/{chat}", h.getChat)
			r.Patch("/chats/{chat}", h.patchChat)
			r.Post("/chats/{chat}/read", h.markRead)
			r.Post("/chats/{chat}/typing", h.typing)
			r.Get("/chats/{chat}/messages", h.listMessages)
			r.Post("/chats/{chat}/messages", h.sendMessage)

			r.Get("/messages/{msg}", h.getMessage)
			r.Patch("/messages/{msg}", h.editMessage)
			r.Delete("/messages/{msg}", h.deleteMessage)
			r.Put("/messages/{msg}/reactions/{emoji}", h.react(false))
			r.Delete("/messages/{msg}/reactions/{emoji}", h.react(true))

			r.Post("/media", h.upload)

			r.Get("/keys", h.keysStatus)
			r.Post("/keys/verify", h.keysVerify)
			r.Post("/keys/export", h.keysExport)
			r.Post("/keys/import", h.keysImport)

			r.Get("/contacts", h.listContacts)
			r.Get("/contacts/{user}", h.getContact)
			r.Patch("/contacts/{user}", h.patchContact)
		})

		r.Get("/media/{id}", h.getMedia)
		r.Get("/media/{id}/meta", h.getMediaMeta)
		r.Post("/media/{id}/fetch", h.fetchMedia)

		r.Get("/events", h.events)
		r.Get("/events/stream", h.eventStream)
		r.Get("/webhooks", h.listWebhooks)
		r.Post("/webhooks", h.createWebhook)
		r.Delete("/webhooks/{id}", h.deleteWebhook)
	})
	return r
}

func bearerAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeErr(w, &core.Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "invalid or missing bearer token"})
				return
			}
			next.ServeHTTP(w, r)
		})
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
