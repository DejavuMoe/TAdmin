package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const busyTimeoutMilliseconds = 5000

type Store struct {
	db   *sql.DB
	lock *os.File
}

func Open(ctx context.Context, databasePath string) (*Store, error) {
	if err := ensureParentDirectory(databasePath); err != nil {
		return nil, err
	}

	lock, err := os.OpenFile(databasePath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open database lock: %w", err)
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("database is already in use by another instance: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			lock.Close()
		}
	}()
	file, err := os.OpenFile(databasePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	file.Close()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}

	// A single connection makes connection-scoped PRAGMAs deterministic. This is
	// sufficient for the expected 1-5 groups and keeps the first release simple.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db, lock: lock}
	if err := s.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	failed = false
	return s, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to SQLite database: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMilliseconds),
	}
	for _, statement := range pragmas {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply SQLite setting: %w", err)
		}
	}

	if err := s.Migrate(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping SQLite database: %w", err)
	}
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("query SQLite database: %w", err)
	}
	return nil
}

func (s *Store) CountManagedChats(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM managed_chats WHERE enabled = 1").Scan(&count); err != nil {
		return 0, fmt.Errorf("count managed chats: %w", err)
	}
	return count, nil
}

func (s *Store) Close() error {
	err := s.db.Close()
	if s.lock != nil {
		err = errors.Join(err, s.lock.Close())
		s.lock = nil
	}
	return err
}

func ensureParentDirectory(databasePath string) error {
	if databasePath == "" {
		return fmt.Errorf("database path must not be empty")
	}
	parent := filepath.Dir(databasePath)
	if parent == "." {
		return nil
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	return nil
}
