package migrate_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/dbtest"
	"github.com/meanii/downly/internal/migrate"
)

// legacySchema is what EnsureSchema/EnsurePreferencesTable produced before
// all schema moved into migrations, with 000001 already recorded.
const legacySchema = `
create table schema_migrations (version text primary key, applied_at timestamptz not null default now());
insert into schema_migrations(version) values ('000001_init');
create table download_jobs (
	id bigserial primary key, chat_id bigint not null, user_id bigint not null default 0, url text not null,
	platform text not null default '', status text not null, priority integer not null default 0,
	output_path text not null default '', output_name text not null default '', error_message text not null default '',
	retry_count integer not null default 0, telegram_message_id bigint not null default 0,
	progress_text text not null default '', progress_percent integer not null default 0,
	file_size_bytes bigint not null default 0, created_at timestamptz not null default now(),
	started_at timestamptz, finished_at timestamptz
);
create table banned_users (user_id bigint primary key, banned_at timestamptz not null default now(), reason text not null default '');
create table user_preferences (user_id bigint primary key, quality text not null default 'best', updated_at timestamptz not null default now());
insert into download_jobs (chat_id, user_id, url, status) values
	(11, 11, 'https://a.com/1', 'done'),
	(11, 11, 'audio:https://a.com/2', 'done'),
	(-100, 11, 'q720:https://a.com/3', 'pending'),
	(22, 22, 'telegram:https://a.com/4', 'failed');
`

func TestUpgradeFromLegacySchema(t *testing.T) {
	dsn := dbtest.NewDatabase(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, legacySchema); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}

	if err := migrate.Up(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := pool.Query(ctx, `select url, mode, quality from download_jobs order by id`)
	if err != nil {
		t.Fatal(err)
	}
	type r struct{ url, mode, quality string }
	var got []r
	for rows.Next() {
		var x r
		if err := rows.Scan(&x.url, &x.mode, &x.quality); err != nil {
			t.Fatal(err)
		}
		got = append(got, x)
	}
	want := []r{
		{"https://a.com/1", "video", ""},
		{"https://a.com/2", "audio", ""},
		{"https://a.com/3", "video", "q720"},
		{"https://a.com/4", "video", "telegram"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Users are backfilled, preferring the private chat (chat_id = user_id).
	var chat11 int64
	if err := pool.QueryRow(ctx, `select chat_id from users where user_id = 11`).Scan(&chat11); err != nil {
		t.Fatal(err)
	}
	if chat11 != 11 {
		t.Errorf("user 11 chat_id = %d, want 11 (private chat preferred over group)", chat11)
	}
	var n int
	_ = pool.QueryRow(ctx, `select count(*) from users`).Scan(&n)
	if n != 2 {
		t.Errorf("users = %d, want 2", n)
	}

	// The status constraint now rejects garbage.
	if _, err := pool.Exec(ctx, `insert into download_jobs (chat_id, url, status) values (1, 'x', 'bogus')`); err == nil {
		t.Error("status check constraint missing")
	}
}

func TestUpIsIdempotentAndConcurrencySafe(t *testing.T) {
	dsn := dbtest.NewDatabase(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- migrate.Up(dsn)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Up: %v", err)
		}
	}
	if err := migrate.Up(dsn); err != nil {
		t.Fatalf("second Up: %v", err)
	}
}
