# downly

A Telegram bot that downloads media from URLs and sends files back to the chat.

Built in Go with Postgres-backed job queue and yt-dlp as the download backend.

## Project structure

```
cmd/downly/          entrypoint
internal/
  config/            YAML config loader
  db/                Postgres queries and schema
  downloader/        yt-dlp wrapper
  telegram/          Telegram bot handlers
  worker/            job processing loop
  cleanup/           old job cleanup loop
  updater/           yt-dlp auto-updater
  migrate/           embedded SQL migrations
  logging/           structured logger setup
```

## How it works

1. User sends a URL to the Telegram bot
2. Job is inserted into Postgres with queue position
3. Worker claims jobs using `FOR UPDATE SKIP LOCKED`
4. Progress updates are edited into the original Telegram message
5. Completed file is sent back to the user

## Commands

User commands:
- `/start`, `/help` - show usage
- `/queue` - show your active jobs and queue positions
- `/history` - show your past downloads
- `/cancel <job_id>` - cancel a pending or running job

Admin commands:
- `/stats` - bot analytics
- `/promote <job_id>` - raise job priority
- `/demote <job_id>` - reset job priority
- `/broadcast <message>` - message all users
- `/ban <user_id> [reason]` - block a user
- `/unban <user_id>` - unblock a user

## Config

Copy `sample-config.yaml` to `config.yaml` and fill in your values.

Key fields:
- `downly.telegram.bot_token` - Telegram bot token
- `downly.database.postgres_url` - Postgres connection string
- `downly.worker.numbers_of_workers` - concurrent download workers
- `downly.worker.max_file_size_mb` - max file size for Telegram upload
- `downly.limits.rate_limit_seconds` - cooldown between URL submissions per user
- `downly.limits.max_retries` - auto-retry count for failed downloads
- `downly.services.ytdl.auto_update_hours` - yt-dlp self-update interval
- `downly.admin.user_ids` - Telegram user IDs with admin access

## Build

```bash
go build -o .build/downly ./cmd/downly
```

## Docker

```bash
docker compose up --build
```

Development:

```bash
docker compose -f compose.development.yaml up --build
```

## Instagram cookies

Export cookies to `cookies/instagram.txt` and set in config:

```yaml
downly:
  services:
    ytdl:
      cookies_file: "/app/cookies/instagram.txt"
```

A template is at `cookies/instagram.txt.sample`.
