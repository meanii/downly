package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GetChatLanguage returns the language chosen for a chat (a private chat's
// ID equals the user's ID). ok is false if none has been chosen.
func GetChatLanguage(ctx context.Context, pool *pgxpool.Pool, chatID int64) (lang string, ok bool, err error) {
	var l *string
	err = pool.QueryRow(ctx, `select language from chat_settings where chat_id = $1`, chatID).Scan(&l)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if l == nil {
		return "", false, nil
	}
	return *l, true, nil
}

// SetChatLanguage stores the language for a chat.
func SetChatLanguage(ctx context.Context, pool *pgxpool.Pool, chatID int64, lang string) error {
	_, err := pool.Exec(ctx, `
		insert into chat_settings (chat_id, language) values ($1, $2)
		on conflict (chat_id) do update set language = excluded.language, updated_at = now()
	`, chatID, lang)
	return err
}

// Group link handling modes.
const (
	GroupModeAuto    = "auto"    // download every link posted
	GroupModeCommand = "command" // only links passed to /dl
)

// GetGroupMode returns how a group handles links (default GroupModeAuto).
func GetGroupMode(ctx context.Context, pool *pgxpool.Pool, chatID int64) (string, error) {
	var mode string
	err := pool.QueryRow(ctx, `select group_mode from chat_settings where chat_id = $1`, chatID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return GroupModeAuto, nil
	}
	if err != nil {
		return GroupModeAuto, err
	}
	return mode, nil
}

// SetGroupMode stores how a group handles links.
func SetGroupMode(ctx context.Context, pool *pgxpool.Pool, chatID int64, mode string) error {
	if mode != GroupModeAuto && mode != GroupModeCommand {
		return errors.New("invalid group mode")
	}
	_, err := pool.Exec(ctx, `
		insert into chat_settings (chat_id, group_mode) values ($1, $2)
		on conflict (chat_id) do update set group_mode = excluded.group_mode, updated_at = now()
	`, chatID, mode)
	return err
}
