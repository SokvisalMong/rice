package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const defaultRegion = "us"

// DB persists per-user preferences in SQLite.
type DB struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("database path is empty")
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	database.SetMaxOpenConns(1)

	cleanup := func(cause error) (*DB, error) {
		_ = database.Close()
		return nil, cause
	}
	if err := database.PingContext(ctx); err != nil {
		return cleanup(fmt.Errorf("connect to SQLite database: %w", err))
	}
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		`CREATE TABLE IF NOT EXISTS user_preferences (
			user_id TEXT PRIMARY KEY,
			region TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		) WITHOUT ROWID`,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return cleanup(fmt.Errorf("initialize SQLite database: %w", err))
		}
	}
	return &DB{db: database}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

// Region returns a user's saved region or the US default when none is stored.
func (d *DB) Region(ctx context.Context, userID string) (string, error) {
	var region string
	err := d.db.QueryRowContext(ctx, "SELECT region FROM user_preferences WHERE user_id = ?", userID).Scan(&region)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultRegion, nil
	}
	if err != nil {
		return "", fmt.Errorf("read user region: %w", err)
	}
	return region, nil
}

// SetRegion saves a user's preferred GG.deals region.
func (d *DB) SetRegion(ctx context.Context, userID, region string) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("user ID is empty")
	}
	if strings.TrimSpace(region) == "" {
		return errors.New("region is empty")
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, region, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			region = excluded.region,
			updated_at = excluded.updated_at
	`, userID, region, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("save user region: %w", err)
	}
	return nil
}
