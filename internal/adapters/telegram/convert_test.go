package telegram

import (
	"encoding/json"
	"testing"

	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/model"
)

func TestPeerIDs(t *testing.T) {
	if got := peerID(&tg.PeerUser{UserID: 42}); got != "42" {
		t.Fatalf("user: %s", got)
	}
	if got := peerID(&tg.PeerChat{ChatID: 7}); got != "-7" {
		t.Fatalf("chat: %s", got)
	}
	if got := peerID(&tg.PeerChannel{ChannelID: 123}); got != "-1000000000123" {
		t.Fatalf("channel: %s", got)
	}
	if userID(42) != "42" || chatIDOf(7) != "-7" || channelID(123) != "-1000000000123" {
		t.Fatal("helpers disagree with peerID")
	}
	chat, msg, err := splitMessageID(messageID("-7", 99))
	if err != nil || chat != "-7" || msg != 99 {
		t.Fatalf("split: %s %d %v", chat, msg, err)
	}
	if _, _, err := splitMessageID("-7:x"); err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestConvertContent(t *testing.T) {
	text := convertContent(&tg.Message{Message: "hi"}, "m")
	if text.Type != model.ContentText || text.Text != "hi" {
		t.Fatalf("text: %+v", text)
	}
	photo := &tg.Message{Message: "cap", Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1, AccessHash: 2, FileReference: []byte{1},
		Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 10, H: 20, Size: 300}, &tg.PhotoSize{Type: "x", W: 100, H: 200, Size: 3000}}}}}
	c := convertContent(photo, "m1")
	if c.Type != model.ContentImage || c.Text != "cap" || len(c.Attachments) != 1 {
		t.Fatalf("photo: %+v", c)
	}
	att := c.Attachments[0]
	if att.Width != 100 || att.Size != 3000 || att.State != model.MediaRemote {
		t.Fatalf("photo attachment: %+v", att)
	}
	var ref remoteRef
	_ = json.Unmarshal(att.RemoteRef, &ref)
	if ref.Kind != "photo" || ref.ID != 1 || ref.ThumbSize != "x" {
		t.Fatalf("ref: %+v", ref)
	}
	voice := &tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 5, MimeType: "audio/ogg", Size: 9,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true, Duration: 3}}}}}
	c = convertContent(voice, "m2")
	if c.Type != model.ContentVoice || c.Attachments[0].DurationMs != 3000 || c.Attachments[0].Mime != "audio/ogg" {
		t.Fatalf("voice: %+v", c)
	}
	file := &tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 6, MimeType: "application/pdf",
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "a.pdf"}}}}}
	c = convertContent(file, "m3")
	if c.Type != model.ContentFile || c.Attachments[0].FileName != "a.pdf" {
		t.Fatalf("file: %+v", c)
	}
	geo := &tg.Message{Media: &tg.MessageMediaGeo{Geo: &tg.GeoPoint{Lat: 1.5, Long: 2.5}}}
	c = convertContent(geo, "m4")
	if c.Type != model.ContentLocation || c.Location.Lat != 1.5 || c.Location.Lon != 2.5 {
		t.Fatalf("geo: %+v", c)
	}
	contact := &tg.Message{Media: &tg.MessageMediaContact{FirstName: "A", LastName: "B", PhoneNumber: "+1"}}
	c = convertContent(contact, "m5")
	if c.Type != model.ContentContact || c.Contacts[0].Name != "A B" || c.Contacts[0].Phones[0] != "+1" {
		t.Fatalf("contact: %+v", c)
	}
	unsupported := &tg.Message{Media: &tg.MessageMediaDice{Value: 3, Emoticon: "🎲"}}
	c = convertContent(unsupported, "m6")
	if c.Type != model.ContentUnsupported || c.Unsupported.PlatformType != "messageMediaDice" {
		t.Fatalf("unsupported: %+v", c)
	}
}

func TestSenderAndHints(t *testing.T) {
	acc := &account{seen: map[int]string{}, self: &tg.User{ID: 9}}
	if got := acc.senderOf(nil, &tg.PeerUser{UserID: 5}, false); got != "5" {
		t.Fatalf("inbound dm sender: %s", got)
	}
	if got := acc.senderOf(nil, &tg.PeerUser{UserID: 5}, true); got != "9" {
		t.Fatalf("outbound sender: %s", got)
	}
	if got := acc.senderOf(&tg.PeerUser{UserID: 3}, &tg.PeerChat{ChatID: 1}, false); got != "3" {
		t.Fatalf("group sender: %s", got)
	}
	ents := tg.Entities{Users: map[int64]*tg.User{5: {ID: 5, FirstName: "Al", LastName: "Ice", Username: "alice", Phone: "1"}},
		Channels: map[int64]*tg.Channel{8: {ID: 8, Title: "News", Broadcast: true}}}
	if h := acc.chatHint(&tg.PeerUser{UserID: 5}, ents); h.Kind != model.ChatDirect || h.Name != "Al Ice" {
		t.Fatalf("dm hint: %+v", h)
	}
	if h := acc.chatHint(&tg.PeerChannel{ChannelID: 8}, ents); h.Kind != model.ChatChannel || h.Name != "News" {
		t.Fatalf("channel hint: %+v", h)
	}
	s := senderHint("5", ents)
	if s == nil || s.Handle != "@alice" || s.Phone != "+1" || s.Names.Profile != "Al Ice" {
		t.Fatalf("sender hint: %+v", s)
	}
	for i := 0; i < seenCap+10; i++ {
		acc.remember(i, "c")
	}
	if len(acc.seen) != seenCap {
		t.Fatalf("seen cap: %d", len(acc.seen))
	}
}
