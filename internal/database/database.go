// Package database is Lumberjack's data-access layer. It opens the SQLite
// database (pure-Go, via modernc.org/sqlite), applies the embedded goose
// migrations in-process, and exposes a Bun client for queries.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver

	"github.com/ceilingfish/lumberjack/internal/database/migrations"
)

// EnvDBPath is the environment variable that overrides the database location.
const EnvDBPath = "LUMBERJACK_DB_PATH"

// driverName is modernc.org/sqlite's registered database/sql driver.
const driverName = "sqlite"

// Client wraps a Bun DB handle bound to Lumberjack's SQLite database.
type Client struct {
	*bun.DB
}

// DefaultPath returns the database path, honouring LUMBERJACK_DB_PATH and
// falling back to ~/.lumberjack/db.sqlite.
func DefaultPath() (string, error) {
	if p := os.Getenv(EnvDBPath); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".lumberjack", "db.sqlite"), nil
}

// Open opens (creating if necessary) the database at path, applies all
// pending migrations, and returns a ready-to-use Client. The parent directory
// is created if it does not exist. Callers must Close the returned Client.
func Open(ctx context.Context, path string) (*Client, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	// Foreign keys are off by default in SQLite; enable per connection.
	// busy_timeout avoids spurious "database is locked" errors under the daemon.
	// The path is URL-escaped so a path containing '?', '#', or a space can't
	// corrupt the DSN's query string.
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	}).String()

	sqldb, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// The daemon is the sole owner of the database, but database/sql still
	// pools connections. Cap at one so two queries can never contend for
	// SQLite's single-writer lock and deadlock past busy_timeout.
	sqldb.SetMaxOpenConns(1)

	if err := migrate(ctx, sqldb); err != nil {
		_ = sqldb.Close()
		return nil, err
	}

	return &Client{DB: bun.NewDB(sqldb, sqlitedialect.New())}, nil
}

// migrate applies every embedded goose migration to db.
//
// goose configuration is process-global. This is safe because the daemon owns
// the database and calls Open exactly once, at startup; if Open ever becomes
// concurrent, these setters must move behind a sync.Once.
func migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("setting goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}
