package telegram

import (
	"context"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/i18n"
)

// Where a language/quality picker was opened from, carried in callback data
// so the bot knows what to show after a choice.
const (
	originWelcome  = "w" // first contact: show help afterwards
	originSettings = "s" // settings menu: return to it
)

// --- Pickers and views ---

// languageKeyboard shows one flag button per language, two per row, with a
// check on current. From settings it adds a back button.
func languageKeyboard(lang, current i18n.Lang, origin string) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	for _, info := range i18n.Languages {
		label := info.Flag + " " + info.Name
		if info.Code == current {
			label = "✓ " + label
		}
		row = append(row, models.InlineKeyboardButton{Text: label, CallbackData: "lang:" + string(info.Code) + ":" + origin})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	if origin == originSettings {
		rows = append(rows, []models.InlineKeyboardButton{{Text: i18n.T(lang, "btn_back"), CallbackData: "set:home"}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// sendLanguagePicker asks a chat to choose a language. The prompt is shown
// in every language because none has been chosen yet.
func (h *handler) sendLanguagePicker(ctx context.Context, chat models.Chat, origin string) {
	current, _ := h.storedLang(ctx, chat.ID)
	if _, err := h.send(ctx, &bot.SendMessageParams{
		ChatID:      chat.ID,
		Text:        i18n.PickerPrompt(),
		ReplyMarkup: languageKeyboard(i18n.Default, current, origin),
	}); err != nil {
		h.log.Warn("send language picker failed", "chat_id", chat.ID, "error", err)
	}
}

// settingsView renders the settings menu for a chat. Groups only have a
// language; private chats also have the user's default quality.
func (h *handler) settingsView(ctx context.Context, lang i18n.Lang, chat models.Chat, userID int64) (string, *models.InlineKeyboardMarkup) {
	langBtn := models.InlineKeyboardButton{Text: i18n.T(lang, "btn_language"), CallbackData: "set:lang"}
	if chat.Type != models.ChatTypePrivate {
		mode, err := db.GetGroupMode(ctx, h.pool, chat.ID)
		if err != nil {
			h.log.Warn("load group mode failed", "chat_id", chat.ID, "error", err)
		}
		links := groupModeLabel(lang, mode)
		return i18n.T(lang, "settings_title_group", i18n.Label(lang)) + "\n" + i18n.T(lang, "settings_links_line", links),
			&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
				{langBtn},
				{{Text: i18n.T(lang, "btn_group_links", links), CallbackData: "set:gmode"}},
			}}
	}
	quality := qualityLabel(lang, h.currentQuality(ctx, userID))
	return i18n.T(lang, "settings_title", i18n.Label(lang), quality),
		&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			langBtn,
			{Text: i18n.T(lang, "btn_quality"), CallbackData: "set:quality"},
		}}}
}

func (h *handler) cmdSettings(ctx context.Context, r *request) {
	text, kb := h.settingsView(ctx, r.lang, r.msg.Chat, r.msg.From.ID)
	if _, err := h.send(ctx, &bot.SendMessageParams{ChatID: r.msg.Chat.ID, Text: text, ReplyMarkup: kb}); err != nil {
		h.log.Warn("send settings failed", "error", err)
	}
}

func (h *handler) cmdLanguage(ctx context.Context, r *request) {
	h.sendLanguagePicker(ctx, r.msg.Chat, originSettings)
}

// --- Permissions ---

// canManage reports whether userID may change chat's settings: anyone in a
// private chat, bot admins anywhere, and the group's own admins.
func (h *handler) canManage(ctx context.Context, chat models.Chat, userID int64) bool {
	if chat.Type == models.ChatTypePrivate || isAdmin(h.cfg, userID) {
		return true
	}
	member, err := h.b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chat.ID, UserID: userID})
	if err != nil {
		h.log.Warn("get chat member failed", "chat_id", chat.ID, "user_id", userID, "error", err)
		return false
	}
	return member.Type == models.ChatMemberTypeOwner || member.Type == models.ChatMemberTypeAdministrator
}

// --- Callbacks ---

