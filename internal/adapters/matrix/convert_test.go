package matrix

import (
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/model"
)

func TestConvertContent(t *testing.T) {
	text := convertContent(&event.MessageEventContent{MsgType: event.MsgText, Body: "hi"}, event.EventMessage, "e1")
	if text.Type != model.ContentText || text.Text != "hi" || text.Format != "" {
		t.Fatalf("text: %+v", text)
	}
	html := convertContent(&event.MessageEventContent{MsgType: event.MsgText, Body: "hi", Format: event.FormatHTML, FormattedBody: "<b>hi</b>"}, event.EventMessage, "e1")
	if html.Format != "html" || html.Text != "<b>hi</b>" {
		t.Fatalf("html: %+v", html)
	}
	img := convertContent(&event.MessageEventContent{MsgType: event.MsgImage, Body: "cat.png", URL: "mxc://hs/abc",
		Info: &event.FileInfo{MimeType: "image/png", Size: 10, Width: 4, Height: 3}}, event.EventMessage, "e2")
	if img.Type != model.ContentImage || img.Text != "" || len(img.Attachments) != 1 {
		t.Fatalf("image: %+v", img)
	}
	att := img.Attachments[0]
	if att.Mime != "image/png" || att.Width != 4 || att.State != model.MediaRemote {
		t.Fatalf("attachment: %+v", att)
	}
	var ref remoteRef
	_ = json.Unmarshal(att.RemoteRef, &ref)
	if ref.URL != "mxc://hs/abc" {
		t.Fatalf("ref: %+v", ref)
	}
	captioned := convertContent(&event.MessageEventContent{MsgType: event.MsgFile, Body: "look at this", FileName: "doc.pdf", URL: "mxc://hs/d"}, event.EventMessage, "e3")
	if captioned.Text != "look at this" || captioned.Attachments[0].FileName != "doc.pdf" {
		t.Fatalf("caption: %+v", captioned)
	}
	enc := convertContent(&event.MessageEventContent{MsgType: event.MsgImage, Body: "x", File: &event.EncryptedFileInfo{URL: "mxc://hs/enc"}}, event.EventMessage, "e4")
	if enc.Attachments[0].State != model.MediaFailed || enc.Attachments[0].RemoteRef != nil {
		t.Fatalf("encrypted: %+v", enc.Attachments[0])
	}
	voice := convertContent(&event.MessageEventContent{MsgType: event.MsgAudio, Body: "v.ogg", URL: "mxc://hs/v", MSC3245Voice: &event.MSC3245Voice{}}, event.EventMessage, "e5")
	if voice.Type != model.ContentVoice {
		t.Fatalf("voice: %+v", voice)
	}
	loc := convertContent(&event.MessageEventContent{MsgType: event.MsgLocation, Body: "here", GeoURI: "geo:1.5,2.5;u=10"}, event.EventMessage, "e6")
	if loc.Type != model.ContentLocation || loc.Location.Lat != 1.5 || loc.Location.Lon != 2.5 {
		t.Fatalf("location: %+v", loc)
	}
	sticker := convertContent(&event.MessageEventContent{Body: "s", URL: "mxc://hs/s"}, event.EventSticker, "e7")
	if sticker.Type != model.ContentSticker {
		t.Fatalf("sticker: %+v", sticker)
	}
	odd := convertContent(&event.MessageEventContent{MsgType: "m.custom", Body: "?"}, event.EventMessage, "e8")
	if odd.Type != model.ContentUnsupported || odd.Unsupported.PlatformType != "m.custom" {
		t.Fatalf("unsupported: %+v", odd)
	}
}

func TestRelatesAndUserContact(t *testing.T) {
	if relates(adapter.SendRequest{}) != nil {
		t.Fatal("no relation expected")
	}
	rel := relates(adapter.SendRequest{ReplyTo: "$a"})
	if rel.InReplyTo == nil || rel.InReplyTo.EventID != "$a" || rel.Type != "" {
		t.Fatalf("reply: %+v", rel)
	}
	rel = relates(adapter.SendRequest{ThreadID: "$root"})
	if rel.Type != event.RelThread || rel.EventID != "$root" || !rel.IsFallingBack || rel.InReplyTo.EventID != "$root" {
		t.Fatalf("thread: %+v", rel)
	}
	rel = relates(adapter.SendRequest{ThreadID: "$root", ReplyTo: "$b"})
	if rel.IsFallingBack || rel.InReplyTo.EventID != "$b" {
		t.Fatalf("thread reply: %+v", rel)
	}
	c := userContact(id.UserID("@alice:example.org"), "Alice")
	if c.ID != "@alice:example.org" || c.Names.Username != "alice" || c.Names.Profile != "Alice" || c.Handle != c.ID {
		t.Fatalf("contact: %+v", c)
	}
	md := textContent("**bold**", "markdown")
	if md.Format != event.FormatHTML || md.FormattedBody == "" || md.Body != "**bold**" {
		t.Fatalf("markdown: %+v", md)
	}
}
