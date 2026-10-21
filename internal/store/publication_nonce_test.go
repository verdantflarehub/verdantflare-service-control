package store

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicationNonceClaimIsAtomic(t *testing.T) {
	repository := NewMemoryBootstrap()
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repository.ClaimPublicationNonce(context.Background(), "user/nonce", time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("nonce accepted %d times, want 1", accepted)
	}
}

func TestPublicationNonceSharedAcrossPostgresInstances(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("disposable *_test database required")
	}
	first, err := OpenPostgres(databaseURL, 2, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenPostgres(databaseURL, 2, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	key := "test/" + time.Now().UTC().Format(time.RFC3339Nano)
	if accepted, err := first.ClaimPublicationNonce(context.Background(), key, time.Minute); err != nil || !accepted {
		t.Fatalf("first claim = %v, %v", accepted, err)
	}
	if accepted, err := second.ClaimPublicationNonce(context.Background(), key, time.Minute); err != nil || accepted {
		t.Fatalf("second claim = %v, %v", accepted, err)
	}
}
