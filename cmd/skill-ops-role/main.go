// Command skill-ops-role shows or grants skill_ops_admin on one existing
// Center membership. It is run by hand inside the Control pod.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/verdantflarehub/verdantflare-service-control/internal/store"
)

var exact = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,128}$`)

func main() {
	action := flag.String("action", "status", "status or grant")
	subject := flag.String("subject", "", "exact Login subject")
	organization := flag.String("organization", "", "exact organization ID")
	flag.Parse()
	if !exact.MatchString(*subject) || !exact.MatchString(*organization) {
		fail("-subject and -organization must be exact IDs")
	}
	if *action != "status" && *action != "grant" {
		fail("-action must be status or grant")
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result := map[string]any{"subject": *subject, "organizationId": *organization}
	if *action == "grant" {
		roles, changed, err := repo.GrantSkillOperator(ctx, *subject, *organization)
		if err != nil {
			fail("grant failed: %v", err)
		}
		result["roles"], result["changed"] = roles, changed
	} else {
		roles, err := repo.SkillOperatorRoles(ctx, *subject, *organization)
		if err != nil {
			fail("status failed: %v", err)
		}
		result["roles"] = roles
	}
	raw, _ := json.Marshal(result)
	fmt.Println(string(raw))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
