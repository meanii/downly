package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/i18n"
	"github.com/meanii/downly/internal/tgutil"
)

// Messenger is everything the worker needs from Telegram.
type Messenger interface {
	// Edit makes a single attempt to replace a status message's text.
	Edit(ctx context.Context, chatID int64, messageID int, text string) error
	// Send posts a new text message.
	Send(ctx context.Context, chatID int64, text string) error
	// SendResult uploads a finished download with the given caption.
	SendResult(ctx context.Context, chatID int64, res *downloader.Result, caption string) error
}

// Downloader is everything the worker needs from yt-dlp.
type Downloader interface {
	Download(ctx context.Context, workDir string, jobID int64, url string, onProgress func(string, int)) (*downloader.Result, error)
	DownloadWithQuality(ctx context.Context, workDir string, jobID int64, url, quality string, onProgress func(string, int)) (*downloader.Result, error)
	DownloadAudio(ctx context.Context, workDir string, jobID int64, url string, onProgress func(string, int)) (*downloader.Result, error)
}

// TelegramMessenger implements Messenger with the Bot API.
type TelegramMessenger struct{ Bot *bot.Bot }

func (m TelegramMessenger) Edit(ctx context.Context, chatID int64, messageID int, text string) error {
	if messageID == 0 || text == "" {
		return nil
	}
	_, err := m.Bot.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: messageID, Text: text})
	if tgutil.IsNotModified(err) {
		return nil
	}
	return err
}

func (m TelegramMessenger) Send(ctx context.Context, chatID int64, text string) error {
	_, err := m.Bot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text})
	return err
}

func (m TelegramMessenger) SendResult(ctx context.Context, chatID int64, res *downloader.Result, caption string) error {
	f, err := os.Open(res.FilePath)
	if err != nil {
		return err
	}
	defer f.Close()
	return sendMedia(ctx, m.Bot, chatID, f, res, caption)
}

// uploadTimeout scales with file size, assuming a pessimistic 256 KiB/s uplink.
func uploadTimeout(size int64) time.Duration {
	d := 2*time.Minute + time.Duration(size/(256*1024))*time.Second
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

// uploadAttempts and uploadBackoff bound in-place upload retries before the
// job is handed back to the queue.
var (
	uploadAttempts = 3
	uploadBackoff  = 5 * time.Second
)

// uploadWithRetry sends the result, waiting out 429s and retrying transient
// failures with backoff. Permanent Telegram errors return immediately.
func uploadWithRetry(ctx context.Context, msg Messenger, chatID int64, res *downloader.Result, caption string, size int64) error {
	upCtx, cancel := context.WithTimeout(ctx, uploadTimeout(size))
	defer cancel()
	var err error
	wait := uploadBackoff
	for attempt := 1; attempt <= uploadAttempts; attempt++ {
		err = tgutil.Call(upCtx, 3, func() error { return msg.SendResult(upCtx, chatID, res, caption) })
		if err == nil || isPermanentUploadErr(err) || upCtx.Err() != nil || attempt == uploadAttempts {
			break
		}
		select {
		case <-upCtx.Done():
			return errors.Join(err, upCtx.Err())
		case <-time.After(wait):
		}
		wait *= 3
	}
	return err
}

func sendMedia(ctx context.Context, b *bot.Bot, chatID int64, f *os.File, res *downloader.Result, caption string) error {
	upload := &models.InputFileUpload{Filename: res.FileName, Data: f}

	var err error
	switch res.Media {
	case downloader.MediaVideo:
		params := &bot.SendVideoParams{
			ChatID:            chatID,
			Video:             upload,
			Caption:           caption,
			SupportsStreaming: true,
			Duration:          res.Duration,
		}
		if res.ThumbnailPath != "" {
			if thumbFile, terr := os.Open(res.ThumbnailPath); terr == nil {
				defer thumbFile.Close()
				params.Thumbnail = &models.InputFileUpload{Filename: filepath.Base(res.ThumbnailPath), Data: thumbFile}
			}
		}
		_, err = b.SendVideo(ctx, params)
	case downloader.MediaAudio:
		_, err = b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:   chatID,
			Audio:    upload,
			Caption:  caption,
			Title:    res.Title,
			Duration: res.Duration,
		})
	case downloader.MediaPhoto:
		_, err = b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:  chatID,
			Photo:   upload,
			Caption: caption,
		})
	default:
		_, err = b.SendDocument(ctx, &bot.SendDocumentParams{ChatID: chatID, Document: upload, Caption: caption})
		return err
	}
	// Telegram rejects some files as video/audio/photo (odd codecs, image
	// dimensions); sending as a plain document almost always works.
	if err != nil && errors.Is(err, bot.ErrorBadRequest) {
		if _, serr := f.Seek(0, 0); serr != nil {
			return err
		}
		upload = &models.InputFileUpload{Filename: res.FileName, Data: f}
		if _, err2 := b.SendDocument(ctx, &bot.SendDocumentParams{ChatID: chatID, Document: upload, Caption: caption}); err2 == nil {
			return nil
		}
	}
	return err
}

func buildCaption(lang i18n.Lang, res *downloader.Result) string {
	parts := []string{}
	if res.Title != "" {
		parts = append(parts, truncateRunes(res.Title, 100))
	}
	if res.Duration > 0 {
		m := res.Duration / 60
		s := res.Duration % 60
		parts = append(parts, i18n.T(lang, "caption_duration", fmt.Sprintf("%d:%02d", m, s)))
	}
	if res.Platform != "" && res.Platform != "unknown" {
		parts = append(parts, i18n.T(lang, "caption_source", res.Platform))
	}
	if len(parts) == 0 {
		return i18n.T(lang, "caption_done")
	}
	return strings.Join(parts, "\n")
}
