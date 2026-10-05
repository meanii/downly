# downly

A Telegram bot that downloads media from URLs and sends files back to the chat.

Built in Go with Postgres-backed job queue and yt-dlp as the download backend.

## Project structure

```
cmd/downly/          entrypoint
internal/
  config/            YAML config loader (+ env overrides)
  db/                Postgres queries (jobs, users, stats, cache, subscriptions, payments)
  downloader/        yt-dlp wrapper, clips/GIFs, ffmpeg compression
  telegram/          Telegram bot handlers
  worker/            job processing, heartbeats, retries, albums
  subscriptions/     /follow poller
  media/             shared media types and cache keys
  i18n/              translations (en, ru, hi, fa)
  reaper/            recovers jobs from dead workers
  cleanup/           prunes old jobs (into daily stats) and stale cache entries
  updater/           yt-dlp auto-updater
  migrate/           embedded SQL migrations
  health/            /health and /metrics endpoints
  safeurl/           URL validation and SSRF-safe HTTP client
  tgutil/            Telegram rate-limit helpers
  tgtest/            fake Bot API server for tests
  e2e/               end-to-end tests
  logging/           structured logger setup
```

## How it works

1. A user sends a link (or uses `/dl`, `/mp3`, `/clip`, `/gif`, inline mode, or a `/follow` subscription produces one)
2. If the same content was sent before, it is re-sent instantly from Telegram's copy (media cache)
3. Otherwise the URL is validated and a job is queued in Postgres within the user's limits
4. Workers are woken via Postgres `LISTEN/NOTIFY`, claim jobs with `FOR UPDATE SKIP LOCKED`, send heartbeats and edit throttled progress into a status message
5. The result is compressed to fit if needed and sent back: a single file, or an album for multi-photo/video posts

Reliability:
- **Failures** retry with exponential backoff (30s, 2m, 8m, ...). Errors that will never succeed (private video, unsupported URL, too large) fail immediately.
- **Dead workers:** if a worker dies, the reaper requeues its jobs once their heartbeat is older than `stuck_job_minutes`.
- **Shutdown:** on SIGTERM the bot stops taking new jobs and gives running ones `shutdown_grace_seconds` to finish. Anything still running after that is requeued.

## Commands

User commands:
- `/start`, `/help` - show usage
- `/dl <url>` - download a link; in groups you can also reply `/dl` to a message with a link
- `/mp3 <url>` - audio only, with title/artist tags and cover art
- `/clip <url> <start-end>` - download only part of a video (e.g. `1:20-2:05`); sending `<url> 1:20-2:05` works too
- `/gif <url> [start-end]` - turn up to 30 seconds of a video into a GIF
- `/quality <url>` - choose the quality for one download
- `/setquality` - set your default quality
- `/playlist <url> [max]` - queue a playlist (up to 25)
- `/follow <channel or playlist> [audio]` - get new uploads automatically; `/following` lists them, `/unfollow <n>` stops
- `/queue` - your active jobs and queue positions
- `/history` - your past downloads, with "Send again" buttons
- `/cancel <job_id>` - cancel a pending or running job
- `/settings` - language, default quality and (in groups) how links are handled
- `/language` - change the language
- `/premium`, `/paysupport` - when Premium is enabled

You can also prefix a URL with `q360:`, `q480:`, `q720:`, `q1080:` or `audio:`.

Admin commands (English only):
- `/stats` - totals, success rate, cache hits, 7-day active/new/returning users, Premium revenue
- `/health` - per-platform success rates (last 24h)
- `/bandwidth [limit]` - per-user bandwidth report
- `/users` - recently active users
- `/jobs` - active and pending jobs
- `/promote <job_id>` - raise job priority
- `/demote <job_id>` - reset job priority
- `/broadcast <message>` - message all users (throttled, runs in the background)
- `/ban <user_id> [reason]` - block a user
- `/unban <user_id>` - unblock a user
- `/refund <charge_id>` - refund a Telegram Stars payment

## Features

