package telegram

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/gotd/td/tg"

	"gimhq/chat-bridge/internal/adapter"
	"gimhq/chat-bridge/internal/adapters/base"
	"gimhq/chat-bridge/internal/model"
)

const flowBot = "bot"

// userCaps is what a user account can do.
var userCaps = []string{
	adapter.CapSendText, adapter.CapSendMedia, adapter.CapSendLocation, adapter.CapSendContact, adapter.CapReply,
	adapter.CapEdit, adapter.CapDelete, adapter.CapReaction, adapter.CapChatRead, adapter.CapChatTyping,
	adapter.CapChatResolve, adapter.CapChatMembers, adapter.CapPresence, adapter.CapReceipts, adapter.CapMarkdown,
	adapter.CapChatCreate, adapter.CapHistory, adapter.CapSelfUpdate,
}

// botCaps is what a bot account can do: only methods bots may call, plus inline buttons.
var botCaps = []string{
	adapter.CapSendText, adapter.CapSendMedia, adapter.CapSendLocation, adapter.CapSendContact, adapter.CapReply,
	adapter.CapEdit, adapter.CapDelete, adapter.CapReaction, adapter.CapChatTyping, adapter.CapChatResolve,
	adapter.CapMarkdown, adapter.CapButtons,
}

// AccountCapabilities returns the bot or user capability set of an account.
func (a *Adapter) AccountCapabilities(accountID string) []string {
	acc, err := a.accounts.Get(accountID)
	if err != nil {
		return nil
	}
	if acc.isBot() {
		return botCaps
	}
	return userCaps
}

func (acc *account) botMarker() string { return filepath.Join(acc.dir, "bot") }

// isBot reports whether the account is logged in as a bot. Before the first connect after a
// restart it falls back to the marker file written at login.
func (acc *account) isBot() bool {
	acc.mu.Lock()
	self := acc.self
	acc.mu.Unlock()
	if self != nil {
		return self.Bot
	}
	_, err := os.Stat(acc.botMarker())
	return err == nil
}

// markBot keeps the marker file in line with the logged-in identity.
func (acc *account) markBot(bot bool) {
	if !bot {
		_ = os.Remove(acc.botMarker())
		return
	}
	if err := os.WriteFile(acc.botMarker(), nil, 0o600); err != nil {
		acc.rep.Log.Warn("bot marker", "err", err)
	}
}

// loginBot signs in with a token from @BotFather.
func (acc *account) loginBot(ctx context.Context, token string) model.LoginStep {
	cli := acc.client()
	acc.login.Cancel()
	if _, err := cli.Auth().Bot(ctx, token); err != nil {
		return base.Failed(flowBot, err.Error())
	}
	self, err := acc.online(ctx)
	if err != nil {
		return base.Failed(flowBot, err.Error())
	}
	return model.LoginStep{Flow: flowBot, Step: model.StepDone, Self: self}
}

// inlineMarkup renders buttons as a Telegram inline keyboard.
func inlineMarkup(rows [][]model.Button) *tg.ReplyInlineMarkup {
	kb := &tg.ReplyInlineMarkup{}
	for _, row := range rows {
		var r tg.KeyboardInlineButtonRow
		for _, b := range row {
			var t tg.InlineButtonTypeClass = &tg.InlineButtonTypeCallback{Data: []byte(b.Data)}
			if b.URL != "" {
				t = &tg.InlineButtonTypeURL{URL: b.URL}
			}
			r.Buttons = append(r.Buttons, tg.KeyboardInlineButton{Text: b.Text, Type: t})
		}
		kb.Rows = append(kb.Rows, r)
	}
	return kb
}

// inlineButtons maps an inline keyboard to buttons; kinds other than callback and URL are skipped.
func inlineButtons(rm tg.ReplyMarkupClass) [][]model.Button {
	kb, ok := rm.(*tg.ReplyInlineMarkup)
	if !ok {
		return nil
	}
	var rows [][]model.Button
	for _, r := range kb.Rows {
		var row []model.Button
		for _, b := range r.Buttons {
			switch v := b.Type.(type) {
			case *tg.InlineButtonTypeCallback:
				row = append(row, model.Button{Text: b.Text, Data: string(v.Data)})
			case *tg.InlineButtonTypeURL:
				row = append(row, model.Button{Text: b.Text, URL: v.URL})
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	return rows
}

// pressEvent maps a callback query (a press on a data button) to an adapter event.
func pressEvent(u *tg.UpdateBotCallbackQuery, e tg.Entities, at time.Time) adapter.Event {
	chat := peerID(u.Peer)
	user := userID(u.UserID)
	return adapter.Event{Kind: adapter.EvButton, ChatID: chat, MessageID: messageID(chat, u.MsgID), UserID: user,
		Sender: senderHint(user, e), Data: string(u.Data), PressID: strconv.FormatInt(u.QueryID, 10), At: at}
}

func (acc *account) wireBotHandlers(d tg.UpdateDispatcher) {
	d.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotCallbackQuery) error {
		acc.rep.Log.Debug("callback query", "query", u.QueryID, "message", u.MsgID)
		acc.applyEntities(ctx, e)
		acc.rep.Events(pressEvent(u, e, time.Now().UTC()))
		return nil
	})
}

// AnswerPress answers a callback query (messages.setBotCallbackAnswer).
func (a *Adapter) AnswerPress(ctx context.Context, accountID, pressID, text string, alert bool) error {
	_, api, err := a.online(accountID)
	if err != nil {
		return err
	}
	q, err := strconv.ParseInt(pressID, 10, 64)
	if err != nil {
		return adapter.Errorf(adapter.ErrInvalidTarget, "bad press id %q", pressID)
	}
	if _, err := api.MessagesSetBotCallbackAnswer(ctx, &tg.MessagesSetBotCallbackAnswerRequest{QueryID: q, Message: text, Alert: alert}); err != nil {
		return base.PlatformErr("answer press", err)
	}
	return nil
}

// infoCaps is what the platform offers across user and bot accounts.
func infoCaps() []string {
	caps := slices.Clone(userCaps)
	return append(caps, adapter.CapButtons)
}
