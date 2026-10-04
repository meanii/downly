package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/dbtest"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/subscriptions"
	tg "github.com/meanii/downly/internal/telegram"
	"github.com/meanii/downly/internal/tgtest"
	"github.com/meanii/downly/internal/worker"
)

// fakeYTDLP mimics the parts of yt-dlp the bot relies on. It refuses to run
// unless the URL comes after "--" (the argument-injection guard), prints
// metadata for --dump-single-json, fails like yt-dlp for "private" URLs, and
// otherwise writes a small media file where -o points.
const fakeYTDLP = `#!/usr/bin/env bash
set -u
echo "$*" >> "$(dirname "$0")/calls.log"
out=""; audio=0; url=""; after_dd=0; dump=0; yes_playlist=0; flat=0
while [ $# -gt 0 ]; do
  if [ $after_dd = 1 ]; then url="$1"; shift; continue; fi
  case "$1" in
    --) after_dd=1 ;;
    -o) out="$2"; shift ;;
    -x) audio=1 ;;
    --dump-single-json) dump=1 ;;
    --yes-playlist) yes_playlist=1 ;;
    --flat-playlist) flat=1 ;;
  esac
  shift
done
if [ -z "$url" ]; then echo "ERROR: URL must come after --" >&2; exit 2; fi
case "$url" in
  *private*) echo "ERROR: [youtube] vid42: Private video. Sign in if you've been granted access" >&2; exit 1 ;;
  *unsupported*) echo "ERROR: Unsupported URL: $url" >&2; exit 1 ;;
esac
# Channel/playlist listings come from feed.json, which tests rewrite.
if [ $flat = 1 ]; then
  cat "$(dirname "$0")/feed.json" 2>/dev/null || { echo "ERROR: no feed" >&2; exit 1; }
  exit 0
fi
if [ $dump = 1 ]; then
  printf '%s\n' '{"extractor_key":"Youtube","title":"E2E Видео","id":"vid42","duration":65}'
  exit 0
fi
# A carousel post: three items, but only the first without --yes-playlist.
case "$url" in
  *carousel*)
    n=1; [ $yes_playlist = 1 ] && n=3
    for i in $(seq 1 $n); do
      ext=jpg; [ $i = 2 ] && ext=mp4
      f="${out//"%(id)s"/c$i}"; f="${f//"%(ext)s"/$ext}"
      printf 'ITEM-%s' "$i" > "$f"; sleep 0.05
    done
    exit 0 ;;
esac
ext=mp4; [ $audio = 1 ] && ext=mp3
file="${out//"%(id)s"/vid42}"; file="${file//"%(ext)s"/$ext}"
echo "[download] Destination: $file"
echo "[download]  50.0% of 1.00MiB"
printf 'FAKE-MEDIA-%s' "$ext" > "$file"
echo "[download] 100% of 1.00MiB"
`

// fakeFFmpeg stands in for the GIF conversion: it copies the -i input to
// the output path (the last argument).
const fakeFFmpeg = `#!/usr/bin/env bash
in=""; prev=""; last=""
for a in "$@"; do
  [ "$prev" = "-i" ] && in="$a"
  prev="$a"; last="$a"
done
echo "$*" >> "$(dirname "$0")/ffmpeg.log"
cp "$in" "$last"
`

// A literal public IP keeps URL validation offline (no DNS lookup); the fake
// yt-dlp never connects anywhere.
const videoURL = "https://1.1.1.1/watch?v=vid42"

type env struct {
	t       *testing.T
	api     *tgtest.Server
	b       *bot.Bot
	pool    *pgxpool.Pool
	workDir string
	binDir  string
	poller  *subscriptions.Poller
}

