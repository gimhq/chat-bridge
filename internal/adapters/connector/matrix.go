package connector

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/matrixcontent"
	"gimhq/chat-bridge/internal/model"
)

// virtualMatrix is the bridgev2 MatrixConnector: a homeserver that exists only as chat-bridge's
// event stream. Rooms are portals, ghosts are contacts, the bridge user is the account.
type virtualMatrix struct {
	acc    *account
	bridge *bridgev2.Bridge
	bot    *virtualIntent

	mu     sync.Mutex
	rooms  map[id.RoomID]*roomState
	ghosts map[networkid.UserID]*ghostState
}

type roomState struct {
	key     networkid.PortalKey
	name    string
	direct  bool
	members map[id.UserID]*event.MemberEventContent
	power   *event.PowerLevelsEventContent
}

type ghostState struct {
	name  string
	phone string
}

const ghostPrefix = "@g_"

var b64 = base64.RawURLEncoding

func (vm *virtualMatrix) Init(br *bridgev2.Bridge) {
	vm.bridge = br
	vm.rooms = map[id.RoomID]*roomState{}
	vm.ghosts = map[networkid.UserID]*ghostState{}
	vm.bot = &virtualIntent{vm: vm, mxid: id.NewUserID("bot", serverName), isBot: true}
}

func (vm *virtualMatrix) Start(context.Context) error { return nil }
func (vm *virtualMatrix) PreStop()                    {}
func (vm *virtualMatrix) Stop()                       {}
func (vm *virtualMatrix) ServerName() string          { return serverName }

func (vm *virtualMatrix) GetCapabilities() *bridgev2.MatrixCapabilities {
	return &bridgev2.MatrixCapabilities{AutoJoinInvites: true, BatchSending: true, ArbitraryMemberChange: true}
}

func (vm *virtualMatrix) ParseGhostMXID(userID id.UserID) (networkid.UserID, bool) {
	s := userID.String()
	if !strings.HasPrefix(s, ghostPrefix) || !strings.HasSuffix(s, ":"+serverName) {
		return "", false
	}
	raw, err := b64.DecodeString(strings.TrimSuffix(strings.TrimPrefix(s, ghostPrefix), ":"+serverName))
	if err != nil {
		return "", false
	}
	return networkid.UserID(raw), true
}

func (vm *virtualMatrix) FormatGhostMXID(userID networkid.UserID) id.UserID {
	return id.UserID(ghostPrefix + b64.EncodeToString([]byte(userID)) + ":" + serverName)
}

func (vm *virtualMatrix) GhostIntent(userID networkid.UserID) bridgev2.MatrixAPI {
	return &virtualIntent{vm: vm, mxid: vm.FormatGhostMXID(userID), ghost: userID}
}

// NewUserIntent is never used: the account has no double puppet, so "from me" events are sent
// by the bot with the login's own network id in MessageMeta.
func (vm *virtualMatrix) NewUserIntent(context.Context, id.UserID, string) (bridgev2.MatrixAPI, string, error) {
	return nil, "", nil
}

func (vm *virtualMatrix) BotIntent() bridgev2.MatrixAPI { return vm.bot }

func (vm *virtualMatrix) SendBridgeStatus(_ context.Context, st *status.BridgeState) error {
	vm.acc.bridgeStatus(st)
	return nil
}

func (vm *virtualMatrix) SendMessageStatus(_ context.Context, st *bridgev2.MessageStatus, info *bridgev2.MessageStatusEventInfo) {
	switch st.Status {
	case event.MessageStatusSuccess:
		vm.acc.resolveSend(info.SourceEventID, true, nil)
	case event.MessageStatusRetriable, event.MessageStatusFail:
		err := st.InternalError
		if err == nil {
			err = errors.New(st.Message)
		}
		vm.acc.resolveSend(info.SourceEventID, false, err)
	}
}

func (vm *virtualMatrix) GenerateContentURI(_ context.Context, mediaID networkid.MediaID) (id.ContentURIString, error) {
	return id.ContentURIString("mxc://" + serverName + "/" + string(mediaID)), nil
}

func (vm *virtualMatrix) ParseContentURI(_ context.Context, uri id.ContentURIString) (networkid.MediaID, error) {
	return networkid.MediaID(mediaIDOf(uri)), nil
}