// onSettingsCallback handles "lang:<code>:<origin>" and "set:<page>".
func (h *handler) onSettingsCallback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	m := cb.Message.Message
	if m == nil {
		// The message is too old to edit; nothing sensible to update.
		h.answerCallback(ctx, cb.ID, i18n.T(i18n.Guess(cb.From.LanguageCode), "button_expired"))
		return
	}
	chat := m.Chat
	lang := h.langFor(ctx, chat.ID, &cb.From)

	kind, rest, _ := strings.Cut(cb.Data, ":")
	switch kind {
	case "lang":
		code, origin, _ := strings.Cut(rest, ":")
		newLang, ok := i18n.Parse(code)
		if !ok || (origin != originWelcome && origin != originSettings) {
			h.answerCallback(ctx, cb.ID, i18n.T(lang, "button_invalid"))
			return
		}
		if !h.canManage(ctx, chat, cb.From.ID) {
			h.answerCallback(ctx, cb.ID, i18n.T(lang, "lang_group_admins_only"))
			return
		}
		if err := db.SetChatLanguage(ctx, h.pool, chat.ID, string(newLang)); err != nil {
			h.log.Error("save chat language failed", "chat_id", chat.ID, "error", err)
			h.answerCallback(ctx, cb.ID, i18n.T(lang, "generic_error"))
			return
		}
		h.log.Info("language set", "chat_id", chat.ID, "user_id", cb.From.ID, "language", newLang)
		h.answerCallback(ctx, cb.ID, i18n.T(newLang, "lang_set", i18n.Label(newLang)))
		h.setChatCommands(ctx, chat.ID, newLang)

		if origin == originWelcome {
			h.editMarkup(ctx, m, i18n.T(newLang, "lang_set", i18n.Label(newLang)), nil)
			h.reply(ctx, chat.ID, h.helpText(ctx, newLang, cb.From.ID))
			return
		}
		text, kb := h.settingsView(ctx, newLang, chat, cb.From.ID)
		h.editMarkup(ctx, m, text, kb)

	case "set":
		h.answerCallback(ctx, cb.ID, "")
		switch rest {
		case "home":
			text, kb := h.settingsView(ctx, lang, chat, cb.From.ID)
			h.editMarkup(ctx, m, text, kb)
		case "lang":
			current, _ := h.storedLang(ctx, chat.ID)
			h.editMarkup(ctx, m, i18n.PickerPrompt(), languageKeyboard(lang, current, originSettings))
		case "gmode":
			if !h.canManage(ctx, chat, cb.From.ID) {
				h.answerCallback(ctx, cb.ID, i18n.T(lang, "lang_group_admins_only"))
				return
			}
			mode, _ := db.GetGroupMode(ctx, h.pool, chat.ID)
			next := db.GroupModeCommand
			if mode == db.GroupModeCommand {
				next = db.GroupModeAuto
			}
			if err := db.SetGroupMode(ctx, h.pool, chat.ID, next); err != nil {
				h.log.Error("save group mode failed", "chat_id", chat.ID, "error", err)
				h.answerCallback(ctx, cb.ID, i18n.T(lang, "generic_error"))
				return
			}
			h.log.Info("group mode set", "chat_id", chat.ID, "user_id", cb.From.ID, "mode", next)
			text, kb := h.settingsView(ctx, lang, chat, cb.From.ID)
			h.editMarkup(ctx, m, text, kb)
		case "quality":
			current := h.currentQuality(ctx, cb.From.ID)
			h.editMarkup(ctx, m, i18n.T(lang, "quality_current", qualityLabel(lang, current)), qualityPreferenceKeyboard(lang, current, true))
		}
	}
}

func (h *handler) editMarkup(ctx context.Context, m *models.Message, text string, kb *models.InlineKeyboardMarkup) {
	params := &bot.EditMessageTextParams{ChatID: m.Chat.ID, MessageID: m.ID, Text: truncateRunes(text, maxMessageLen)}
	if kb != nil {
		params.ReplyMarkup = kb
	}
	if _, err := h.b.EditMessageText(ctx, params); err != nil && !strings.Contains(err.Error(), "message is not modified") {
		h.log.Warn("edit message failed", "chat_id", m.Chat.ID, "message_id", m.ID, "error", err)
	}
}

// onMyChatMember greets a group the bot was just added to with the language
// picker, unless the group already chose one.
func (h *handler) onMyChatMember(ctx context.Context, _ *bot.Bot, update *models.Update) {
	u := update.MyChatMember
	if u.Chat.Type != models.ChatTypeGroup && u.Chat.Type != models.ChatTypeSupergroup {
		return
	}
	wasOut := u.OldChatMember.Type == models.ChatMemberTypeLeft || u.OldChatMember.Type == models.ChatMemberTypeBanned
	isIn := u.NewChatMember.Type == models.ChatMemberTypeMember || u.NewChatMember.Type == models.ChatMemberTypeAdministrator
	if !wasOut || !isIn {
		return
	}
	if _, chosen := h.storedLang(ctx, u.Chat.ID); chosen {
		return
	}
	h.log.Info("added to group", "chat_id", u.Chat.ID, "by", u.From.ID)
	h.sendLanguagePicker(ctx, u.Chat, originWelcome)
}

// --- Command menus ---

var menuCommands = []string{"start", "dl", "queue", "history", "mp3", "clip", "gif", "follow", "following", "quality", "setquality", "playlist", "cancel", "settings"}

func groupModeLabel(lang i18n.Lang, mode string) string {
	if mode == db.GroupModeCommand {
		return i18n.T(lang, "group_links_command")
	}
	return i18n.T(lang, "group_links_auto")
}

func commandMenu(lang i18n.Lang) []models.BotCommand {
	cmds := make([]models.BotCommand, 0, len(menuCommands))
	for _, c := range menuCommands {
		cmds = append(cmds, models.BotCommand{Command: c, Description: i18n.T(lang, "cmd_"+c)})
	}
	return cmds
}

// SetCommandMenus publishes the "/" command menu in every language, keyed
// by the user's Telegram client language. Chats that chose a language get a
// per-chat menu when they choose.
func SetCommandMenus(ctx context.Context, b *bot.Bot, log *slog.Logger) {
	if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: commandMenu(i18n.Default)}); err != nil {
		log.Warn("set default command menu failed", "error", err)
	}
	for _, info := range i18n.Languages {
		if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: commandMenu(info.Code), LanguageCode: string(info.Code)}); err != nil {
			log.Warn("set command menu failed", "language", info.Code, "error", err)
		}
	}
}

func (h *handler) setChatCommands(ctx context.Context, chatID int64, lang i18n.Lang) {
	if _, err := h.b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: commandMenu(lang),
		Scope:    &models.BotCommandScopeChat{ChatID: chatID},
	}); err != nil {
		h.log.Warn("set chat command menu failed", "chat_id", chatID, "error", err)
	}
}