func newEnv(t *testing.T, opts ...func(*config.Root)) *env {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	pool := dbtest.NewPool(t)
	api := tgtest.New(t)

	bin := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(bin, []byte(fakeYTDLP), 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpeg := filepath.Join(filepath.Dir(bin), "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte(fakeFFmpeg), 0o755); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()

	cfg := &config.Root{}
	cfg.Downly.Worker.WorkDir = workDir
	cfg.Downly.Worker.PollIntervalSec = 1
	cfg.Downly.Worker.MaxFileSizeMB = 50
	cfg.Downly.Worker.JobTimeoutMinutes = 5
	cfg.Downly.Limits.MaxQueuedPerUser = 5
	cfg.Downly.Limits.MaxConcurrentPerUser = 2
	cfg.Downly.Limits.MaxRetries = 1
	cfg.Downly.Services.YTDLP.Bin = bin
	for _, o := range opts {
		o(cfg)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b, err := bot.New("123:TEST", bot.WithServerURL(api.URL()), bot.WithNotAsyncHandlers(),
		bot.WithMiddlewares(tg.UserTracker(pool, logger)))
	if err != nil {
		t.Fatal(err)
	}
	controller := worker.NewController()
	tg.RegisterHandlers(logger, cfg, controller, b, pool)

	ctx, cancel := context.WithCancel(context.Background())
	waker := worker.NewWaker()
	go worker.Listen(ctx, logger, pool, waker)
	dl := downloader.YTDLP{
		Bin:           bin,
		MaxFileSizeMB: cfg.Downly.Worker.MaxFileSizeMB,
		// The literal-IP test host stands in for an album site.
		AlbumHosts: []string{"1.1.1.1"},
		FFmpegBin:  ffmpeg,
		Logger:     logger,
	}
	w := &worker.Worker{
		ID:         "e2e-1",
		Cfg:        cfg,
		Pool:       pool,
		DL:         dl,
		Msg:        worker.TelegramMessenger{Bot: b},
		Controller: controller,
		Waker:      waker,
		Log:        logger,
	}
	done := make(chan struct{})
	go func() { w.Run(ctx, ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	poller := &subscriptions.Poller{Pool: pool, Fetcher: dl, Interval: time.Hour, Log: logger}
	return &env{t: t, api: api, b: b, pool: pool, workDir: workDir, binDir: filepath.Dir(bin), poller: poller}
}

// ytdlpCalls returns the argument lists of yt-dlp download calls.
func (e *env) ytdlpCalls() []string {
	data, _ := os.ReadFile(filepath.Join(e.binDir, "calls.log"))
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && !strings.Contains(line, "--dump-single-json") {
			out = append(out, line)
		}
	}
	return out
}

// downloads counts how many times yt-dlp actually downloaded media
// (metadata-only calls excluded).
func (e *env) downloads() int {
	data, _ := os.ReadFile(filepath.Join(e.binDir, "calls.log"))
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && !strings.Contains(line, "--dump-single-json") {
			n++
		}
	}
	return n
}

var updateID int64

// send delivers a text message and returns its message ID.
func (e *env) send(chat models.Chat, userID int64, text string) int {
	return e.sendReply(chat, userID, text, nil)
}

// sendReply delivers a message answering replyTo (nil = not a reply).
func (e *env) sendReply(chat models.Chat, userID int64, text string, replyTo *models.Message) int {
	updateID++
	msg := &models.Message{
		ID:             int(updateID),
		From:           &models.User{ID: userID, FirstName: "U"},
		Chat:           chat,
		Text:           text,
		ReplyToMessage: replyTo,
	}
	e.b.ProcessUpdate(context.Background(), &models.Update{ID: updateID, Message: msg})
	return msg.ID
}

func (e *env) press(chat models.Chat, userID int64, data string) {
	updateID++
	e.b.ProcessUpdate(context.Background(), &models.Update{
		ID: updateID,
		CallbackQuery: &models.CallbackQuery{
			ID:      fmt.Sprint(updateID),
			From:    models.User{ID: userID},
			Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 1, Chat: chat}},
			Data:    data,
		},
	})
}

// waitJob waits until the user's newest job reaches a final status.
func (e *env) waitJob(userID int64) db.Job {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := db.GetUserJobs(context.Background(), e.pool, userID, 1)
		if err != nil {
			e.t.Fatal(err)
		}
		if len(jobs) == 1 {
			switch jobs[0].Status {
			case db.StatusDone, db.StatusFailed, db.StatusCanceled:
				return jobs[0]
			}
		}
		select {
		case <-e.api.Changed():
		case <-time.After(100 * time.Millisecond):
		}
	}
	e.t.Fatal("job did not finish in time")
	return db.Job{}
}

