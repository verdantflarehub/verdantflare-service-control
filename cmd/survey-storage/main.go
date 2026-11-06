// Command survey-storage inspects or atomically switches one survey's storage
// format after the additive migration and compatible Control release are live.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	state, err := repo.ReadSurvey(store.WithSurveyID(ctx, *survey))
	if err != nil {
		fail("read survey state: %v", err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		fail("encode survey state: %v", err)
	}
	digest := sha256.Sum256(raw)
	summary := struct {
		Survey          string `json:"survey"`
		StorageVersion  int    `json:"storageVersion"`
		Open            bool   `json:"open"`
		SettingsVersion int    `json:"settingsVersion"`
		Guests          int    `json:"guests"`
		Responses       int    `json:"responses"`
		StateSHA256     string `json:"stateSHA256"`
	}{*survey, version, state.Settings.Open, state.Settings.Version, len(state.Guests), len(state.Responses), hex.EncodeToString(digest[:])}
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		fail("write survey summary: %v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
