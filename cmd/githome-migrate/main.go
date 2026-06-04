// Command githome-migrate applies or rolls back Githome's database migrations.
//
// Usage:
//
//	githome-migrate up                 apply all pending migrations
//	githome-migrate down [n]           roll back the last n migrations (default 1)
//	githome-migrate version            print the current schema version
//
// The database is selected by GITHOME_DATABASE_URL, the same variable the server
// uses; the dialect is inferred from its scheme.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/tamnd/githome/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "githome-migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: githome-migrate up|down [n]|version")
	}

	dsn := os.Getenv("GITHOME_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("GITHOME_DATABASE_URL is required")
	}

	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	switch args[0] {
	case "up":
		if err := st.Migrate(ctx); err != nil {
			return err
		}
		v, err := st.Version(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("migrated up; schema version %d\n", v)
		return nil

	case "down":
		n := 1
		if len(args) > 1 {
			parsed, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid step count %q", args[1])
			}
			n = parsed
		}
		if err := st.Rollback(ctx, n); err != nil {
			return err
		}
		v, err := st.Version(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("rolled back %d; schema version %d\n", n, v)
		return nil

	case "version":
		v, err := st.Version(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%d\n", v)
		return nil

	default:
		return fmt.Errorf("unknown command %q (want up|down|version)", args[0])
	}
}
