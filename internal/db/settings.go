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
	err = pool.QueryRow(ctx, `select language from chat_settings where chat_id = $1`, chatID).Scan(&lang)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return lang, true, nil
}

// SetChatLanguage stores the language for a chat.
func SetChatLanguage(ctx context.Context, pool *pgxpool.Pool, chatID int64, lang string) error {
	_, err := pool.Exec(ctx, `
		insert into chat_settings (chat_id, language) values ($1, $2)
		on conflict (chat_id) do update set language = excluded.language, updated_at = now()
	`, chatID, lang)
	return err
}
