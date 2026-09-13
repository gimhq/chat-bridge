package remote

import (
	"encoding/json"
	"fmt"
	"time"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

// JSON-RPC 2.0 framing (docs/adapter-protocol.md §2).

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc %d: %s", e.Code, e.Message) }

// Error codes on the wire (§4).
const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeNotConnected   = 1001
	codeInvalidTarget  = 1002
	codeInvalidInput   = 1003
	codeUnsupported    = 1004
	codeRateLimited    = 1005
	codePlatform       = 1006
)

// toAdapterErr maps a wire error to the adapter error the core understands.
func toAdapterErr(e *rpcError) error {
	code := adapter.ErrPlatform
	switch e.Code {
	case codeMethodNotFound, codeUnsupported:
		code = adapter.ErrUnsupported
	case codeNotConnected:
		code = adapter.ErrNotConnected
	case codeInvalidTarget:
		code = adapter.ErrInvalidTarget
	case codeInvalidInput, codeInvalidParams:
		code = adapter.ErrInvalidInput
	case codeRateLimited:
		code = adapter.ErrRateLimited
	}
	return &adapter.Error{Code: code, Message: e.Message}
}

// helloParams is the adapter's first request (§3).
type helloParams struct {
	Protocol     int                            `json:"protocol"`
	Platform     string                         `json:"platform"`
	Instance     string                         `json:"instance"`
	Adapter      struct{ Name, Version string } `json:"adapter"`
	Capabilities []string                       `json:"capabilities"`
	LoginFlows   []model.LoginFlow              `json:"login_flows"`
	ConfigSchema json.RawMessage                `json:"config_schema"`
	DeviceSchema json.RawMessage                `json:"device_schema"`
}

type helloAccount struct {
	ID      string          `json:"id"`
	Config  json.RawMessage `json:"config"`
	DataDir string          `json:"data_dir"`
	Status  string          `json:"status"`
}

type helloResult struct {
	CoreVersion string         `json:"core_version"`
	Accounts    []helloAccount `json:"accounts"`
	Media       struct {
		PutURL string `json:"put_url"`
		GetURL string `json:"get_url"`
	} `json:"media"`
}

// wireEvent is one entry of the adapter → core `events` batch (§5.3).
type wireEvent struct {
	Kind string `json:"kind"`

	Message  json.RawMessage `json:"message,omitempty"`
	Chat     *model.Chat     `json:"chat,omitempty"`
	Sender   *model.Contact  `json:"sender,omitempty"`
	Backfill bool            `json:"backfill,omitempty"`

	ChatID     string         `json:"chat_id,omitempty"`
	MessageID  string         `json:"message_id,omitempty"`
	MessageIDs []string       `json:"message_ids,omitempty"`
	UserID     string         `json:"user_id,omitempty"`
	SenderID   string         `json:"sender_id,omitempty"`
	Content    *model.Content `json:"content,omitempty"`
	EditedAt   *time.Time     `json:"edited_at,omitempty"`
	DeletedAt  *time.Time     `json:"deleted_at,omitempty"`
	Emoji      string         `json:"emoji,omitempty"`
	Removed    bool           `json:"removed,omitempty"`
	Receipt    string         `json:"receipt,omitempty"`
	State      string         `json:"state,omitempty"`
	LastSeen   *time.Time     `json:"last_seen,omitempty"`
	TS         *time.Time     `json:"ts,omitempty"`

	ChatName string `json:"chat_name,omitempty"`
	Role     string `json:"role,omitempty"`
	Left     bool   `json:"left,omitempty"`

	Contact *model.Contact `json:"contact,omitempty"`

	PlatformType string          `json:"platform_type,omitempty"`
	Raw          json.RawMessage `json:"raw,omitempty"`

	Request *wireRequest `json:"request,omitempty"`
}

// wireRequest is the `request` event payload (adapter-protocol.md §5.3).
type wireRequest struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// State is empty or "pending" for a new request; "accepted", "rejected" or "expired" when the
	// platform resolved it (answered on another device, call ended).
	State string `json:"state,omitempty"`
	From  *struct {
		ID   string `json:"id"`
		Name string `json:"name,omitempty"`
	} `json:"from,omitempty"`
	Chat        *model.RequestChat `json:"chat,omitempty"`
	Message     string             `json:"message,omitempty"`
	CallKind    string             `json:"call_kind,omitempty"`
	PlatformRef json.RawMessage    `json:"platform_ref,omitempty"`
	CreatedAt   *time.Time         `json:"created_at,omitempty"`
	ExpiresAt   *time.Time         `json:"expires_at,omitempty"`
}

