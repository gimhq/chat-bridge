// Package adapter defines the contract between the bridge core and a platform implementation.
// See docs/adapter-protocol.md §10.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"gimhq/chat-bridge/internal/model"
)

// Capability strings, see docs/chat-api-spec.md §3.2.
const (
	CapSendText     = "send.text"
	CapSendMedia    = "send.media"
	CapSendLocation = "send.location"
	CapSendContact  = "send.contact"
	CapReply        = "message.reply"
	CapThread       = "message.thread"
	CapEdit         = "message.edit"
	CapDelete       = "message.delete"
	CapReaction     = "message.reaction"
	CapHistory      = "message.history"
	CapChatRead     = "chat.read"
	CapChatTyping   = "chat.typing"
	CapChatResolve  = "chat.resolve"
	CapChatCreate   = "chat.create"
	CapChatMembers  = "chat.members"
	CapPresence     = "presence"
	CapReceipts     = "receipts"
	CapMarkdown     = "format.markdown"
	CapHTML         = "format.html"
	CapSelfUpdate   = "self.update"
	CapContactAlias = "contact.alias"
	CapKeys         = "keys.manage"
)

// Error codes an adapter returns; the core maps them to HTTP statuses.
const (
	ErrNotConnected  = "not_connected"
	ErrInvalidTarget = "invalid_target"
	ErrInvalidInput  = "invalid_input"
	ErrUnsupported   = "unsupported"
	ErrRateLimited   = "rate_limited"
	ErrPlatform      = "platform_error"
)

// ErrMediaStoredBySink is returned by FetchMedia when the adapter delivered the bytes through
// Sink.PutMedia itself (out-of-process adapters upload over HTTP) instead of the writer.
var ErrMediaStoredBySink = errors.New("media stored through sink")

