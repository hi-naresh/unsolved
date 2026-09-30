package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hi-naresh/unsolved/migrations"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"
)

// Migrate runs a goose command ("up", "down", "reset", "status", "version",
// "redo") against the embedded migrations.
func Migrate(ctx context.Context, databaseURL, command string, args ...string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.RunContext(ctx, command, db, ".", args...); err != nil {
		return fmt.Errorf("goose %s: %w", command, err)
	}
	return nil
}