// decode converts a wire event into the in-process form.
func (w wireEvent) decode() (adapter.Event, error) {
	ev := adapter.Event{Kind: w.Kind, ChatID: w.ChatID, MessageID: w.MessageID, MessageIDs: w.MessageIDs, Content: w.Content,
		Emoji: w.Emoji, Removed: w.Removed, Receipt: w.Receipt, State: w.State, LastSeen: w.LastSeen, Contact: w.Contact,
		PlatformType: w.PlatformType, Raw: w.Raw, Chat: w.Chat, Sender: w.Sender, Backfill: w.Backfill}
	ev.UserID = w.UserID
	if ev.UserID == "" {
		ev.UserID = w.SenderID
	}
	for _, t := range []*time.Time{w.TS, w.EditedAt, w.DeletedAt} {
		if t != nil {
			ev.At = t.UTC()
			break
		}
	}
	switch w.Kind {
	case adapter.EvMessage:
		if len(w.Message) == 0 {
			return ev, fmt.Errorf("message event without message")
		}
		var m model.Message
		if err := json.Unmarshal(w.Message, &m); err != nil {
			return ev, fmt.Errorf("message: %w", err)
		}
		// remote_ref is not part of the public Attachment JSON; pull it from the wire form.
		var refs struct {
			Content struct {
				Attachments []struct {
					RemoteRef json.RawMessage `json:"remote_ref"`
				} `json:"attachments"`
			} `json:"content"`
		}
		_ = json.Unmarshal(w.Message, &refs)
		for i := range m.Content.Attachments {
			if i < len(refs.Content.Attachments) {
				m.Content.Attachments[i].RemoteRef = refs.Content.Attachments[i].RemoteRef
			}
		}
		if len(m.Raw) > 0 && len(ev.Raw) == 0 {
			ev.Raw = m.Raw
		}
		m.Raw = nil
		ev.Message = &m
	case adapter.EvMember:
		ev.Member = &adapter.Member{ChatID: w.ChatID, UserID: w.UserID, ChatName: w.ChatName, Role: w.Role, Left: w.Left}
	case adapter.EvRequest:
		r := w.Request
		if r == nil || r.Key == "" || r.Kind == "" {
			return ev, fmt.Errorf("request event needs request.key and request.kind")
		}
		in := &adapter.Request{Key: r.Key, Kind: r.Kind, State: r.State, Message: r.Message, CallKind: r.CallKind, PlatformRef: r.PlatformRef, ExpiresAt: r.ExpiresAt}
		if in.State == model.RequestPending {
			in.State = ""
		}
		if r.From != nil {
			in.FromID, in.FromName = r.From.ID, r.From.Name
		}
		if r.Chat != nil {
			in.ChatID, in.ChatName, in.ChatKind = r.Chat.ID, r.Chat.Name, r.Chat.Kind
		}
		if r.CreatedAt != nil {
			in.CreatedAt = r.CreatedAt.UTC()
		}
		ev.Request = in
	case adapter.EvMessageUpdate, adapter.EvMessageDelete, adapter.EvReaction, adapter.EvReceipt, adapter.EvChat,
		adapter.EvContact, adapter.EvTyping, adapter.EvPresence, adapter.EvPlatform:
	default:
		// Unknown kinds surface as platform events instead of being dropped (§9).
		ev.Kind, ev.PlatformType = adapter.EvPlatform, w.Kind
		if len(ev.Raw) == 0 {
			ev.Raw, _ = json.Marshal(w)
		}
	}
	return ev, nil
}

// wireAttachment is what the adapter receives for each attachment of message.send (§4).
type wireAttachment struct {
	MediaID  string `json:"media_id"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	FileName string `json:"file_name,omitempty"`
	SHA256   string `json:"sha256"`
	URL      string `json:"url"`
}

// statusParams is the adapter → core `status` request (§5.1).
type statusParams struct {
	AccountID string          `json:"account_id"`
	Status    string          `json:"status"`
	Self      *model.Contact  `json:"self"`
	Device    json.RawMessage `json:"device"`
	Error     *model.Error    `json:"error"`
}