// Error is a typed adapter failure.
type Error struct {
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds an Error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the adapter error code, or "" if err is not an adapter Error.
func CodeOf(err error) string {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// Info is what an adapter declares at registration (the "hello").
type Info struct {
	Platform string
	// Instance distinguishes several adapters of one platform (e.g. two WhatsApp hosts on
	// different networks). In-process adapters default to "local", remote ones to "remote".
	Instance     string
	Name         string
	Version      string
	Capabilities []string
	LoginFlows   []model.LoginFlow
	ConfigSchema json.RawMessage
}

// Has reports whether the capability is declared.
func (i Info) Has(capability string) bool {
	for _, c := range i.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// Status is what an adapter reports about one account.
type Status struct {
	Status string
	Self   *model.Contact
	Device json.RawMessage
	Error  *model.Error
}

// Event kinds an adapter emits through Sink.Events.
const (
	EvMessage       = "message"
	EvMessageUpdate = "message_update"
	EvMessageDelete = "message_delete"
	EvReaction      = "reaction"
	EvReceipt       = "receipt"
	EvChat          = "chat"
	EvMember        = "member"
	EvContact       = "contact"
	EvTyping        = "typing"
	EvPresence      = "presence"
	EvPlatform      = "platform_event"
	EvRequest       = "request"
)

// Event is one adapter-side occurrence. Which fields apply depends on Kind.
type Event struct {
	Kind string

	// message: Message plus optional hints for a chat/sender the core may not know yet.
	Message *model.Message
	Chat    *model.Chat
	Sender  *model.Contact
	Raw     json.RawMessage
	// Backfill marks a message replayed from history (initial sync, dialog load): the core does
	// not bump unread counters or auto-download its media.
	Backfill bool

	// message_update / message_delete / reaction / receipt / typing.
	ChatID     string
	MessageID  string
	MessageIDs []string
	UserID     string
	Content    *model.Content
	Emoji      string
	Removed    bool
	Receipt    string // delivered|read
	State      string // typing: typing|paused; presence: online|offline
	LastSeen   *time.Time
	At         time.Time

	// member.
	Member *Member

	// contact.
	Contact *model.Contact

	// platform_event.
	PlatformType string

	// request.
	Request *Request
}

// Request is an invite, join request, or call waiting for the owner. Key is stable per account
// so the adapter can re-emit the same request with a new State (accepted on another device, call
// ended) or better names; State empty means pending.
type Request struct {
	Key         string
	Kind        string
	State       string
	FromID      string
	FromName    string
	ChatID      string
	ChatName    string
	ChatKind    string
	Message     string
	CallKind    string // voice|video, calls only
	PlatformRef json.RawMessage
	CreatedAt   time.Time
	ExpiresAt   *time.Time
}

// Member is a chat membership change.
type Member struct {
	ChatID   string
	UserID   string
	ChatName string
	Role     string
	Left     bool
}

// Sink is what the core hands the adapter. Every method is durable when it returns.
type Sink interface {
	Status(ctx context.Context, accountID string, st Status) error
	LoginStep(ctx context.Context, accountID string, step model.LoginStep) error
	Events(ctx context.Context, accountID string, evs []Event) error
	// PutMedia stores bytes for an attachment the adapter downloaded. The returned attachment
	// has State ready, SHA256, and Size set.
	PutMedia(ctx context.Context, accountID, mediaID string, meta MediaMeta, r io.Reader) (model.Attachment, error)
}

// MediaMeta accompanies bytes into PutMedia.
type MediaMeta struct {
	Mime       string
	FileName   string
	Width      int
	Height     int
	DurationMs int64
}

// MediaSource lets an adapter read an uploaded attachment when sending.
type MediaSource interface {
	Open(ctx context.Context, mediaID string) (io.ReadCloser, model.Attachment, error)
}

// SendRequest is what the core hands SendMessage. Attachments are resolved (SHA/size known) and
// readable through Media.
type SendRequest struct {
	ChatID   string
	ClientID string
	Content  model.Content
	ReplyTo  string
	// ReplyTarget is the quoted message when the core knows it (author and text for the quote).
	ReplyTarget *model.Message
	ThreadID    string
	Mentions    []string
	Media       MediaSource
}

// Adapter is the full contract. Optional capabilities live in the interfaces below.
type Adapter interface {
	Info() Info
	Start(ctx context.Context, sink Sink) error
	Stop(ctx context.Context) error

	AddAccount(ctx context.Context, id string, cfg json.RawMessage, dataDir string) error
	RemoveAccount(ctx context.Context, id string) error
	Reconnect(ctx context.Context, id string) error

	LoginStart(ctx context.Context, id, flow string) (model.LoginStep, error)
	LoginSubmit(ctx context.Context, id string, fields map[string]string) (model.LoginStep, error)
	LoginRefresh(ctx context.Context, id string) (model.LoginStep, error)
	LoginCancel(ctx context.Context, id string) error
	Logout(ctx context.Context, id string) error

	GetChat(ctx context.Context, id, chatID string) (model.Chat, error)
	ListContacts(ctx context.Context, id string) ([]model.Contact, error)

	SendMessage(ctx context.Context, id string, req SendRequest) (model.Message, error)
	FetchMedia(ctx context.Context, id, mediaID string, ref json.RawMessage, w io.Writer) (MediaMeta, error)
}

// Resolver maps a handle to a chat (chat.resolve).
type Resolver interface {
	ResolveChat(ctx context.Context, id, handle string) (model.ResolvedChat, error)
}

// Editor edits sent messages (message.edit).
type Editor interface {
	EditMessage(ctx context.Context, id, chatID, msgID string, c model.Content) (model.Message, error)
}

// Deleter unsends messages (message.delete). senderID is the original author.
type Deleter interface {
	DeleteMessage(ctx context.Context, id, chatID, msgID, senderID string) error
}

// Reactor adds and removes reactions (message.reaction). senderID is the target's author.
type Reactor interface {
	React(ctx context.Context, id, chatID, msgID, senderID, emoji string, remove bool) error
}

// Reader sends read receipts (chat.read) for messages by senderID.
type Reader interface {
	MarkRead(ctx context.Context, id, chatID string, messageIDs []string, senderID string) error
}

// KeyManager exposes end-to-end encryption key management (keys.manage).
type KeyManager interface {
	KeysStatus(ctx context.Context, id string) (model.KeyStatus, error)
	KeysVerify(ctx context.Context, id, recoveryKey string) (model.KeyVerifyResult, error)
	KeysExport(ctx context.Context, id, passphrase string) ([]byte, error)
	KeysImport(ctx context.Context, id, passphrase string, data []byte) (int, error)
}

// Typer sends typing indicators (chat.typing).
type Typer interface {
	Typing(ctx context.Context, id, chatID, state string) error
}

// CreateChatRequest is the body of chat.create.
type CreateChatRequest struct {
	Kind    string
	Name    string
	Members []string
}

// ChatCreator creates group chats on the platform (chat.create).
type ChatCreator interface {
	CreateChat(ctx context.Context, id string, req CreateChatRequest) (model.Chat, error)
}

// ChatUpdate carries chat metadata that reaches the platform; nil leaves a field alone.
type ChatUpdate struct {
	Name *string
}

// ChatUpdater pushes chat metadata to the platform (chat.update). No capability: every adapter
// that can rename a chat implements it; the core answers unsupported otherwise.
type ChatUpdater interface {
	UpdateChat(ctx context.Context, id, chatID string, p ChatUpdate) (model.Chat, error)
}

// BackfillCursor names the oldest message the core already holds; zero means "from the newest".
type BackfillCursor struct {
	Timestamp time.Time
	MessageID string
}

// Backfiller fetches history older than a point (message.history). The bool reports whether
// more history exists beyond the returned batch.
type Backfiller interface {
	Backfill(ctx context.Context, id, chatID string, before BackfillCursor, limit int) ([]model.Message, bool, error)
}

// SelfUpdate carries profile changes; nil leaves a field alone. Avatar is readable through Media.
type SelfUpdate struct {
	Name          *string
	Bio           *string
	AvatarMediaID string
	Media         MediaSource
}

// SelfUpdater changes the account's own profile (self.update).
type SelfUpdater interface {
	UpdateSelf(ctx context.Context, id string, p SelfUpdate) (model.Contact, error)
}

// Blocker blocks and unblocks users on the platform (contact.block). No capability: every
// built-in adapter implements it; the core answers unsupported otherwise.
type Blocker interface {
	Block(ctx context.Context, id, userID string, blocked bool) error
}

// RequestAnswer is what the core hands AnswerRequest: the stored request plus the owner's action.
type RequestAnswer struct {
	Kind        string
	Key         string
	ChatID      string
	FromID      string
	PlatformRef json.RawMessage
	Action      string // accept|reject
	Reason      string
}

// RequestAnswerer answers invites, join requests, and calls on the platform. No capability: the
// core offers the actions a request kind allows whenever the adapter implements it.
type RequestAnswerer interface {
	AnswerRequest(ctx context.Context, id string, a RequestAnswer) error
}
