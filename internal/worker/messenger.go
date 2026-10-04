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
	"github.com/meanii/downly/internal/media"
	"github.com/meanii/downly/internal/tgutil"
)

// SendOptions tune how media is delivered.
type SendOptions struct {
	Caption string
	// ReplyTo is the message to reply to (0 = none).
	ReplyTo int
}

// Messenger is everything the worker needs from Telegram.
type Messenger interface {
	// Edit makes a single attempt to replace a status message's text.
	Edit(ctx context.Context, chatID int64, messageID int, text string) error
	// Send posts a new text message.
	Send(ctx context.Context, chatID int64, text string) error
	// SendResult uploads a finished download and returns the Telegram file
	// IDs of what was sent, for the cache.
	SendResult(ctx context.Context, chatID int64, res *downloader.Result, opts SendOptions) ([]media.Item, error)
	// SendCached re-sends media Telegram already stores.
	SendCached(ctx context.Context, chatID int64, items []media.Item, meta media.Meta, opts SendOptions) error
	// EditInlineMedia replaces an inline-mode message with media.
	EditInlineMedia(ctx context.Context, inlineMessageID string, item media.Item, caption string) error
	// EditInlineText replaces an inline-mode message's text.
	EditInlineText(ctx context.Context, inlineMessageID, text string) error
	// Delete removes a message the bot sent.
	Delete(ctx context.Context, chatID int64, messageID int) error
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

func (m TelegramMessenger) Delete(ctx context.Context, chatID int64, messageID int) error {
	if messageID == 0 {
		return nil
	}
	_, err := m.Bot.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: messageID})
	return err
}

func replyParams(opts SendOptions) *models.ReplyParameters {
	if opts.ReplyTo == 0 {
		return nil
	}
	// The original message may have been deleted meanwhile; deliver anyway.
	return &models.ReplyParameters{MessageID: opts.ReplyTo, AllowSendingWithoutReply: true}
}

func (m TelegramMessenger) SendResult(ctx context.Context, chatID int64, res *downloader.Result, opts SendOptions) ([]media.Item, error) {
	if len(res.More) > 0 {
		return sendAlbum(ctx, m.Bot, chatID, res, opts)
	}
	f, err := os.Open(res.FilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	msg, err := sendMedia(ctx, m.Bot, chatID, f, res, opts)
	if err != nil {
		return nil, err
	}
	if item, ok := media.ItemFromMessage(msg); ok {
		return []media.Item{item}, nil
	}
	return nil, nil
}

func (m TelegramMessenger) SendCached(ctx context.Context, chatID int64, items []media.Item, meta media.Meta, opts SendOptions) error {
	if len(items) == 0 {
		return errors.New("no cached media")
	}
	if len(items) == 1 {
		return sendCachedOne(ctx, m.Bot, chatID, items[0], meta, opts)
	}
	kinds := make([]media.Kind, len(items))
	for i, it := range items {
		kinds[i] = it.Kind
	}
	for gi, group := range albumGroups(kinds) {
		o := opts
		if gi > 0 {
			o.Caption = ""
		}
		if len(group) == 1 {
			if err := sendCachedOne(ctx, m.Bot, chatID, items[group[0]], meta, o); err != nil {
				return err
			}
			continue
		}
		var inputs []models.InputMedia
		for n, i := range group {
			caption := ""
			if n == 0 {
				caption = o.Caption
			}
			inputs = append(inputs, inputMediaFor(items[i].Kind, items[i].FileID, nil, caption))
		}
		if _, err := m.Bot.SendMediaGroup(ctx, &bot.SendMediaGroupParams{ChatID: chatID, Media: inputs, ReplyParameters: replyParams(o)}); err != nil {
			return err
		}
	}
	return nil
}

// maxAlbumItems is Telegram's limit for one media group.
const maxAlbumItems = 10

func sendCachedOne(ctx context.Context, b *bot.Bot, chatID int64, it media.Item, meta media.Meta, opts SendOptions) error {
	file := &models.InputFileString{Data: it.FileID}
	rp := replyParams(opts)
	var err error
	switch it.Kind {
	case media.Video:
		_, err = b.SendVideo(ctx, &bot.SendVideoParams{ChatID: chatID, Video: file, Caption: opts.Caption, SupportsStreaming: true, ReplyParameters: rp})
	case media.Audio:
		_, err = b.SendAudio(ctx, &bot.SendAudioParams{ChatID: chatID, Audio: file, Caption: opts.Caption, Title: meta.Title, Performer: meta.Performer, ReplyParameters: rp})
	case media.Photo:
		_, err = b.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chatID, Photo: file, Caption: opts.Caption, ReplyParameters: rp})
	case media.Animation:
		_, err = b.SendAnimation(ctx, &bot.SendAnimationParams{ChatID: chatID, Animation: file, Caption: opts.Caption, ReplyParameters: rp})
	default:
		_, err = b.SendDocument(ctx, &bot.SendDocumentParams{ChatID: chatID, Document: file, Caption: opts.Caption, ReplyParameters: rp})
	}
	return err
}