func mediaIDOf(uri id.ContentURIString) string {
	return strings.TrimPrefix(string(uri), "mxc://"+serverName+"/")
}

func (vm *virtualMatrix) room(roomID id.RoomID) *roomState {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	r := vm.rooms[roomID]
	if r == nil {
		r = &roomState{key: roomKeyOf(roomID), members: map[id.UserID]*event.MemberEventContent{}}
		vm.rooms[roomID] = r
	}
	return r
}

func (vm *virtualMatrix) GetPowerLevels(_ context.Context, roomID id.RoomID) (*event.PowerLevelsEventContent, error) {
	r := vm.room(roomID)
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if r.power == nil {
		r.power = &event.PowerLevelsEventContent{Users: map[id.UserID]int{vm.bot.mxid: 9001}}
	}
	return r.power, nil
}

func (vm *virtualMatrix) GetMembers(_ context.Context, roomID id.RoomID) (map[id.UserID]*event.MemberEventContent, error) {
	r := vm.room(roomID)
	vm.mu.Lock()
	defer vm.mu.Unlock()
	out := make(map[id.UserID]*event.MemberEventContent, len(r.members))
	for k, v := range r.members {
		out[k] = v
	}
	return out, nil
}

func (vm *virtualMatrix) GetMemberInfo(_ context.Context, roomID id.RoomID, userID id.UserID) (*event.MemberEventContent, error) {
	r := vm.room(roomID)
	vm.mu.Lock()
	defer vm.mu.Unlock()
	return r.members[userID], nil
}

// BatchSend replays history: every event goes through the same conversion, flagged as backfill.
func (vm *virtualMatrix) BatchSend(ctx context.Context, roomID id.RoomID, req *mautrix.ReqBeeperBatchSend, extras []*bridgev2.MatrixSendExtra) (*mautrix.RespBeeperBatchSend, error) {
	resp := &mautrix.RespBeeperBatchSend{EventIDs: make([]id.EventID, 0, len(req.Events))}
	for i, evt := range req.Events {
		var extra *bridgev2.MatrixSendExtra
		if i < len(extras) {
			extra = extras[i]
		}
		intent := vm.bot
		if g, ok := vm.ParseGhostMXID(evt.Sender); ok {
			intent = &virtualIntent{vm: vm, mxid: evt.Sender, ghost: g}
		}
		evtID := intent.emit(ctx, roomID, evt.Type, &evt.Content, extra, true)
		resp.EventIDs = append(resp.EventIDs, evtID)
	}
	return resp, nil
}

func (vm *virtualMatrix) GenerateDeterministicRoomID(key networkid.PortalKey) id.RoomID {
	return id.RoomID("!" + b64.EncodeToString([]byte(string(key.ID)+"\x00"+string(key.Receiver))) + ":" + serverName)
}

func roomKeyOf(roomID id.RoomID) networkid.PortalKey {
	raw, err := b64.DecodeString(strings.TrimSuffix(strings.TrimPrefix(roomID.String(), "!"), ":"+serverName))
	if err != nil {
		return networkid.PortalKey{ID: networkid.PortalID(roomID)}
	}
	idPart, recv, _ := strings.Cut(string(raw), "\x00")
	return networkid.PortalKey{ID: networkid.PortalID(idPart), Receiver: networkid.UserLoginID(recv)}
}

func (vm *virtualMatrix) GenerateDeterministicEventID(_ id.RoomID, key networkid.PortalKey, msgID networkid.MessageID, partID networkid.PartID) id.EventID {
	return id.EventID("$" + b64.EncodeToString([]byte(string(key.ID)+"\x00"+string(msgID)+"\x00"+string(partID))))
}

func (vm *virtualMatrix) GenerateReactionEventID(_ id.RoomID, target *database.Message, sender networkid.UserID, emoji networkid.EmojiID) id.EventID {
	return id.EventID("$" + b64.EncodeToString([]byte("r\x00"+string(target.ID)+"\x00"+string(sender)+"\x00"+string(emoji))))
}

