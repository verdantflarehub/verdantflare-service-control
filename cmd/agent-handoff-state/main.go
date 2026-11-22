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
	advance := flag.Bool("advance", false, "explicitly advance the global handoff epoch")
	floor := flag.Uint64("floor", 0, "externally retained pre-restore handoff epoch; zero only for first initialization")
	flag.Parse()
	database := os.Getenv("CONTROL_DATABASE_URL")
	if database == "" {
		return fmt.Errorf("CONTROL_DATABASE_URL is required")
	}
	repository, err := store.OpenPostgres(database, 1, 1, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("cannot open handoff repository")
	}
	defer repository.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if *advance {
		if err := repository.AdvanceAgentHandoffEpoch(ctx, *floor); err != nil {
			return fmt.Errorf("cannot advance handoff epoch")
		}
	}
	identityRevision, epoch, err := repository.AgentHandoffHighWater(ctx)
	if err != nil {
		return fmt.Errorf("cannot read handoff high-water marks")
	}
	fmt.Printf("identity_revision=%d authentication_epoch=%d\n", identityRevision, epoch)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
