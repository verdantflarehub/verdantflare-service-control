package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func run() error {
	floor := flag.Uint64("floor", 0, "externally retained pre-restore high-water mark; zero for first initialization")
	flag.Parse()
	database := os.Getenv("CONTROL_DATABASE_URL")
	if database == "" {
		return fmt.Errorf("CONTROL_DATABASE_URL is required")
	}
	repository, err := store.OpenPostgres(database, 1, 1, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("cannot open identity repository")
	}
	defer repository.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := repository.AdvanceAssistantIdentityRevision(ctx, *floor); err != nil {
		return fmt.Errorf("cannot advance identity revision")
	}
	fmt.Println("Assistant identity revision advanced; verify restored state before enabling its listener.")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
