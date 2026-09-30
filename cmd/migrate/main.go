// Command migrate wraps goose over the embedded migrations.
//
//	migrate up | down | reset | status | version | redo | up-to N | down-to N
//
// DATABASE_URL is read from the environment. Fly runs "migrate up" as the
// release_command before new machines start.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hi-naresh/unsolved/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down|reset|status|version|redo|up-to N|down-to N>")
		os.Exit(2)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}
	if err := store.Migrate(context.Background(), url, os.Args[1], os.Args[2:]...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