// upload returns the single upload of the given method sent to chatID.
func (e *env) upload(method string, chatID int64) tgtest.Call {
	e.t.Helper()
	var found []tgtest.Call
	for _, c := range e.api.CallsTo(method) {
		if c.Fields["chat_id"] == fmt.Sprint(chatID) {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		e.t.Fatalf("%s calls to %d = %d, want 1", method, chatID, len(found))
	}
	return found[0]
}

func private(id int64) models.Chat { return models.Chat{ID: id, Type: models.ChatTypePrivate} }

// A Russian-speaking user picks a language on first contact and gets their
// video back with a Russian caption and status.
func TestE2EVideoInRussian(t *testing.T) {
	e := newEnv(t)
	const user = 101

	e.send(private(user), user, "/start")
	if !e.api.AnyText(user, "Выберите язык") {
		t.Fatalf("no language picker: %v", e.api.Texts(user))
	}
	e.press(private(user), user, "lang:ru:w")

	e.send(private(user), user, videoURL)
	job := e.waitJob(user)
	if job.Status != db.StatusDone {
		t.Fatalf("job status = %s (%s)", job.Status, job.ErrorMessage)
	}

	up := e.upload("sendVideo", user)
	video, ok := up.Files["video"]
	if !ok {
		t.Fatalf("sendVideo has no video file: %+v", up.Fields)
	}
	if string(video.Data) != "FAKE-MEDIA-mp4" || video.Name != "E2E Видео [vid42].mp4" {
		t.Fatalf("uploaded %q (%q)", video.Name, video.Data)
	}
	caption := up.Fields["caption"]
	for _, want := range []string{"E2E Видео", "Длительность: 1:05", "Источник: youtube"} {
		if !strings.Contains(caption, want) {
			t.Errorf("caption %q missing %q", caption, want)
		}
	}
	if up.Fields["duration"] != "65" {
		t.Errorf("duration = %q", up.Fields["duration"])
	}

	final := e.api.LastText(user)
	if !strings.Contains(final, "Задача #") || !strings.Contains(final, "готово") {
		t.Fatalf("final status not in Russian: %q", final)
	}
	if !e.api.AnyText(user, "в очереди") {
		t.Fatal("queue ack not in Russian")
	}

	// The job is marked done just before the worker removes its directory.
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, _ := os.ReadDir(e.workDir)
		if len(entries) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("work dir not cleaned up: %v", entries)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// /mp3 in the default language goes through audio extraction and sendAudio.
func TestE2EAudioInEnglish(t *testing.T) {
	e := newEnv(t)
	const user = 102
	e.send(private(user), user, "/mp3 "+videoURL)
	job := e.waitJob(user)
	if job.Status != db.StatusDone || job.Mode != db.ModeAudio {
		t.Fatalf("job = %+v", job)
	}
	up := e.upload("sendAudio", user)
	if a := up.Files["audio"]; string(a.Data) != "FAKE-MEDIA-mp3" {
		t.Fatalf("audio upload = %q", a.Data)
	}
	if !strings.Contains(up.Fields["caption"], "Duration: 1:05") {
		t.Fatalf("caption = %q", up.Fields["caption"])
	}
	if !strings.Contains(e.api.LastText(user), "Status: done") {
		t.Fatalf("final = %q", e.api.LastText(user))
	}
}

// A group set to Hindi by its admin gets Hindi status messages, including a
// permanent failure, which is not retried.
func TestE2EGroupFailureInHindi(t *testing.T) {
	e := newEnv(t)
	const g, admin, member = -1001, 201, 202
	group := models.Chat{ID: g, Type: models.ChatTypeSupergroup}

	e.api.SetChatAdmin(g, admin)
	e.press(group, member, "lang:hi:s") // not an admin: ignored
	e.press(group, admin, "lang:hi:s")
	if code, _, _ := db.GetChatLanguage(context.Background(), e.pool, g); code != "hi" {
		t.Fatalf("group language = %q", code)
	}

	e.send(group, member, "https://1.1.1.1/private-video")
	job := e.waitJob(member)
	if job.Status != db.StatusFailed || job.RetryCount != 0 {
		t.Fatalf("job = %s retries=%d (%s)", job.Status, job.RetryCount, job.ErrorMessage)
	}
	final := e.api.LastText(g)
	for _, want := range []string{"जॉब #", "स्थिति: विफल", "Private video"} {
		if !strings.Contains(final, want) {
			t.Errorf("final %q missing %q", final, want)
		}
	}
	if len(e.api.CallsTo("sendVideo")) != 0 {
		t.Fatal("failed job must not upload anything")
	}
}

// Changing the language later from /settings applies to the next download.
func TestE2ELanguageChangeAppliesToNextDownload(t *testing.T) {
	e := newEnv(t)
	const user = 103
	e.send(private(user), user, "/settings")
	e.press(private(user), user, "set:lang")
	e.press(private(user), user, "lang:fa:s")

	e.send(private(user), user, videoURL)
	if job := e.waitJob(user); job.Status != db.StatusDone {
		t.Fatalf("job = %+v", job)
	}
	if !strings.Contains(e.upload("sendVideo", user).Fields["caption"], "منبع: youtube") {
		t.Fatal("caption not in Persian")
	}
	if !strings.Contains(e.api.LastText(user), "انجام شد") {
		t.Fatalf("final = %q", e.api.LastText(user))
	}
}