### Media cache
Every finished upload's Telegram file ID is stored. The same content requested again is re-sent instantly, without downloading or uploading. This works even when the link differs, e.g. `youtu.be/x` vs `youtube.com/watch?v=x&si=...`.

Cached deliveries don't count toward the daily quota. File IDs Telegram rejects are dropped, and the content is downloaded again. Entries unused for `cache.retention_days` (default 60) are pruned.

### Albums
Carousels and multi-photo posts on album sites are sent as Telegram albums, up to 10 items. The default album sites are Instagram, X/Twitter, Threads, TikTok, Reddit, Facebook and Bluesky (`services.ytdl.album_hosts`).

Other sites keep single-video behaviour, so a YouTube `watch?v=...&list=...` link still downloads one video.

### Groups
Add the bot to a group. By default it downloads every link posted. A group admin can switch to "only with /dl" in `/settings`.

In groups:
- the bot replies to the message with the link
- it skips percentage progress edits
- it deletes its status message once the media is posted
- it ignores links that aren't media instead of posting errors

To see ordinary messages in auto mode, the bot needs privacy mode off (@BotFather → `/setprivacy` → Disable) or admin rights.

### Inline mode
Type `@yourbot <url>` in any chat:
- **Already cached:** the media itself is shared instantly.
- **Not yet downloaded:** a placeholder is posted and replaced with the video when it's ready. The file is also sent to the user's private chat, so they must have started the bot.

This needs inline mode and inline feedback (`/setinline`, `/setinlinefeedback`) enabled in @BotFather.

### Subscriptions
`/follow` checks a channel or playlist every `subscriptions.interval_minutes` (default 60) and sends new uploads:
- **Backlog:** existing uploads are skipped, and at most 5 new ones are sent per check.
- **YouTube channel links** use their `/videos` tab.
- **Limits:** a chat can follow up to `subscriptions.max_per_chat` (default 5) feeds. In groups, only admins can manage subscriptions.

### Premium (Telegram Stars)
Off by default. Set `premium.enabled: true` to sell `premium.days` (default 30) of Premium for `premium.price_stars` (default 100) Stars.

Premium users get:
- no daily quota (or `premium.daily_quota`)
- a bigger queue (`premium.max_queued`, default 20)
- queue priority

Purchases stack. Payments are recorded idempotently, and `/refund` takes the days back. Telegram requires bots that sell for Stars to support `/paysupport`.

## Languages

The bot speaks 🇬🇧 English, 🇷🇺 Russian, 🇮🇳 Hindi and 🇮🇷 Persian.

- **First contact:** the first `/start` in a private chat shows a flag picker. When the bot is added to a group, it posts the picker there. The choice is saved per chat.
- **Changing it later:** use `/settings` → 🌐 Language, or `/language`. In groups, only group admins (and bot admins) can change it.
- **Which language is used:** the chat's choice, then (in a group that hasn't chosen) the member's own choice, then their Telegram app language, then English.
- **Command menu:** the "/" menu is localized too.

Translations live in `internal/i18n/catalog.go`. A test fails if any language is missing a message or uses different `%` placeholders from English.

## Config

Copy `sample-config.yaml` to `config.yaml` and fill in your values.

Key fields:
- `downly.telegram.bot_token` - Telegram bot token
- `downly.database.postgres_url` - Postgres connection string
- `downly.worker.numbers_of_workers` - concurrent download workers
- `downly.worker.max_file_size_mb` - max file size for Telegram upload (50 for the public Bot API)
- `downly.worker.max_download_size_mb` - larger sources are fetched and compressed to fit (default 4x the upload limit)
- `downly.worker.job_timeout_minutes` - max time for one download attempt
- `downly.worker.stuck_job_minutes` - requeue jobs whose worker stopped sending heartbeats for this long
- `downly.worker.shutdown_grace_seconds` - time running jobs get to finish on shutdown
- `downly.limits.max_queued_per_user` / `max_concurrent_per_user` / `daily_quota_per_user`
- `downly.limits.rate_limit_seconds` - cooldown between URL submissions per user
- `downly.limits.max_retries` - auto-retry count for failed downloads
- `downly.services.ytdl.auto_update_hours` - yt-dlp self-update interval
- `downly.admin.user_ids` - Telegram user IDs with admin access
- `downly.cache.disabled` / `retention_days` - media cache
- `downly.services.ytdl.album_hosts` - sites whose multi-media posts become albums
- `downly.subscriptions.disabled` / `interval_minutes` / `max_per_chat` - `/follow`
- `downly.premium.enabled` / `price_stars` / `days` / `daily_quota` / `max_queued` - Premium

