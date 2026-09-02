package store

import (
	"context"
	"errors"
	"testing"
)

// Die Kennung landet in Dateinamen und Dienstverweisen (der Katalog nennt
// z.B. eine systemd-Unit "volt-plugin-redis"). Sie kommt zwar aus dem
// Quelltext, nicht aus einer Anfrage — geprüft wird sie trotzdem, aus
// demselben Grund wie überall in diesem Projekt: eine Prüfung, die sich auf
// die Herkunft eines Werts verlässt statt auf seine Form, bricht beim ersten
// Refactoring still.
func TestValidPluginID(t *testing.T) {
	gut := []string{"redis", "phpmyadmin", "node-red", "wp1"}
	for _, id := range gut {
		if !ValidPluginID(id) {
			t.Errorf("%q wurde abgelehnt", id)
		}
	}
	schlecht := []string{
		"", "a", "-redis", "redis-", "Redis", "redis_cache", "redis cache",
		"redis;rm -rf /", "../etc", "redis\nrm",
	}
	for _, id := range schlecht {
		if ValidPluginID(id) {
			t.Errorf("%q wurde angenommen", id)
		}
	}
}

// SetPlugin ist ein Upsert: die erste Installation legt installed_at fest,
// jede weitere lässt es stehen. Ohne das verlöre ein Update — enable/disable
// zählt hier auch als "weitere" — den ursprünglichen Installationszeitpunkt.
func TestSetPluginBehaeltInstalledAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetPlugin(ctx, "redis", true, "{}"); err != nil {
		t.Fatal(err)
	}
	erst, err := s.GetPlugin(ctx, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if erst.InstalledAt == nil {
		t.Fatal("installed_at fehlt nach dem ersten Set")
	}

	// Auf einen Wert setzen, den "jetzt" nie träfe: ein Test, der stattdessen
	// eine Sekunde wartet und zwei echte Zeitstempel vergleicht, wäre langsam
	// und liefe genau dann grün, wenn beide Aufrufe zufällig in dieselbe
	// Sekunde fielen — die Prüfung stünde da, ohne je etwas zu prüfen.
	const sentinel = 12345
	if _, err := s.db.ExecContext(ctx,
		`UPDATE plugins SET installed_at = ? WHERE id = 'redis'`, sentinel); err != nil {
		t.Fatal(err)
	}
	zeit := int64(sentinel)

	if err := s.SetPlugin(ctx, "redis", false, "{}"); err != nil {
		t.Fatal(err)
	}
	nachher, err := s.GetPlugin(ctx, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if nachher.Enabled {
		t.Error("enabled wurde nicht auf false gesetzt")
	}
	if nachher.InstalledAt == nil || *nachher.InstalledAt != zeit {
		t.Errorf("installed_at hat sich geändert: %v -> %v", zeit, nachher.InstalledAt)
	}
}

func TestGetPluginNichtGefunden(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetPlugin(context.Background(), "redis"); !errors.Is(err, ErrNotFound) {
		t.Errorf("erwartet ErrNotFound, bekommen: %v", err)
	}
}

func TestDeletePlugin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SetPlugin(ctx, "redis", true, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlugin(ctx, "redis"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPlugin(ctx, "redis"); !errors.Is(err, ErrNotFound) {
		t.Errorf("nach dem löschen noch da: %v", err)
	}
}
