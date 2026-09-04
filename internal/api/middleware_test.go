package api

import (
	"context"
	"testing"
	"time"
)

// TestRateLimiterPruneEntferntAbgelaufeneEintraege hält die Kernlogik hinter
// dem vorher nirgends aufgerufenen Cleanup() fest: ohne sie wächst buckets mit
// jeder eindeutigen Quell-IP, die den Login-Endpunkt je erreicht, unbegrenzt.
func TestRateLimiterPruneEntferntAbgelaufeneEintraege(t *testing.T) {
	r := newRateLimiter(5, time.Minute)
	r.buckets["abgelaufen"] = []time.Time{time.Now().Add(-2 * time.Minute)}
	r.buckets["frisch"] = []time.Time{time.Now()}

	r.prune()

	if _, ok := r.buckets["abgelaufen"]; ok {
		t.Error("prune ließ einen abgelaufenen Eintrag stehen")
	}
	if _, ok := r.buckets["frisch"]; !ok {
		t.Error("prune entfernte einen noch gültigen Eintrag")
	}
}

// TestRateLimiterCleanupEndetMitDemContext stellt sicher, dass die Goroutine,
// die serve.go jetzt über Server.RunLoginRateCleanup startet, sauber endet,
// statt den Prozess am Beenden zu hindern.
func TestRateLimiterCleanupEndetMitDemContext(t *testing.T) {
	r := newRateLimiter(5, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		r.Cleanup(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Cleanup kehrte nach abgebrochenem Context nicht zurück")
	}
}
