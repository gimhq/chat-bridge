// Package matrixcontent converts Matrix message content to the platform-agnostic model. It is
// shared by the Matrix client adapter and by hosted bridgev2 connectors, whose output is Matrix
// content too.
package matrixcontent

import (
	"encoding/json"
	"strconv"
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"gimhq/chat-bridge/internal/model"
)

// RemoteRef is stored per attachment so the bytes can be fetched later. File is set for
// encrypted attachments and carries the key needed to decrypt after download.
type RemoteRef struct {
	URL  string                   `json:"url"`
	File *event.EncryptedFileInfo `json:"file,omitempty"`
}

// Convert maps m.room.message / m.sticker content. mediaID names the attachment row.
func Convert(c *event.MessageEventContent, typ event.Type, mediaID string) model.Content {
	if typ == event.EventSticker {
		return mediaContent(model.ContentSticker, "", mediaID, c)
	}
	switch c.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		out := model.Content{Type: model.ContentText, Text: c.Body}
		if c.Format == event.FormatHTML && c.FormattedBody != "" {
			out.Format, out.Text = "html", c.FormattedBody
		}
		return out
	case event.MsgImage:
		return mediaContent(model.ContentImage, c.Body, mediaID, c)
	case event.MsgVideo:
		return mediaContent(model.ContentVideo, c.Body, mediaID, c)
	case event.MsgAudio:
		t := model.ContentAudio
		if c.MSC3245Voice != nil {
			t = model.ContentVoice
		}
		return mediaContent(t, c.Body, mediaID, c)
	case event.MsgFile:
		return mediaContent(model.ContentFile, c.Body, mediaID, c)
	case event.MsgLocation:
		loc := &model.Location{Name: c.Body}
		if coords, ok := strings.CutPrefix(c.GeoURI, "geo:"); ok {
			parts := strings.Split(strings.SplitN(coords, ";", 2)[0], ",")
			if len(parts) >= 2 {
				loc.Lat, _ = strconv.ParseFloat(parts[0], 64)
				loc.Lon, _ = strconv.ParseFloat(parts[1], 64)
			}
		}
		return model.Content{Type: model.ContentLocation, Text: c.Body, Location: loc}
	}
	return model.Content{Type: model.ContentUnsupported, Text: c.Body, Unsupported: &model.Unsupported{PlatformType: string(c.MsgType)}}
}

func mediaContent(t, caption, mediaID string, c *event.MessageEventContent) model.Content {
	att := model.Attachment{MediaID: mediaID, State: model.MediaRemote}
	if c.FileName != "" {
		att.FileName = c.FileName
	} else if t == model.ContentFile {
		att.FileName = c.Body
	}
	if c.Info != nil {
		att.Mime, att.Size, att.Width, att.Height, att.DurationMs = c.Info.MimeType, int64(c.Info.Size), c.Info.Width, c.Info.Height, int64(c.Info.Duration)
	}
	if att.Mime == "" {
		att.Mime = "application/octet-stream"
	}
	// Caption is only a caption when a filename is present; otherwise body is the file name.
	if c.FileName == "" || c.FileName == caption {
		caption = ""
	}
	if c.File != nil {
		att.RemoteRef, _ = json.Marshal(RemoteRef{URL: string(c.File.URL), File: c.File})
		return model.Content{Type: t, Text: caption, Attachments: []model.Attachment{att}}
	}
	if c.URL != "" {
		att.RemoteRef, _ = json.Marshal(RemoteRef{URL: string(c.URL)})
	}
	return model.Content{Type: t, Text: caption, Attachments: []model.Attachment{att}}
}

// Build renders platform-agnostic content as Matrix message content. Attachments are referenced
// by mediaURL (an mxc URI the receiver can download); the caller uploads first.
func Build(c model.Content, mediaURL string, meta *model.Attachment) (*event.MessageEventContent, event.Type) {
	switch c.Type {
	case model.ContentText:
		mc := &event.MessageEventContent{MsgType: event.MsgText, Body: c.Text}
		if c.Format == "html" {
			mc.Format, mc.FormattedBody = event.FormatHTML, c.Text
		}
		return mc, event.EventMessage
	case model.ContentLocation:
		l := c.Location
		body := c.Text
		if body == "" {
			body = l.Name
		}
		if body == "" {
			body = strconv.FormatFloat(l.Lat, 'f', 6, 64) + "," + strconv.FormatFloat(l.Lon, 'f', 6, 64)
		}
		return &event.MessageEventContent{MsgType: event.MsgLocation, Body: body,
			GeoURI: "geo:" + strconv.FormatFloat(l.Lat, 'f', 6, 64) + "," + strconv.FormatFloat(l.Lon, 'f', 6, 64)}, event.EventMessage
	}
	name := c.Type
	mc := &event.MessageEventContent{Body: name, URL: id.ContentURIString(mediaURL)}
	if meta != nil {
		if meta.FileName != "" {
			name = meta.FileName
			mc.Body = name
		}
		mc.Info = &event.FileInfo{MimeType: meta.Mime, Size: int(meta.Size), Width: meta.Width, Height: meta.Height, Duration: int(meta.DurationMs)}
	}
	if c.Text != "" {
		mc.FileName, mc.Body = name, c.Text
	}
	typ := event.EventMessage
	switch c.Type {
	case model.ContentImage:
		mc.MsgType = event.MsgImage
	case model.ContentVideo:
		mc.MsgType = event.MsgVideo
	case model.ContentAudio:
		mc.MsgType = event.MsgAudio
	case model.ContentVoice:
		mc.MsgType = event.MsgAudio
		mc.MSC3245Voice = &event.MSC3245Voice{}
	case model.ContentSticker:
		typ = event.EventSticker
	default:
		mc.MsgType = event.MsgFile
	}
	return mc, typ
}
