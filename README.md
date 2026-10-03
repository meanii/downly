# downly

A Telegram bot that downloads media from URLs and sends files back to the chat.

Built in Go with Postgres-backed job queue and yt-dlp as the download backend.

## Project structure

```
cmd/downly/          entrypoint
internal/
  config/            YAML config loader (+ env overrides)
  db/                Postgres queries (jobs, users, stats, limits)
  downloader/        yt-dlp wrapper and ffmpeg compression
  telegram/          Telegram bot handlers
  worker/            job processing, heartbeats, retries
  reaper/            recovers jobs from dead workers
  cleanup/           prunes old jobs into daily stats
  updater/           yt-dlp auto-updater
  migrate/           embedded SQL migrations
  health/            /health and /metrics endpoints
  safeurl/           URL validation and SSRF-safe HTTP client
  tgutil/            Telegram rate-limit helpers
  logging/           structured logger setup
```

## How it works

1. User sends a URL to the Telegram bot
2. The URL is validated and the job is inserted into Postgres, within the user's queue and daily limits
3. Workers are woken via Postgres `LISTEN/NOTIFY` and claim jobs with `FOR UPDATE SKIP LOCKED`
4. While a job runs, the worker sends heartbeats and edits throttled progress into the original message
5. The file is compressed to fit the upload limit if needed and sent back to the user

Reliability:
- **Failures** retry with exponential backoff (30s, 2m, 8m, ...). Errors that will never succeed (private video, unsupported URL, too large) fail immediately.
- **Dead workers:** if a worker dies, the reaper requeues its jobs once their heartbeat is older than `stuck_job_minutes`.
- **Shutdown:** on SIGTERM the bot stops taking new jobs and gives running ones `shutdown_grace_seconds` to finish. Anything still running after that is requeued.

## Commands

User commands:
- `/start`, `/help` - show usage
- `/queue` - show your active jobs and queue positions
- `/history` - show your past downloads
- `/mp3 <url>` - extract audio only
- `/quality <url>` - choose the quality for one download
- `/setquality` - set your default quality
- `/playlist <url> [max]` - queue a playlist (up to 25)
- `/cancel <job_id>` - cancel a pending or running job

You can also prefix a URL with `q360:`, `q480:`, `q720:`, `q1080:` or `audio:`.

- `/settings` - language and default quality
- `/language` - change the language

## Languages

The bot speaks 🇬🇧 English, 🇷🇺 Russian, 🇮🇳 Hindi and 🇮🇷 Persian.

- **First contact:** the first `/start` in a private chat shows a flag picker. When the bot is added to a group, it posts the picker there. The choice is saved per chat.
- **Changing it later:** use `/settings` → 🌐 Language, or `/language`. In groups, only group admins (and bot admins) can change it.
- **Which language is used:** the chat's choice, then (in a group that hasn't chosen) the member's own choice, then their Telegram app language, then English.
- **Command menu:** the "/" menu is localized too.
- **Admin commands:** these reply in English.

Translations live in `internal/i18n/catalog.go`. A test fails if any language is missing a message or uses different `%` placeholders from English.

Admin commands:
- `/stats` - bot analytics
- `/health` - per-platform success rates (last 24h)
- `/bandwidth [limit]` - per-user bandwidth report
- `/users` - recently active users
- `/jobs` - active and pending jobs
- `/promote <job_id>` - raise job priority
- `/demote <job_id>` - reset job priority
- `/broadcast <message>` - message all users (throttled, runs in the background)
- `/ban <user_id> [reason]` - block a user
- `/unban <user_id>` - unblock a user

Inline mode (`@yourbot <url>` in any chat) needs inline mode and inline feedback (`/setinline`, `/setinlinefeedback`) enabled in @BotFather. The file goes to the user's private chat, so they must have started the bot first.

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