// inputMediaFor builds an album/edit entry for a file ID or, with r set, an
// upload attached under name fileID.
func inputMediaFor(kind media.Kind, fileID string, r *os.File, caption string) models.InputMedia {
	ref := fileID
	if r != nil {
		ref = "attach://" + fileID
	}
	switch kind {
	case media.Video:
		v := &models.InputMediaVideo{Media: ref, Caption: caption, SupportsStreaming: true}
		if r != nil {
			v.MediaAttachment = r
		}
		return v
	case media.Audio:
		a := &models.InputMediaAudio{Media: ref, Caption: caption}
		if r != nil {
			a.MediaAttachment = r
		}
		return a
	case media.Photo:
		p := &models.InputMediaPhoto{Media: ref, Caption: caption}
		if r != nil {
			p.MediaAttachment = r
		}
		return p
	case media.Animation:
		g := &models.InputMediaAnimation{Media: ref, Caption: caption}
		if r != nil {
			g.MediaAttachment = r
		}
		return g
	default:
		d := &models.InputMediaDocument{Media: ref, Caption: caption}
		if r != nil {
			d.MediaAttachment = r
		}
		return d
	}
}

func (m TelegramMessenger) EditInlineMedia(ctx context.Context, inlineMessageID string, item media.Item, caption string) error {
	_, err := m.Bot.EditMessageMedia(ctx, &bot.EditMessageMediaParams{
		InlineMessageID: inlineMessageID,
		Media:           inputMediaFor(item.Kind, item.FileID, nil, caption),
	})
	return err
}

func (m TelegramMessenger) EditInlineText(ctx context.Context, inlineMessageID, text string) error {
	_, err := m.Bot.EditMessageText(ctx, &bot.EditMessageTextParams{InlineMessageID: inlineMessageID, Text: text})
	if tgutil.IsNotModified(err) {
		return nil
	}
	return err
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
func uploadWithRetry(ctx context.Context, msg Messenger, chatID int64, res *downloader.Result, opts SendOptions, size int64) ([]media.Item, error) {
	upCtx, cancel := context.WithTimeout(ctx, uploadTimeout(size))
	defer cancel()
	var items []media.Item
	var err error
	wait := uploadBackoff
	for attempt := 1; attempt <= uploadAttempts; attempt++ {
		err = tgutil.Call(upCtx, 3, func() error {
			var sendErr error
			items, sendErr = msg.SendResult(upCtx, chatID, res, opts)
			return sendErr
		})
		if err == nil || isPermanentUploadErr(err) || upCtx.Err() != nil || attempt == uploadAttempts {
			break
		}
		select {
		case <-upCtx.Done():
			return nil, errors.Join(err, upCtx.Err())
		case <-time.After(wait):
		}
		wait *= 3
	}
	return items, err
}

func sendMedia(ctx context.Context, b *bot.Bot, chatID int64, f *os.File, res *downloader.Result, opts SendOptions) (*models.Message, error) {
	upload := &models.InputFileUpload{Filename: res.FileName, Data: f}
	caption := opts.Caption
	rp := replyParams(opts)

	var msg *models.Message
	var err error
	switch res.Media {
	case downloader.MediaVideo:
		params := &bot.SendVideoParams{
			ChatID:            chatID,
			Video:             upload,
			Caption:           caption,
			SupportsStreaming: true,
			Duration:          res.Duration,
			ReplyParameters:   rp,
		}
		if res.ThumbnailPath != "" {
			if thumbFile, terr := os.Open(res.ThumbnailPath); terr == nil {
				defer thumbFile.Close()
				params.Thumbnail = &models.InputFileUpload{Filename: filepath.Base(res.ThumbnailPath), Data: thumbFile}
			}
		}
		msg, err = b.SendVideo(ctx, params)
	case downloader.MediaAudio:
		msg, err = b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:          chatID,
			Audio:           upload,
			Caption:         caption,
			Title:           res.Title,
			Performer:       res.Performer,
			Duration:        res.Duration,
			ReplyParameters: rp,
		})
	case downloader.MediaPhoto:
		msg, err = b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:          chatID,
			Photo:           upload,
			Caption:         caption,
			ReplyParameters: rp,
		})
	default:
		return b.SendDocument(ctx, &bot.SendDocumentParams{ChatID: chatID, Document: upload, Caption: caption, ReplyParameters: rp})
	}
	// Telegram rejects some files as video/audio/photo (odd codecs, image
	// dimensions); sending as a plain document almost always works.
	if err != nil && errors.Is(err, bot.ErrorBadRequest) {
		if _, serr := f.Seek(0, 0); serr != nil {
			return nil, err
		}
		upload = &models.InputFileUpload{Filename: res.FileName, Data: f}
		if doc, err2 := b.SendDocument(ctx, &bot.SendDocumentParams{ChatID: chatID, Document: upload, Caption: caption, ReplyParameters: rp}); err2 == nil {
			return doc, nil
		}
	}
	return msg, err
}

// MetaFromResult describes a finished download for captions and the cache.
func MetaFromResult(res *downloader.Result, size int64) media.Meta {
	return media.Meta{Title: res.Title, Performer: res.Performer, Platform: res.Platform, Duration: res.Duration, SizeBytes: size}
}

// Caption renders the caption for media in lang.
func Caption(lang i18n.Lang, meta media.Meta) string {
	parts := []string{}
	if meta.Title != "" {
		parts = append(parts, truncateRunes(meta.Title, 100))
	}
	if meta.Duration > 0 {
		m := meta.Duration / 60
		s := meta.Duration % 60
		parts = append(parts, i18n.T(lang, "caption_duration", fmt.Sprintf("%d:%02d", m, s)))
	}
	if meta.Platform != "" && meta.Platform != "unknown" {
		parts = append(parts, i18n.T(lang, "caption_source", meta.Platform))
	}
	if len(parts) == 0 {
		return i18n.T(lang, "caption_done")
	}
	return strings.Join(parts, "\n")
}

func buildCaption(lang i18n.Lang, res *downloader.Result) string {
	return Caption(lang, MetaFromResult(res, 0))
}
