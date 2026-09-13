// Package model holds the JSON types shared by the API, the core, and adapters.
// Shapes follow docs/chat-api-spec.md.
package model

import (
	"encoding/json"
	"time"
)

// Account status values.
const (
	StatusUnpaired     = "unpaired"
	StatusLoggingIn    = "logging_in"
	StatusConnecting   = "connecting"
	StatusConnected    = "connected"
	StatusDisconnected = "disconnected"
	StatusError        = "error"
)

// Error is the {code, message} pair used on accounts and login steps.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Account is one logged-in identity on one platform.
type Account struct {
	ID           string          `json:"id"`
	Platform     string          `json:"platform"`
	Adapter      string          `json:"adapter"`
	Status       string          `json:"status"`
	Self         *Contact        `json:"self"`
	Login        *LoginRecord    `json:"login,omitempty"`
	Capabilities []string        `json:"capabilities"`
	Config       json.RawMessage `json:"config,omitempty"`
	Device       json.RawMessage `json:"device,omitempty"`
	Stats        *AccountStats   `json:"stats,omitempty"`
	Error        *Error          `json:"error"`
	CreatedAt    time.Time       `json:"created_at"`
	ConnectedAt  *time.Time      `json:"connected_at"`
}

// LoginRecord remembers how a session was established.
type LoginRecord struct {
	Flow       string    `json:"flow"`
	Identifier string    `json:"identifier,omitempty"`
	At         time.Time `json:"at"`
}

// AccountStats are derived counts shown on GET /accounts/{a}.
type AccountStats struct {
	Chats           int64      `json:"chats"`
	Messages        int64      `json:"messages"`
	RequestsPending int64      `json:"requests_pending"`
	LastInboundAt   *time.Time `json:"last_inbound_at"`
	LastEventID     string     `json:"last_event_id"`
}

