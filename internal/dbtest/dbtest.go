// Package dbtest gives integration tests a throwaway Postgres database.
//
// Tests using it are skipped unless DOWNLY_TEST_DATABASE_URL points at a
// server where the user may create databases, e.g.
//
//	docker run -d -p 55432:5432 -e POSTGRES_USER=downly -e POSTGRES_PASSWORD=downly postgres:16-alpine
//	DOWNLY_TEST_DATABASE_URL=postgres://downly:downly@localhost:55432/downly go test ./...
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/migrate"
)

const EnvVar = "DOWNLY_TEST_DATABASE_URL"

var counter atomic.Int64

// NewDatabase creates an empty database and returns its URL. It is dropped
// when the test ends.
func NewDatabase(t testing.TB) string {
	t.Helper()
	admin := os.Getenv(EnvVar)
	if admin == "" {
		t.Skipf("%s not set; skipping Postgres integration test", EnvVar)
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin db: %v", err)
	}
	name := fmt.Sprintf("downly_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano()%1e9, counter.Add(1))
	if _, err := adminPool.Exec(ctx, "create database "+name); err != nil {
		adminPool.Close()
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "drop database if exists "+name+" with (force)")
		adminPool.Close()
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvVar, err)
	}
	u.Path = "/" + name
	return u.String()
}

// NewPool creates a fresh, fully migrated database and returns a pool to it.
func NewPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := NewDatabase(t)
	if err := migrate.Up(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