Secrets can come from the environment instead of the file. These variables override it:

| Variable | Overrides |
|---|---|
| `DOWNLY_BOT_TOKEN` | `downly.telegram.bot_token` |
| `DOWNLY_POSTGRES_URL` | `downly.database.postgres_url` |
| `DOWNLY_ADMIN_IDS` | `downly.admin.user_ids` (comma-separated) |

If `DOWNLY_BOT_TOKEN` is set, `config.yaml` is optional.

## Health and metrics

The health port (`worker.health_port`, default 8080) serves two endpoints.

`/health` returns a JSON status and HTTP 503 when any of these is true:
- the database is unreachable
- Telegram has not answered `getMe` for 5 minutes
- a worker has stopped checking in

`/metrics` uses the Prometheus text format:
- `downly_jobs{status}` - pending and processing jobs
- `downly_jobs_finished_total{result}` - finished jobs by result (`done`, `failed`, `retried`, `canceled`, `requeued`)
- `downly_upload_failures_total` - failed uploads to Telegram
- `downly_telegram_rate_limited_total` - 429 responses from Telegram
- `downly_telegram_up` - 1 if Telegram is reachable
- `downly_worker_last_seen_seconds{worker}` - seconds since each worker last checked in

## Build

```bash
go build -o .build/downly ./cmd/downly
```

## Test

Unit tests need nothing extra:

```bash
go test -race ./...
```

Postgres integration tests (DB, worker lifecycle, bot handlers) run when `DOWNLY_TEST_DATABASE_URL` is set. Each test creates and drops its own database:

```bash
docker run -d --name downly-test-pg -p 55432:5432 \
  -e POSTGRES_USER=downly -e POSTGRES_PASSWORD=downly postgres:16-alpine
DOWNLY_TEST_DATABASE_URL=postgres://downly:downly@localhost:55432/downly go test -race ./...
```

The compression test runs when `ffmpeg` is installed.

End-to-end tests (`internal/e2e`) run the real handlers, queue, worker and downloader together:
- **Inputs:** Telegram updates go in through the real handlers.
- **Fakes:** a fake Bot API server (`internal/tgtest`) stands in for Telegram, and a scripted stand-in replaces `yt-dlp`.
- **Checks:** the uploaded file, its caption and the status messages in each language.

They use the same `DOWNLY_TEST_DATABASE_URL` and need `bash`.

## Docker

```bash
docker compose up --build
```

Development:

```bash
docker compose -f compose.development.yaml up --build
```

The image includes yt-dlp, ffmpeg and [Deno](https://github.com/yt-dlp/yt-dlp/wiki/EJS), which yt-dlp needs for YouTube.

It runs as uid 10001, so `config.yaml` must be readable by that user (e.g. `chmod 644 config.yaml`), or you can pass secrets through the environment variables above.

## Larger files

The public Bot API caps uploads at 50MB. To send files up to 2GB:

1. Run a [local Bot API server](https://github.com/tdlib/telegram-bot-api).
2. Point `downly.telegram.api_url` at it, e.g. `http://telegram-bot-api:8081`.
3. Raise `max_file_size_mb`.

## Instagram cookies

Export cookies to `cookies/instagram.txt` and set in config:

```yaml
downly:
  services:
    ytdl:
      cookies_file: "/app/cookies/instagram.txt"
```

A template is at `cookies/instagram.txt.sample`.
