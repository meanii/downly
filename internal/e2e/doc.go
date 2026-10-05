// Package e2e holds end-to-end tests that run the real Telegram handlers,
// job queue, worker and downloader together against Postgres, a fake Bot
// API server and a scripted stand-in for yt-dlp.
//
// They need DOWNLY_TEST_DATABASE_URL (see internal/dbtest) and bash.
package e2e