// chatHint describes the room for events the core may see before the chat itself.
func (vm *virtualMatrix) chatHint(ctx context.Context, roomID id.RoomID) *model.Chat {
	r := vm.room(roomID)
	vm.mu.Lock()
	ch := &model.Chat{ID: string(r.key.ID), Kind: model.ChatGroup, Name: r.name}
	if r.direct {
		ch.Kind = model.ChatDirect
	}
	known := r.name != ""
	vm.mu.Unlock()
	if !known {
		if p, err := vm.bridge.GetPortalByMXID(ctx, roomID); err == nil && p != nil {
			ch.Name, ch.Kind = p.Name, kindOf(p.RoomType)
		}
	}
	return ch
}

func (vm *virtualMatrix) contactHint(uid networkid.UserID) *model.Contact {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	g := vm.ghosts[uid]
	if g == nil {
		return nil
	}
	return ghostContact(uid, g)
}

func ghostContact(uid networkid.UserID, g *ghostState) *model.Contact {
	c := &model.Contact{ID: string(uid), Name: g.name, Handle: string(uid), Phone: g.phone, Names: model.Names{Profile: g.name}}
	if g.phone != "" {
		c.Handle = g.phone
	}
	return c
}

// virtualIntent is one Matrix user (the bot or a ghost) acting on the virtual homeserver.
type virtualIntent struct {
	vm    *virtualMatrix
	mxid  id.UserID
	ghost networkid.UserID
	isBot bool
}

func (in *virtualIntent) GetMXID() id.UserID   { return in.mxid }
func (in *virtualIntent) IsDoublePuppet() bool { return false }

func newEventID() id.EventID { return id.EventID("$in-" + uuid.NewString()) }

func (in *virtualIntent) SendMessage(ctx context.Context, roomID id.RoomID, evType event.Type, content *event.Content, extra *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	return &mautrix.RespSendEvent{EventID: in.emit(ctx, roomID, evType, content, extra, false)}, nil
}

// emit converts one timeline event into adapter events and returns its Matrix event id.
func (in *virtualIntent) emit(ctx context.Context, roomID id.RoomID, evType event.Type, content *event.Content, extra *bridgev2.MatrixSendExtra, backfill bool) id.EventID {
	evtID := newEventID()
	acc := in.vm.acc
	chatID := string(roomKeyOf(roomID).ID)
	sender := in.ghost
	ts := time.Now()
	var meta *database.Message
	var reaction *database.Reaction
	if extra != nil {
		meta, reaction = extra.MessageMeta, extra.ReactionMeta
		if !extra.Timestamp.IsZero() {
			ts = extra.Timestamp
		}
	}
	if meta != nil {
		chatID, sender = string(meta.Room.ID), meta.SenderID
	} else if reaction != nil {
		chatID, sender = string(reaction.Room.ID), reaction.SenderID
	}
	fromMe := sender != "" && sender == acc.selfID(acc.userLogin())
	switch evType {
	case event.EventMessage, event.EventSticker:
		mc, ok := content.Parsed.(*event.MessageEventContent)
		if !ok || meta == nil { // no MessageMeta: a bridge notice, not a platform message
			return evtID
		}
		if mc.NewContent != nil && mc.RelatesTo != nil && mc.RelatesTo.Type == event.RelReplace {
			c := in.convert(mc.NewContent, evType)
			acc.rep.Events(adapter.Event{Kind: adapter.EvMessageUpdate, ChatID: chatID, MessageID: partMessageID(meta), UserID: string(sender), Content: &c, At: ts})
			return evtID
		}
		msg := &model.Message{ID: partMessageID(meta), ChatID: chatID, Sender: model.Sender{ID: string(sender)}, FromMe: fromMe, Timestamp: ts.UTC(),
			Content: in.convert(mc, evType), ReplyTo: string(meta.ReplyTo.MessageID), ThreadID: string(meta.ThreadRoot), Status: model.MsgSent}
		if h := in.vm.contactHint(sender); h != nil {
			msg.Sender.Name = h.Name
		}
		acc.rep.Events(adapter.Event{Kind: adapter.EvMessage, Message: msg, Chat: in.vm.chatHint(ctx, roomID), Sender: in.vm.contactHint(sender), Backfill: backfill})
	case event.EventReaction:
		rc, ok := content.Parsed.(*event.ReactionEventContent)
		if !ok || reaction == nil {
			return evtID
		}
		acc.rep.Events(adapter.Event{Kind: adapter.EvReaction, ChatID: chatID, MessageID: string(reaction.MessageID), UserID: string(sender), Emoji: reactionEmoji(reaction, rc.RelatesTo.Key), At: ts})
	case event.EventRedaction:
		rc, ok := content.Parsed.(*event.RedactionEventContent)
		if !ok {
			return evtID
		}
		switch {
		case meta != nil:
			acc.rep.Events(adapter.Event{Kind: adapter.EvMessageDelete, ChatID: chatID, MessageID: partMessageID(meta), UserID: string(in.ghost), At: ts})
		case reaction != nil:
			acc.rep.Events(adapter.Event{Kind: adapter.EvReaction, ChatID: chatID, MessageID: string(reaction.MessageID), UserID: string(sender), Emoji: reactionEmoji(reaction, ""), Removed: true, At: ts})
		default:
			if m, err := acc.bridge.DB.Message.GetPartByMXID(ctx, rc.Redacts); err == nil && m != nil {
				acc.rep.Events(adapter.Event{Kind: adapter.EvMessageDelete, ChatID: string(m.Room.ID), MessageID: partMessageID(m), UserID: string(in.ghost), At: ts})
			}
		}
	}
	return evtID
}

