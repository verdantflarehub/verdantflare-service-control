// Command survey-storage inspects or atomically switches one survey's storage
// format after the additive migration and compatible Control release are live.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

func main() {
	action := flag.String("action", "status", "status, cutover, or restore")
	survey := flag.String("survey", "", "exact survey ID")
	flag.Parse()
	if *survey == "" || strings.TrimSpace(*survey) != *survey || strings.ContainsAny(*survey, " \t\r\n") {
		fail("-survey must be one exact survey ID")
	}
	if *action != "status" && *action != "cutover" && *action != "restore" {
		fail("-action must be status, cutover, or restore")
	}
	databaseURL := os.Getenv("CONTROL_DATABASE_URL")
	if databaseURL == "" {
		fail("CONTROL_DATABASE_URL is required")
	}
	repo, err := store.OpenPostgres(databaseURL, 2, 2, time.Now().UTC())
	if err != nil {
		fail("open repository: %v", err)
	}
	defer repo.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch *action {
	case "cutover":
		err = repo.CutoverSurveyRows(ctx, *survey)
	case "restore":
		err = repo.RestoreLegacySurveyRows(ctx, *survey)
	}
	if err != nil {
		fail("%s failed: %v", *action, err)
	}
	version, err := repo.SurveyStorageVersion(ctx, *survey)
	if err != nil {
		fail("read storage version: %v", err)
	}
	fmt.Printf("survey storage version: %d\n", version)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
