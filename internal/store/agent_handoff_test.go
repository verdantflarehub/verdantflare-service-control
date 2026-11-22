package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestHandoffSingleUseAndRevocation(t *testing.T) {
	m := NewMemoryBootstrap()
	m.identityRevision = 1
	const subject = "00000000-0000-4000-8000-000000000001"
	user := m.users[subject]
	user.Memberships[0].Status = "正常"
	m.users[subject] = user
	now := time.Now().UTC()
	intent := HandoffIntent{RequestRef: "request-ref", RequestHash: "request-hash", ProviderRef: "center", IntentID: "intent-1", RecipientRef: "studio-service", StationID: "station-1", LocalOrgRef: "local-1", ExternalOrgRef: "org_verdantflare", CallbackRef: "studio", CallbackURL: "https://studio.example.test/callback", BrowserOrigin: "https://hub.example.test", Nonce: "nonce-1", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	const workers = 12
	results := make(chan HandoffIntent, workers)
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := m.RegisterHandoff(context.Background(), intent, now)
			results <- got
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for got := range results {
		if got.RequestRef != intent.RequestRef {
			t.Fatalf("duplicate registration changed ref: %+v", got)
		}
	}
	if len(m.handoffIntents) != 1 {
		t.Fatalf("registered %d intents", len(m.handoffIntents))
	}
	changed := intent
	changed.RequestHash = "different"
	if _, err := m.RegisterHandoff(context.Background(), changed, now); err == nil {
		t.Fatal("same intent ID with different body accepted")
	}
	reusedNonce := intent
	reusedNonce.RequestRef = "request-ref-2"
	reusedNonce.RequestHash = "request-hash-2"
	reusedNonce.IntentID = "intent-2"
	if _, err := m.RegisterHandoff(context.Background(), reusedNonce, now); err == nil {
		t.Fatal("same consumer reused nonce across intents")
	}
	fact := HandoffLoginFact{SubjectRef: subject, AuthenticationRef: "login-session-1", ExpiresAt: now.Add(time.Hour), CheckedAt: now}
	confirmed, err := m.ConfirmHandoff(context.Background(), intent.RequestRef, "state-1", "handoff-ref", "provider-auth-ref", fact, now)
	if err != nil || confirmed.Status != "confirmed" {
		t.Fatalf("confirm: %+v %v", confirmed, err)
	}
	if _, err := m.ConfirmHandoff(context.Background(), intent.RequestRef, "state-2", "other-ref", "other-auth-ref", fact, now); err == nil {
		t.Fatal("second confirmation accepted")
	}
	exchange := HandoffExchangeInput{ProviderRef: intent.ProviderRef, IntentID: intent.IntentID, RequestRef: intent.RequestRef, HandoffRef: "handoff-ref", RecipientRef: intent.RecipientRef, StationID: intent.StationID, LocalOrgRef: intent.LocalOrgRef, Nonce: intent.Nonce, ExchangeID: "exchange-1"}
	proof, err := m.ExchangeHandoff(context.Background(), exchange, "exchange-hash", "event-1", "center-issuer", fact, now)
	if err != nil || proof.EventID != "event-1" || proof.CenterUserRef != user.CenterUserID || proof.AuthenticationEpoch != "1" {
		t.Fatalf("exchange: %+v %v", proof, err)
	}
	recovered, err := m.ExchangeHandoff(context.Background(), exchange, "exchange-hash", "new-event", "center-issuer", fact, now.Add(time.Second))
	if err != nil || recovered.EventID != proof.EventID || !recovered.ExpiresAt.Equal(proof.ExpiresAt) {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	if _, err := m.ExchangeHandoff(context.Background(), exchange, "exchange-hash", "new-event", "center-issuer", HandoffLoginFact{}, now.Add(time.Second)); err == nil {
		t.Fatal("cached proof replayed after login revocation")
	}
	if _, err := m.ExchangeHandoff(context.Background(), exchange, "other-hash", "event-2", "center-issuer", fact, now); err == nil {
		t.Fatal("same exchange ID with different body accepted")
	}
	if _, err := m.CheckHandoff(context.Background(), proof.AuthenticationRef, proof.AuthenticationEpoch, subject, user.CenterUserID, intent.ExternalOrgRef, intent.RecipientRef, intent.StationID); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := m.RevokeAllHandoffs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CheckHandoff(context.Background(), proof.AuthenticationRef, proof.AuthenticationEpoch, subject, user.CenterUserID, intent.ExternalOrgRef, intent.RecipientRef, intent.StationID); err == nil {
		t.Fatal("global epoch revocation ignored")
	}
}

func TestHandoffConsumerCancellationRevokesProof(t *testing.T) {
	m := NewMemoryBootstrap()
	m.identityRevision = 1
	const subject = "00000000-0000-4000-8000-000000000001"
	user := m.users[subject]
	user.Memberships[0].Status = "正常"
	m.users[subject] = user
	now := time.Now().UTC()
	intent := HandoffIntent{RequestRef: "request-cancel", RequestHash: "hash-cancel", ProviderRef: "center", IntentID: "intent-cancel", RecipientRef: "studio-service", StationID: "station-1", LocalOrgRef: "local-1", ExternalOrgRef: "org_verdantflare", Nonce: "0123456789abcdef", ExpiresAt: now.Add(5 * time.Minute)}
	if _, err := m.RegisterHandoff(context.Background(), intent, now); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelHandoff(context.Background(), intent.RequestRef, intent.IntentID, intent.RecipientRef, intent.StationID, "wrong-nonce-12345"); err == nil {
		t.Fatal("incorrect nonce cancelled intent")
	}
	fact := HandoffLoginFact{SubjectRef: subject, AuthenticationRef: "login-session", ExpiresAt: now.Add(time.Hour)}
	if _, err := m.ConfirmHandoff(context.Background(), intent.RequestRef, "state", "handoff-ref", "auth-ref", fact, now); err != nil {
		t.Fatal(err)
	}
	exchange := HandoffExchangeInput{ProviderRef: intent.ProviderRef, IntentID: intent.IntentID, RequestRef: intent.RequestRef, HandoffRef: "handoff-ref", RecipientRef: intent.RecipientRef, StationID: intent.StationID, LocalOrgRef: intent.LocalOrgRef, Nonce: intent.Nonce, ExchangeID: "exchange-1"}
	proof, err := m.ExchangeHandoff(context.Background(), exchange, "hash", "event", "issuer", fact, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CancelHandoff(context.Background(), intent.RequestRef, intent.IntentID, intent.RecipientRef, intent.StationID, intent.Nonce); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelHandoff(context.Background(), intent.RequestRef, intent.IntentID, intent.RecipientRef, intent.StationID, intent.Nonce); err != nil {
		t.Fatalf("idempotent cancellation: %v", err)
	}
	if _, err := m.CheckHandoff(context.Background(), proof.AuthenticationRef, proof.AuthenticationEpoch, subject, user.CenterUserID, intent.ExternalOrgRef, intent.RecipientRef, intent.StationID); err == nil {
		t.Fatal("cancelled proof remained active")
	}
	if _, err := m.ExchangeHandoff(context.Background(), exchange, "hash", "event", "issuer", fact, now); err == nil {
		t.Fatal("cancelled exchange was replayed")
	}
}
