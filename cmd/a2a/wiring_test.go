package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A missing DATABASE_URL is a boot error, never a silent in-memory fallback,
// both from run() (before telemetry and the listener) and from buildDeps.
func TestA2ARefusesToBootWithoutDatabaseURL(t *testing.T) {
	t.Run("run", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		t.Setenv("A2A_AGENT_URL", testAgentURL)
		if err := run(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Fatalf("run() without DATABASE_URL = %v, want a boot error naming DATABASE_URL", err)
		}
	})
	t.Run("buildDeps", func(t *testing.T) {
		_, _, err := buildDeps(context.Background(), quietLogger(), "", "")
		if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Fatalf("buildDeps(\"\") = %v, want an error naming DATABASE_URL", err)
		}
	})
}

// buildDeps runs the boot migrations against migrationsDatabaseURL and opens
// the pool on databaseURL, never the two conflated. The migrations URL is a
// schemeless string migrate rejects with a distinctive parse error; the app URL
// is an address nothing listens on. The retries are cut short by a timeout (not
// a cancelled context, which would discard the real cause) and the cause is
// read from the logged "retrying ... err=" line.
func TestBuildDeps_UsesMigrationsDatabaseURLForMigrations(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _, err := buildDeps(ctx, logger, "postgres://u@127.0.0.1:1/db?sslmode=disable&connect_timeout=1", "not-a-valid-connection-string")
	if err == nil {
		t.Fatal("a malformed MIGRATIONS_DATABASE_URL must fail boot")
	}
	line := logs.String()
	if !strings.Contains(line, "retrying") || !strings.Contains(line, "parse scheme") {
		t.Fatalf("log = %q: want the migrate parse error of the migrations URL; a dial/connection-refused error would mean migrations ran against DATABASE_URL", line)
	}
	if strings.Contains(line, "connection refused") {
		t.Fatalf("migrations were attempted against DATABASE_URL: %q", line)
	}
}

// The MIGRATIONS_DATABASE_URL fallback run() applies: unset means DATABASE_URL.
func TestMigrationsDatabaseURLFallback(t *testing.T) {
	const databaseURL = "postgres://u@pgbouncer.example:6432/checkout"
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	if got := getenv("MIGRATIONS_DATABASE_URL", databaseURL); got != databaseURL {
		t.Fatalf("fallback = %q, want %q", got, databaseURL)
	}
	const direct = "postgres://u@postgres.example:5432/checkout"
	t.Setenv("MIGRATIONS_DATABASE_URL", direct)
	if got := getenv("MIGRATIONS_DATABASE_URL", databaseURL); got != direct {
		t.Fatalf("set = %q, want the direct URL %q", got, direct)
	}
}
