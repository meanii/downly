# downly 🦉

A Telegram bot that downloads media links and sends the result back to Telegram.

This branch contains a clean Go rewrite path focused on:
- minimal structure
- Postgres-backed job queue
- yt-dlp as the downloader backend
- async worker processing

## Current rewrite direction
The rewrite runs as a Go app under:
- `cmd/downly/`
- `internal/config`
- `internal/db`
- `internal/downloader`
- `internal/telegram`
- `internal/worker`

The older Python + RabbitMQ path is still present in the repo, but this branch introduces an isolated Go path so the rewrite can move forward without mixing concerns.

## How it works
1. Telegram bot receives a supported URL
2. A job is inserted into Postgres
3. Worker claims jobs using `FOR UPDATE SKIP LOCKED`
4. Worker runs `yt-dlp`
5. Result is sent back to Telegram

## Supported services
The rewrite uses `yt-dlp` as the backend, so it supports many services that `yt-dlp` supports, including:
- Instagram
- YouTube
- YouTube Shorts
- and many other yt-dlp-supported platforms

This should be treated as broad support, not an absolute guarantee, because upstream service support can change.

## Config
Use `sample-config.yaml` as the starting point.

Important fields:
- `downly.telegram.bot_token`
- `downly.database.postgres_url`
- `downly.worker.numbers_of_workers`
- `downly.worker.poll_interval_sec`
- `downly.worker.work_dir`
- `downly.worker.max_file_size_mb`
- `downly.services.ytdl.bin`

## Build
From the repo root:

```bash
go mod tidy
go build -o .build/downly ./cmd/downly
```

## Notes
- No auto-merge to `main`
- This branch is intentionally isolated and cleaner than the older worker path
- Next improvements can include `/status`, retries with backoff, and better media handling rules