// Names splits the several names a platform can have for one user.
type Names struct {
	Alias       string `json:"alias,omitempty"`
	AliasSource string `json:"alias_source,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Username    string `json:"username,omitempty"`
	First       string `json:"first,omitempty"`
	Last        string `json:"last,omitempty"`
}

// Contact is a user as seen by one account.
type Contact struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Names     Names           `json:"names"`
	Handle    string          `json:"handle,omitempty"`
	Phone     string          `json:"phone,omitempty"`
	Email     string          `json:"email,omitempty"`
	Avatar    *AvatarRef      `json:"avatar"`
	Bio       string          `json:"bio,omitempty"`
	IsSelf    bool            `json:"is_self"`
	IsContact bool            `json:"is_contact"`
	Blocked   bool            `json:"blocked"`
	UpdatedAt time.Time       `json:"updated_at"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// AvatarRef points at a stored avatar image.
type AvatarRef struct {
	MediaID string `json:"media_id"`
}

// Chat kinds.
const (
	ChatDirect  = "direct"
	ChatGroup   = "group"
	ChatChannel = "channel"
	ChatSelf    = "self"
)

// Chat is a conversation.
type Chat struct {
	ID               string          `json:"id"`
	AccountID        string          `json:"account_id"`
	Kind             string          `json:"kind"`
	Name             string          `json:"name,omitempty"`
	Avatar           *AvatarRef      `json:"avatar"`
	UnreadCount      int64           `json:"unread_count"`
	LastMessageAt    *time.Time      `json:"last_message_at"`
	LastMessage      *Message        `json:"last_message,omitempty"`
	Muted            bool            `json:"muted"`
	Archived         bool            `json:"archived"`
	Tags             []string        `json:"tags"`
	PinnedMessageIDs []string        `json:"pinned_message_ids"`
	EphemeralTTL     *int64          `json:"ephemeral_ttl_s"`
	Participants     []Participant   `json:"participants,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
	UpdatedAt        time.Time       `json:"-"`
}

// Participant is a chat member.
type Participant struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	ChatName string `json:"chat_name,omitempty"`
	Role     string `json:"role"`
}

// Sender identifies the author of a message.
type Sender struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	ChatName string `json:"chat_name,omitempty"`
}

// Outbound message status values.
const (
	MsgPending   = "pending"
	MsgSent      = "sent"
	MsgDelivered = "delivered"
	MsgRead      = "read"
	MsgFailed    = "failed"
)

// Message is one chat message.
type Message struct {
	ID        string          `json:"id"`
	AccountID string          `json:"account_id"`
	ChatID    string          `json:"chat_id"`
	Sender    Sender          `json:"sender"`
	FromMe    bool            `json:"from_me"`
	Timestamp time.Time       `json:"timestamp"`
	Content   Content         `json:"content"`
	ReplyTo   string          `json:"reply_to,omitempty"`
	ThreadID  string          `json:"thread_id,omitempty"`
	Mentions  []string        `json:"mentions"`
	Forwarded bool            `json:"forwarded"`
	Ephemeral *Ephemeral      `json:"ephemeral"`
	EditedAt  *time.Time      `json:"edited_at"`
	DeletedAt *time.Time      `json:"deleted_at"`
	Reactions []Reaction      `json:"reactions"`
	Status    string          `json:"status,omitempty"`
	ClientID  string          `json:"client_id,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// Ephemeral marks disappearing or view-once messages.
type Ephemeral struct {
	ExpiresAt time.Time `json:"expires_at"`
	ViewOnce  bool      `json:"view_once"`
}

// Reaction is one emoji from one sender.
type Reaction struct {
	Emoji    string `json:"emoji"`
	SenderID string `json:"sender_id"`
}

// Content types.
const (
	ContentText        = "text"
	ContentImage       = "image"
	ContentVideo       = "video"
	ContentAudio       = "audio"
	ContentVoice       = "voice"
	ContentFile        = "file"
	ContentSticker     = "sticker"
	ContentLocation    = "location"
	ContentContact     = "contact"
	ContentPoll        = "poll"
	ContentCall        = "call"
	ContentPayment     = "payment"
	ContentSystem      = "system"
	ContentDeleted     = "deleted"
	ContentExpired     = "expired"
	ContentUnsupported = "unsupported"
)

// Content is the body of a message; exactly one Type applies.
type Content struct {
	Type        string        `json:"type"`
	Text        string        `json:"text,omitempty"`
	Format      string        `json:"format,omitempty"`
	Attachments []Attachment  `json:"attachments,omitempty"`
	Location    *Location     `json:"location,omitempty"`
	Contacts    []ContactCard `json:"contacts,omitempty"`
	Poll        *Poll         `json:"poll,omitempty"`
	Call        *Call         `json:"call,omitempty"`
	System      *System       `json:"system,omitempty"`
	Unsupported *Unsupported  `json:"unsupported,omitempty"`
}

// Attachment states.
const (
	MediaReady   = "ready"
	MediaPending = "pending"
	MediaFailed  = "failed"
	MediaRemote  = "remote"
	MediaPurged  = "purged"
)

// Attachment describes a media item.
type Attachment struct {
	MediaID          string          `json:"media_id"`
	Mime             string          `json:"mime"`
	Size             int64           `json:"size,omitempty"`
	FileName         string          `json:"file_name,omitempty"`
	Width            int             `json:"width,omitempty"`
	Height           int             `json:"height,omitempty"`
	DurationMs       int64           `json:"duration_ms,omitempty"`
	SHA256           string          `json:"sha256,omitempty"`
	URL              string          `json:"url,omitempty"`
	ThumbnailMediaID string          `json:"thumbnail_media_id,omitempty"`
	State            string          `json:"state"`
	RemoteRef        json.RawMessage `json:"-"`
}

// Location content.
type Location struct {
	Lat       float64    `json:"lat"`
	Lon       float64    `json:"lon"`
	Name      string     `json:"name,omitempty"`
	Address   string     `json:"address,omitempty"`
	LiveUntil *time.Time `json:"live_until,omitempty"`
}

// ContactCard content.
type ContactCard struct {
	Name   string   `json:"name"`
	Phones []string `json:"phones,omitempty"`
	Emails []string `json:"emails,omitempty"`
	VCard  string   `json:"vcard,omitempty"`
}

// Poll content.
type Poll struct {
	Question string       `json:"question"`
	Options  []PollOption `json:"options"`
	Multi    bool         `json:"multi"`
	Closed   bool         `json:"closed"`
}

// PollOption is one poll answer.
type PollOption struct {
	Text  string `json:"text"`
	Votes int    `json:"votes"`
}

// Call content.
type Call struct {
	Kind      string `json:"kind"`
	State     string `json:"state"`
	DurationS int64  `json:"duration_s,omitempty"`
}

// System content: platform-generated timeline notices.
type System struct {
	Kind    string   `json:"kind"`
	Actor   string   `json:"actor,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Value   string   `json:"value,omitempty"`
}

// Unsupported content names the platform type the adapter could not map.
type Unsupported struct {
	PlatformType string `json:"platform_type"`
}

// SendRequest is the body of POST /chats/{chat}/messages.
type SendRequest struct {
	ClientID string   `json:"client_id,omitempty"`
	Content  Content  `json:"content"`
	ReplyTo  string   `json:"reply_to,omitempty"`
	ThreadID string   `json:"thread_id,omitempty"`
	Mentions []string `json:"mentions,omitempty"`
}

// Login step kinds.
const (
	StepInput   = "input"
	StepDisplay = "display"
	StepDone    = "done"
	StepFailed  = "failed"
)

// LoginStep is one state of the login machine.
type LoginStep struct {
	Flow    string        `json:"flow"`
	Step    string        `json:"step"`
	Input   *LoginInput   `json:"input,omitempty"`
	Display *LoginDisplay `json:"display,omitempty"`
	Self    *Contact      `json:"self,omitempty"`
	Error   *Error        `json:"error,omitempty"`
}

// LoginInput asks the owner for fields.
type LoginInput struct {
	Fields []LoginField `json:"fields"`
}

// LoginField describes one input.
type LoginField struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Label   string `json:"label,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

// LoginDisplay shows the owner something (QR, code, URL).
type LoginDisplay struct {
	Type      string     `json:"type"`
	Data      string     `json:"data"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// LoginFlow is one way an adapter can log in.
type LoginFlow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Platform is what GET /platforms returns.
type Platform struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Capabilities []string           `json:"capabilities"`
	LoginFlows   []LoginFlow        `json:"login_flows"`
	ConfigSchema json.RawMessage    `json:"config_schema,omitempty"`
	Instances    []PlatformInstance `json:"instances"`
}

// PlatformInstance is one connected adapter of a platform.
type PlatformInstance struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Remote       bool     `json:"remote"`
	Capabilities []string `json:"capabilities"`
}

// Event types.
const (
	EvMessageNew      = "message.new"
	EvMessageUpdated  = "message.updated"
	EvMessageDeleted  = "message.deleted"
	EvMessageReaction = "message.reaction"
	EvMessageReceipt  = "message.receipt"
	EvChatNew         = "chat.new"
	EvChatUpdated     = "chat.updated"
	EvChatTyping      = "chat.typing"
	EvPresence        = "presence"
	EvContactUpdated  = "contact.updated"
	EvAccountStatus   = "account.status"
	EvLoginStep       = "account.login_step"
	EvPlatform        = "platform.event"
)

// Event is the envelope on /events and webhooks.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	AccountID string          `json:"account_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// Webhook is a consumer subscription.
type Webhook struct {
	ID        string     `json:"id"`
	URL       string     `json:"url"`
	Secret    string     `json:"-"`
	AccountID string     `json:"account_id,omitempty"`
	Types     []string   `json:"types,omitempty"`
	Cursor    string     `json:"cursor"`
	Failures  int        `json:"failures"`
	PausedAt  *time.Time `json:"paused_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// ResolvedChat is the result of chat.resolve.
type ResolvedChat struct {
	ChatID string `json:"chat_id"`
	Kind   string `json:"kind"`
	UserID string `json:"user_id,omitempty"`
}