// reactionEmoji returns the bare emoji: bridgev2 stores it on the row only when the network has no
// separate emoji id, and adds a variation selector to the Matrix key.
func reactionEmoji(r *database.Reaction, key string) string {
	switch {
	case r.Emoji != "":
		return variationselector.Remove(r.Emoji)
	case key != "":
		return variationselector.Remove(key)
	}
	return variationselector.Remove(string(r.EmojiID))
}

// partMessageID keeps multi-part messages addressable: the first part carries the network id,
// later parts append their part id.
func partMessageID(m *database.Message) string {
	if m.PartID == "" {
		return string(m.ID)
	}
	return string(m.ID) + "#" + string(m.PartID)
}

// convert maps content, adopting media the connector already uploaded through the sink.
func (in *virtualIntent) convert(mc *event.MessageEventContent, evType event.Type) model.Content {
	var mediaID string
	if mc.URL != "" {
		mediaID = mediaIDOf(mc.URL)
	} else if mc.File != nil {
		mediaID = mediaIDOf(mc.File.URL)
	}
	c := matrixcontent.Convert(mc, evType, mediaID)
	acc := in.vm.acc
	acc.mu.Lock()
	defer acc.mu.Unlock()
	for i := range c.Attachments {
		if stored, ok := acc.stored[c.Attachments[i].MediaID]; ok {
			c.Attachments[i].State, c.Attachments[i].SHA256, c.Attachments[i].Size = stored.State, stored.SHA256, stored.Size
			c.Attachments[i].RemoteRef = nil
			delete(acc.stored, c.Attachments[i].MediaID)
		}
	}
	return c
}

func (in *virtualIntent) SendState(ctx context.Context, roomID id.RoomID, evType event.Type, stateKey string, content *event.Content, ts time.Time) (*mautrix.RespSendEvent, error) {
	evtID := newEventID()
	r := in.vm.room(roomID)
	switch evType {
	case event.StateMember:
		mc, ok := content.Parsed.(*event.MemberEventContent)
		if !ok {
			return nil, errors.New("member event without member content")
		}
		target := id.UserID(stateKey)
		in.vm.mu.Lock()
		r.members[target] = mc
		in.vm.mu.Unlock()
		if uid, isGhost := in.vm.ParseGhostMXID(target); isGhost {
			m := &adapter.Member{ChatID: string(r.key.ID), UserID: string(uid), Role: "member", Left: mc.Membership == event.MembershipLeave || mc.Membership == event.MembershipBan}
			in.vm.acc.rep.Events(adapter.Event{Kind: adapter.EvMember, Member: m, At: ts})
		}
	case event.StateRoomName:
		if nc, ok := content.Parsed.(*event.RoomNameEventContent); ok {
			in.vm.mu.Lock()
			r.name = nc.Name
			in.vm.mu.Unlock()
			in.vm.acc.rep.Events(adapter.Event{Kind: adapter.EvChat, Chat: in.vm.chatHint(ctx, roomID)})
		}
	case event.StatePowerLevels:
		if pl, ok := content.Parsed.(*event.PowerLevelsEventContent); ok {
			in.vm.mu.Lock()
			r.power = pl
			in.vm.mu.Unlock()
		}
	}
	return &mautrix.RespSendEvent{EventID: evtID}, nil
}

