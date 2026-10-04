package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/media"
)

// kindOf maps a downloaded file's type to how Telegram will store it.
func kindOf(m downloader.MediaType) media.Kind {
	switch m {
	case downloader.MediaVideo:
		return media.Video
	case downloader.MediaAudio:
		return media.Audio
	case downloader.MediaPhoto:
		return media.Photo
	case downloader.MediaAnimation:
		return media.Animation
	}
	return media.Document
}

// albumClass is which items Telegram lets share one media group: photos and
// videos mix; audio and documents only group with their own kind;
// animations can't be grouped at all.
func albumClass(k media.Kind) string {
	switch k {
	case media.Photo, media.Video:
		return "visual"
	case media.Audio:
		return "audio"
	case media.Document:
		return "document"
	}
	return "single:" + string(k)
}

// albumGroups splits items (by kind) into consecutive runs that can each be
// sent as one media group of at most maxAlbumItems. Order is preserved.
func albumGroups(kinds []media.Kind) [][]int {
	var groups [][]int
	for i, k := range kinds {
		n := len(groups)
		if n > 0 {
			last := groups[n-1]
			prev := kinds[last[0]]
			if albumClass(prev) == albumClass(k) && len(last) < maxAlbumItems && !strings.HasPrefix(albumClass(k), "single") {
				groups[n-1] = append(last, i)
				continue
			}
		}
		groups = append(groups, []int{i})
	}
	return groups
}

// sendAlbum uploads a multi-file result as media groups, with the caption on
// the first item. A group Telegram rejects is retried item by item (which
// falls back to documents). It returns the file IDs of everything sent.
func sendAlbum(ctx context.Context, b *bot.Bot, chatID int64, res *downloader.Result, opts SendOptions) ([]media.Item, error) {
	items := res.Items()
	kinds := make([]media.Kind, len(items))
	for i, it := range items {
		kinds[i] = kindOf(it.Media)
	}
	var sent []media.Item
	for gi, group := range albumGroups(kinds) {
		groupOpts := opts
		if gi > 0 {
			groupOpts.Caption = ""
		}
		got, err := sendAlbumGroup(ctx, b, chatID, res, items, kinds, group, groupOpts)
		if err != nil {
			return sent, err
		}
		sent = append(sent, got...)
	}
	return sent, nil
}

func sendAlbumGroup(ctx context.Context, b *bot.Bot, chatID int64, res *downloader.Result, items []downloader.Item, kinds []media.Kind, group []int, opts SendOptions) ([]media.Item, error) {
	if len(group) == 1 {
		return sendSingleItem(ctx, b, chatID, res, items[group[0]], opts)
	}
	var files []*os.File
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	var inputs []models.InputMedia
	for n, i := range group {
		f, err := os.Open(items[i].FilePath)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
		caption := ""
		if n == 0 {
			caption = opts.Caption
		}
		inputs = append(inputs, inputMediaFor(kinds[i], fmt.Sprintf("file%d", i), f, caption))
	}
	msgs, err := b.SendMediaGroup(ctx, &bot.SendMediaGroupParams{ChatID: chatID, Media: inputs, ReplyParameters: replyParams(opts)})
	if err != nil {
		if !errors.Is(err, bot.ErrorBadRequest) {
			return nil, err
		}
		// One odd file can sink the whole group; send them one by one.
		var sent []media.Item
		for n, i := range group {
			o := opts
			if n > 0 {
				o.Caption = ""
			}
			got, err := sendSingleItem(ctx, b, chatID, res, items[i], o)
			if err != nil {
				return sent, err
			}
			sent = append(sent, got...)
		}
		return sent, nil
	}
	var sent []media.Item
	for _, m := range msgs {
		if it, ok := media.ItemFromMessage(m); ok {
			sent = append(sent, it)
		}
	}
	return sent, nil
}

func sendSingleItem(ctx context.Context, b *bot.Bot, chatID int64, res *downloader.Result, it downloader.Item, opts SendOptions) ([]media.Item, error) {
	f, err := os.Open(it.FilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	single := *res
	single.FilePath, single.FileName, single.Media, single.More = it.FilePath, it.FileName, it.Media, nil
	msg, err := sendMedia(ctx, b, chatID, f, &single, opts)
	if err != nil {
		return nil, err
	}
	if item, ok := media.ItemFromMessage(msg); ok {
		return []media.Item{item}, nil
	}
	return nil, nil
}
