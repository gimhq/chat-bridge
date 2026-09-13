// Package remote hosts out-of-process adapters over WebSocket + JSON-RPC 2.0
// (docs/adapter-protocol.md). Any language can implement a platform by connecting here.
package remote

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/core"
)

const (
	protocolVersion = 1
	pingEvery       = 20 * time.Second
	pingTimeout     = 60 * time.Second
	helloTimeout    = 30 * time.Second

	closeBadProtocol   websocket.StatusCode = 4001
	closeDuplicate     websocket.StatusCode = 4002
	closeAttachFailure websocket.StatusCode = 4003
)

// Hub is the HTTP handler mounted at /adapter/v1.
type Hub struct {
	core      *core.Core
	token     string
	publicURL string
	log       *slog.Logger
	router    chi.Router

	mu    sync.Mutex
	conns map[string]*Adapter // by platform
}

// NewHub builds the handler. publicURL (optional) is the externally reachable base of the core,
// used in media URLs handed to adapters; when empty it is derived from each request.
func NewHub(c *core.Core, token, publicURL string, log *slog.Logger) *Hub {
	h := &Hub{core: c, token: token, publicURL: strings.TrimRight(publicURL, "/"), log: log, conns: map[string]*Adapter{}}
	r := chi.NewRouter()
	r.Use(h.auth)
	r.Get("/", h.serveWS)
	r.Put("/media", h.putMedia)
	r.Get("/media/{id}", h.getMedia)
	h.router = r
	return h
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.router.ServeHTTP(w, r) }

func (h *Hub) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "invalid or missing adapter token"}})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Connected lists the platform/instance keys currently served by remote adapters.
func (h *Hub) Connected() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.conns))
	for p := range h.conns {
		out = append(out, p)
	}
	return out
}

// serveWS upgrades, runs the hello handshake, attaches the adapter, and blocks until it leaves.
func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	// Adapters are non-browser clients authenticated by the bearer token above; the Origin
	// header carries no trust here, so any origin is accepted (this is not TLS verification).
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		h.log.Warn("websocket accept", "err", err)
		return
	}
	conn.SetReadLimit(maxFrame)
	base := h.baseURL(r)

	hctx, cancel := context.WithTimeout(context.Background(), helloTimeout)
	_, data, err := conn.Read(hctx)
	cancel()
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "hello expected")
		return
	}
	var msg rpcMessage
	var hello helloParams
	if json.Unmarshal(data, &msg) != nil || msg.Method != "hello" || json.Unmarshal(msg.Params, &hello) != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "first frame must be a hello request")
		return
	}
	a := newAdapter(h, conn, hello, h.log.With("platform", hello.Platform, "instance", hello.Instance, "adapter", hello.Adapter.Name))
	reject := func(code websocket.StatusCode, rpcCode int, reason string) {
		_ = a.write(rpcMessage{ID: msg.ID, Error: &rpcError{Code: rpcCode, Message: reason}})
		_ = conn.Close(code, reason)
	}
	if hello.Protocol != protocolVersion {
		reject(closeBadProtocol, codeInvalidInput, "unsupported protocol version "+strconv.Itoa(hello.Protocol))
		return
	}
	if hello.Platform == "" {
		reject(closeBadProtocol, codeInvalidInput, "platform is required")
		return
	}
	if hello.Instance == "" {
		hello.Instance = "remote"
		a.info.Instance = hello.Instance
	}
	key := hello.Platform + "/" + hello.Instance
	h.mu.Lock()
	if _, dup := h.conns[key]; dup {
		h.mu.Unlock()
		reject(closeDuplicate, codeInvalidInput, "an adapter "+key+" is already connected; pick another instance id")
		return
	}
	h.conns[key] = a
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.conns, key)
		h.mu.Unlock()
	}()

	ctx := context.Background()
	res := helloResult{CoreVersion: "1", Accounts: []helloAccount{}}
	res.Media.PutURL, res.Media.GetURL = base+"/media", base+"/media/{id}"
	for _, row := range h.core.PlatformAccounts(ctx, hello.Platform, hello.Instance) {
		res.Accounts = append(res.Accounts, helloAccount{ID: row.ID, Config: row.Config, DataDir: h.core.AccountDir(row.ID), Status: row.Status})
	}
	body, _ := json.Marshal(res)
	if err := a.write(rpcMessage{ID: msg.ID, Result: body}); err != nil {
		return
	}
	a.log.Info("remote adapter connected", "capabilities", len(hello.Capabilities), "accounts", len(res.Accounts))

	go a.readLoop()
	go h.keepAlive(a)
	if err := h.core.Attach(ctx, a); err != nil {
		a.log.Warn("attach", "err", err)
		_ = conn.Close(closeAttachFailure, err.Error())
		<-a.done
		return
	}
	<-a.done
	h.core.Detach(ctx, hello.Platform, hello.Instance)
	a.log.Info("remote adapter disconnected")
}

// keepAlive pings on an interval; a missed pong closes the connection (§2).
func (h *Hub) keepAlive(a *Adapter) {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(a.ctx, pingTimeout)
			err := a.conn.Ping(ctx)
			cancel()
			if err != nil {
				a.log.Warn("ping failed; dropping adapter", "err", err)
				_ = a.conn.Close(websocket.StatusPolicyViolation, "ping timeout")
				a.cancel()
				return
			}
		}
	}
}

func (h *Hub) baseURL(r *http.Request) string {
	if h.publicURL != "" {
		return h.publicURL + "/adapter/v1"
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/adapter/v1"
}

// mediaGetURL is absolute when public_url is configured, otherwise relative to the core the
// adapter connected to.
func (h *Hub) mediaGetURL(mediaID string) string {
	return h.publicURL + "/adapter/v1/media/" + mediaID
}

// putMedia streams attachment bytes into the core (§7).
func (h *Hub) putMedia(w http.ResponseWriter, r *http.Request) {
	account, mediaID := r.Header.Get("X-Account-Id"), r.Header.Get("X-Media-Id")
	if account == "" || mediaID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_request", "message": "X-Account-Id and X-Media-Id are required"}})
		return
	}
	meta := adapter.MediaMeta{Mime: r.Header.Get("Content-Type"), FileName: r.Header.Get("X-File-Name")}
	meta.Width, _ = strconv.Atoi(r.Header.Get("X-Width"))
	meta.Height, _ = strconv.Atoi(r.Header.Get("X-Height"))
	meta.DurationMs, _ = strconv.ParseInt(r.Header.Get("X-Duration-Ms"), 10, 64)
	att, err := h.core.Sink().PutMedia(r.Context(), account, mediaID, meta, r.Body)
	if err != nil {
		ce := core.AsError(err)
		writeJSON(w, ce.Status, map[string]any{"error": ce})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"media_id": att.MediaID, "sha256": att.SHA256, "size": att.Size})
}

// getMedia serves an upload's bytes to the adapter for sending.
func (h *Hub) getMedia(w http.ResponseWriter, r *http.Request) {
	mf, err := h.core.GetMedia(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		ce := core.AsError(err)
		writeJSON(w, ce.Status, map[string]any{"error": ce})
		return
	}
	if mf.Path == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "conflict", "message": "media is " + mf.Attachment.State}})
		return
	}
	w.Header().Set("Content-Type", mf.Attachment.Mime)
	http.ServeFile(w, r, mf.Path)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
