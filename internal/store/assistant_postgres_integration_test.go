//go:build integration

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/verdantflarehub/verdantflare-service-control/internal/domain"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAssistantPostgresRevisionLifecycle(t *testing.T) {
	connection := os.Getenv("ASSISTANT_TEST_DATABASE_URL")
	u, err := url.Parse(connection)
	if err != nil || !strings.HasPrefix(u.Path, "/pa_test_") {
		t.Fatal("isolated pa_test_ database required; test must not skip")
	}
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migration, err := os.ReadFile("migrations/postgres/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := OpenPostgres(connection, 8, 2, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	fixture := assistantFixture()
	fixture.identityRevision = 0
	raw, err := encodeMemory(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.db.Exec("UPDATE control_repository_state SET state=$1 WHERE singleton=TRUE", raw); err != nil {
		t.Fatal(err)
	}
	t.Run("explicit-initialization", func(t *testing.T) {
		if p.AssistantIdentityReady(ctx) == nil {
			t.Fatal("uninitialized identity exposed")
		}
		if err = p.AdvanceAssistantIdentityRevision(ctx, 100); err != nil {
			t.Fatal(err)
		}
		got, e := p.ResolveAssistantIdentity(ctx, "login-a")
		if e != nil || got.Revision != "101" {
			t.Fatalf("%+v %v", got, e)
		}
	})
	t.Run("organization-freeze-and-restart", func(t *testing.T) {
		if _, err = p.UpdateManagedOrganization(ctx, "org-a", domain.Organization{OrganizationID: "org-a", Status: "冻结"}, nil, 0); err != nil {
			t.Fatal(err)
		}
		if _, err = p.CheckAssistantIdentity(ctx, "owner-a", "org-a"); err == nil {
			t.Fatal("frozen organization accepted")
		}
		restarted, e := OpenPostgres(connection, 2, 1, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		defer restarted.Close()
		snapshot, e := restarted.read(ctx)
		if e != nil || snapshot.identityRevision != 102 {
			t.Fatal("revision lost on reopen")
		}
	})
	t.Run("failed-mutation-is-atomic", func(t *testing.T) {
		e := p.mutate(ctx, func(m *Memory) error {
			m.organizations["org-a"] = domain.Organization{OrganizationID: "org-a", Status: "正常"}
			return errors.New("injected abort")
		})
		if e == nil {
			t.Fatal("injected abort ignored")
		}
		snapshot, e := p.read(ctx)
		if e != nil || snapshot.identityRevision != 102 || snapshot.organizations["org-a"].Status != "冻结" {
			t.Fatal("failed mutation escaped transaction")
		}
	})
	t.Run("concurrent-writers-preserve-monotonic-revision", func(t *testing.T) {
		var wg sync.WaitGroup
		failures := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				failures <- p.mutate(ctx, func(m *Memory) error {
					key := fmt.Sprintf("org-extra-%d", i)
					m.organizations[key] = domain.Organization{OrganizationID: key, Status: "正常"}
					return nil
				})
			}(i)
		}
		wg.Wait()
		close(failures)
		for e := range failures {
			if e != nil {
				t.Fatal(e)
			}
		}
		snapshot, e := p.read(ctx)
		if e != nil || snapshot.identityRevision != 110 {
			t.Fatal("concurrent revision increments lost")
		}
	})
	t.Run("restore-fencing-uses-external-high-water", func(t *testing.T) {
		if _, err = p.db.Exec("UPDATE control_repository_state SET state=$1 WHERE singleton=TRUE", raw); err != nil {
			t.Fatal(err)
		}
		if err = p.AdvanceAssistantIdentityRevision(ctx, 110); err != nil {
			t.Fatal(err)
		}
		got, e := p.ResolveAssistantIdentity(ctx, "login-a")
		if e != nil || got.Revision != "111" {
			t.Fatalf("%+v %v", got, e)
		}
	})
}
