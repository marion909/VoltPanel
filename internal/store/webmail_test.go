package store

import (
	"context"
	"errors"
	"testing"
)

func TestGetWebmailNichtGefunden(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetWebmail(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Errorf("erwartet ErrNotFound, bekommen: %v", err)
	}
}

// SetWebmail ist ein Upsert wie SetPlugin: die erste Installation legt
// installed_at fest, jede weitere (z.B. ein neu gesetztes Datenbankpasswort)
// lässt es stehen.
func TestSetWebmailBehaeltInstalledAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetWebmail(ctx, "webmail.example.at", "8.3", "volt_webmail", "volt_webmail", "verschluesselt-1"); err != nil {
		t.Fatal(err)
	}
	erst, err := s.GetWebmail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if erst.InstalledAt == 0 {
		t.Fatal("installed_at fehlt nach dem ersten Set")
	}

	const sentinel = 12345
	if _, err := s.db.ExecContext(ctx,
		`UPDATE webmail SET installed_at = ? WHERE id = 1`, sentinel); err != nil {
		t.Fatal(err)
	}

	if err := s.SetWebmail(ctx, "webmail.example.at", "8.3", "volt_webmail", "volt_webmail", "verschluesselt-2"); err != nil {
		t.Fatal(err)
	}
	nachher, err := s.GetWebmail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nachher.InstalledAt != sentinel {
		t.Errorf("installed_at hat sich geändert: %d -> %d", sentinel, nachher.InstalledAt)
	}
	if nachher.DBPasswordEnc != "verschluesselt-2" {
		t.Errorf("db_password_enc wurde nicht aktualisiert: %q", nachher.DBPasswordEnc)
	}
}

func TestDeleteWebmail(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SetWebmail(ctx, "webmail.example.at", "8.3", "volt_webmail", "volt_webmail", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWebmail(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetWebmail(ctx); !errors.Is(err, ErrNotFound) {
		t.Errorf("nach dem löschen noch da: %v", err)
	}
}

// Es gibt nur eine Zeile. Ein zweiter Set-Aufruf mit anderen Werten ersetzt
// die erste, statt eine zweite Installation nebendran anzulegen.
func TestWebmailBleibtEineZeile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SetWebmail(ctx, "webmail.a.at", "8.2", "db_a", "user_a", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWebmail(ctx, "webmail.b.at", "8.3", "db_b", "user_b", "y"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM webmail`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d zeilen in webmail, erwartet 1", n)
	}
	w, err := s.GetWebmail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if w.Hostname != "webmail.b.at" {
		t.Errorf("hostname = %q, erwartet webmail.b.at", w.Hostname)
	}
	if w.PHPVersion != "8.3" {
		t.Errorf("php_version = %q, erwartet 8.3", w.PHPVersion)
	}
}
