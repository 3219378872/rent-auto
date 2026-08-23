// Command migrate applies or rolls back database migrations.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/3219378872/rent-auto/backend/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fatal("DATABASE_URL is required")
	}
	ctx := context.Background()
	pool, err := store.Open(ctx, url)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()

	switch os.Args[1] {
	case "up":
		applied, err := store.MigrateUp(ctx, pool)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("applied %d migration(s)\n", len(applied))
		for _, v := range applied {
			fmt.Println("  +", v)
		}
	case "down":
		n := 1
		if len(os.Args) > 2 {
			v, err := strconv.Atoi(os.Args[2])
			if err != nil || v < 1 {
				fatal("down count must be a positive integer")
			}
			n = v
		}
		rolled, err := store.MigrateDown(ctx, pool, n)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("rolled back %d migration(s)\n", len(rolled))
		for _, v := range rolled {
			fmt.Println("  -", v)
		}
	case "status":
		migs, err := store.LoadMigrations()
		if err != nil {
			fatal(err)
		}
		appliedSet, err := store.AppliedVersions(ctx, pool)
		if err != nil {
			fatal(err)
		}
		for _, m := range migs {
			mark := "pending"
			if appliedSet[m.Version] {
				mark = "applied"
			}
			fmt.Printf("  %-8s %s\n", mark, m.Version)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: migrate up | down [n] | status   (DATABASE_URL required)")
	os.Exit(2)
}

func fatal(v any) {
	fmt.Fprintln(os.Stderr, "migrate:", v)
	os.Exit(1)
}