func (in *virtualIntent) MarkRead(ctx context.Context, roomID id.RoomID, eventID id.EventID, ts time.Time) error {
	m, err := in.vm.acc.bridge.DB.Message.GetPartByMXID(ctx, eventID)
	if err != nil || m == nil {
		return nil
	}
	in.vm.acc.rep.Events(adapter.Event{Kind: adapter.EvReceipt, ChatID: string(m.Room.ID), MessageIDs: []string{partMessageID(m)}, UserID: string(in.ghost), Receipt: "read", At: ts})
	return nil
}

func (in *virtualIntent) MarkUnread(context.Context, id.RoomID, bool) error { return nil }

func (in *virtualIntent) MarkTyping(_ context.Context, roomID id.RoomID, _ bridgev2.TypingType, timeout time.Duration) error {
	state := "typing"
	if timeout == 0 {
		state = "paused"
	}
	in.vm.acc.rep.Events(adapter.Event{Kind: adapter.EvTyping, ChatID: string(roomKeyOf(roomID).ID), UserID: string(in.ghost), State: state, At: time.Now()})
	return nil
}

// DownloadMedia serves attachments chat-bridge is sending out (queued by SendMessage).
func (in *virtualIntent) DownloadMedia(ctx context.Context, uri id.ContentURIString, _ *event.EncryptedFileInfo) ([]byte, error) {
	acc := in.vm.acc
	mediaID := mediaIDOf(uri)
	acc.mu.Lock()
	src := acc.uploads[mediaID]
	acc.mu.Unlock()
	if src == nil {
		return nil, fmt.Errorf("unknown media %s", mediaID)
	}
	rc, _, err := src.Open(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (in *virtualIntent) DownloadMediaToFile(ctx context.Context, uri id.ContentURIString, file *event.EncryptedFileInfo, writable bool, cb func(*os.File) error) error {
	data, err := in.DownloadMedia(ctx, uri, file)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "chat-bridge-media-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return cb(f)
}

// UploadMedia stores inbound attachments through the sink; the mxc id names the media row.
func (in *virtualIntent) UploadMedia(ctx context.Context, _ id.RoomID, data []byte, fileName, mimeType string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	acc := in.vm.acc
	mediaID := "bv2_" + uuid.NewString()
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	att, err := acc.rep.PutMedia(ctx, mediaID, adapter.MediaMeta{Mime: mimeType, FileName: fileName}, bytes.NewReader(data))
	if err != nil {
		return "", nil, err
	}
	acc.mu.Lock()
	acc.stored[mediaID] = att
	acc.mu.Unlock()
	return id.ContentURIString("mxc://" + serverName + "/" + mediaID), nil, nil
}

func (in *virtualIntent) UploadMediaStream(ctx context.Context, roomID id.RoomID, _ int64, requireFile bool, cb bridgev2.FileStreamCallback) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	var buf bytes.Buffer
	var res *bridgev2.FileStreamResult
	var err error
	if requireFile {
		f, ferr := os.CreateTemp("", "chat-bridge-upload-*")
		if ferr != nil {
			return "", nil, ferr
		}
		defer os.Remove(f.Name())
		defer f.Close()
		res, err = cb(f)
		if err != nil {
			return "", nil, err
		}
		path := f.Name()
		if res != nil && res.ReplacementFile != "" {
			path = res.ReplacementFile
			defer os.Remove(path)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return "", nil, rerr
		}
		buf.Write(data)
	} else if res, err = cb(&buf); err != nil {
		return "", nil, err
	}
	var name, mime string
	if res != nil {
		name, mime = res.FileName, res.MimeType
	}
	return in.UploadMedia(ctx, roomID, buf.Bytes(), name, mime)
}

func (in *virtualIntent) SetDisplayName(_ context.Context, name string) error {
	if in.isBot {
		return nil
	}
	in.vm.mu.Lock()
	g := in.vm.ghosts[in.ghost]
	if g == nil {
		g = &ghostState{}
		in.vm.ghosts[in.ghost] = g
	}
	g.name = name
	c := ghostContact(in.ghost, g)
	in.vm.mu.Unlock()
	in.vm.acc.rep.Events(adapter.Event{Kind: adapter.EvContact, Contact: c})
	return nil
}

func (in *virtualIntent) SetAvatarURL(context.Context, id.ContentURIString) error { return nil }

func (in *virtualIntent) SetExtraProfileMeta(_ context.Context, data any) error {
	extra, ok := data.(*event.BeeperProfileExtra)
	if !ok || in.isBot {
		return nil
	}
	in.vm.mu.Lock()
	g := in.vm.ghosts[in.ghost]
	if g == nil {
		g = &ghostState{}
		in.vm.ghosts[in.ghost] = g
	}
	for _, ident := range extra.Identifiers {
		if p, ok := strings.CutPrefix(ident, "tel:"); ok {
			g.phone = p
		}
	}
	c := ghostContact(in.ghost, g)
	in.vm.mu.Unlock()
	if c.Name != "" || c.Phone != "" {
		in.vm.acc.rep.Events(adapter.Event{Kind: adapter.EvContact, Contact: c})
	}
	return nil
}

func (in *virtualIntent) SetProfile(context.Context, any) error { return nil }

// CreateRoom registers the portal's room and announces the chat.
func (in *virtualIntent) CreateRoom(ctx context.Context, req *mautrix.ReqCreateRoom) (id.RoomID, error) {
	roomID := req.BeeperLocalRoomID
	if roomID == "" {
		roomID = id.RoomID("!" + uuid.NewString() + ":" + serverName)
	}
	r := in.vm.room(roomID)
	name := req.Name
	if name == "" { // DM portals carry the peer's name on the portal, not in the request
		if p, err := in.vm.bridge.GetExistingPortalByKey(ctx, r.key); err == nil && p != nil {
			name = p.Name
		}
	}
	in.vm.mu.Lock()
	r.name, r.direct, r.power = name, req.IsDirect, req.PowerLevelOverride
	r.members[in.vm.bot.mxid] = &event.MemberEventContent{Membership: event.MembershipJoin}
	ch := &model.Chat{ID: string(r.key.ID), Kind: model.ChatGroup, Name: name}
	if req.IsDirect {
		ch.Kind = model.ChatDirect
	}
	seen := map[id.UserID]bool{}
	for _, u := range append(append([]id.UserID{}, req.Invite...), req.BeeperInitialMembers...) {
		if seen[u] {
			continue
		}
		seen[u] = true
		r.members[u] = &event.MemberEventContent{Membership: event.MembershipJoin}
		if uid, ok := in.vm.ParseGhostMXID(u); ok {
			p := model.Participant{ID: string(uid), Role: "member"}
			if g := in.vm.ghosts[uid]; g != nil {
				p.Name = g.name
			}
			ch.Participants = append(ch.Participants, p)
		}
	}
	in.vm.mu.Unlock()
	in.vm.acc.rep.Events(adapter.Event{Kind: adapter.EvChat, Chat: ch})
	return roomID, nil
}

func (in *virtualIntent) DeleteRoom(_ context.Context, roomID id.RoomID, _ bool) error {
	in.vm.mu.Lock()
	delete(in.vm.rooms, roomID)
	in.vm.mu.Unlock()
	return nil
}

func (in *virtualIntent) EnsureJoined(_ context.Context, roomID id.RoomID, _ ...bridgev2.EnsureJoinedParams) error {
	r := in.vm.room(roomID)
	in.vm.mu.Lock()
	r.members[in.mxid] = &event.MemberEventContent{Membership: event.MembershipJoin}
	in.vm.mu.Unlock()
	return nil
}

func (in *virtualIntent) EnsureInvited(_ context.Context, roomID id.RoomID, userID id.UserID) error {
	r := in.vm.room(roomID)
	in.vm.mu.Lock()
	if _, ok := r.members[userID]; !ok {
		r.members[userID] = &event.MemberEventContent{Membership: event.MembershipJoin}
	}
	in.vm.mu.Unlock()
	return nil
}

func (in *virtualIntent) TagRoom(context.Context, id.RoomID, event.RoomTag, bool) error { return nil }
func (in *virtualIntent) MuteRoom(context.Context, id.RoomID, time.Time) error          { return nil }

func (in *virtualIntent) GetEvent(context.Context, id.RoomID, id.EventID) (*event.Event, error) {
	return nil, errNotImplemented
}
