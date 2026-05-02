package db

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/internal/config"
	_ "github.com/mattn/go-sqlite3"
)

// NewClient opens the SQLite database, enables required pragmas, and runs migrations.
func NewClient(cfg *config.Config) (*ent.Client, error) {
	dbDir := filepath.Dir(cfg.Database.Path)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Enable WAL mode and foreign keys via connection string and pragmas
	// _fk=1 enables foreign keys at the driver level
	// _journal_mode=WAL enables Write-Ahead Logging
	// _busy_timeout=5000 sets the busy timeout in milliseconds
	dsn := fmt.Sprintf("file:%s?_fk=1&_journal_mode=WAL&_busy_timeout=5000", cfg.Database.Path)

	client, err := ent.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed opening connection to sqlite: %w", err)
	}

	// Run the auto migration tool.
	if err := client.Schema.Create(context.Background()); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed creating schema resources: %w", err)
	}

	log.Printf("Database initialized (WAL mode) and migrated: %s", cfg.Database.Path)
	return client, nil
}
