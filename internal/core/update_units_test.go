package core

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSyncUnitsReplacesOnlyWhatChanged deckt die Drift ab, die diese Sitzung
// viermal gekostet hat: neue Programme unter alten Units. Eine geänderte
// Sandbox-Regel wirkt erst mit der neuen Unit, und bis dahin scheitern
// Operationen aus Gründen, die niemand in einer Unit-Datei sucht.
func TestSyncUnitsReplacesOnlyWhatChanged(t *testing.T) {
	env := newUpdateEnv(t, true)
	ctx := context.Background()

	unitDir := t.TempDir()
	env.updater.systemdDir = unitDir

	reloads := 0
	env.updater.reload = func() error { reloads++; return nil }

	unchanged := "[Service]\nExecStart=/usr/local/bin/volt serve\n"
	write(t, filepath.Join(unitDir, "volt-web.service"), unchanged)
	write(t, filepath.Join(unitDir, "volt-agent.service"), "[Service]\nalt\n")

	env.release.Units = map[string]ReleaseAsset{
		"volt-web.service":   env.serveText(t, unchanged),
		"volt-agent.service": env.serveText(t, "[Service]\nneu\n"),
	}

	snap, err := env.updater.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := env.updater.syncUnits(ctx, env.release, snap)
	if err != nil {
		t.Fatalf("syncUnits: %v", err)
	}

	if len(changed) != 1 || changed[0] != "volt-agent.service" {
		t.Errorf("geändert: %v — erwartet nur volt-agent.service", changed)
	}
	if got := read(t, filepath.Join(unitDir, "volt-agent.service")); got != "[Service]\nneu\n" {
		t.Errorf("agent-unit ist %q", got)
	}
	if got := read(t, filepath.Join(unitDir, "volt-web.service")); got != unchanged {
		t.Error("die unveränderte Unit wurde angefasst")
	}
	if reloads != 1 {
		t.Errorf("daemon-reload %d mal, erwartet genau einmal", reloads)
	}

	// Ohne gesicherte Fassung könnte ein Rollback die alte Unit nicht
	// zurückholen — und ein halbes Update ist schlimmer als keines.
	if snap.UnitDir == "" {
		t.Fatal("die ersetzte Unit wurde nicht gesichert")
	}
	if got := read(t, filepath.Join(snap.UnitDir, "volt-agent.service")); got != "[Service]\nalt\n" {
		t.Errorf("gesichert wurde %q", got)
	}
}

// TestRestoreUnitsUndoesTheChange: der Rollback muss auch die Units umfassen.
func TestRestoreUnitsUndoesTheChange(t *testing.T) {
	env := newUpdateEnv(t, true)
	ctx := context.Background()

	unitDir := t.TempDir()
	env.updater.systemdDir = unitDir
	env.updater.reload = func() error { return nil }

	write(t, filepath.Join(unitDir, "volt-agent.service"), "[Service]\nalt\n")
	env.release.Units = map[string]ReleaseAsset{
		"volt-agent.service": env.serveText(t, "[Service]\nneu\n"),
	}

	snap, err := env.updater.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.updater.syncUnits(ctx, env.release, snap); err != nil {
		t.Fatal(err)
	}
	if err := env.updater.restoreUnits(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := read(t, filepath.Join(unitDir, "volt-agent.service")); got != "[Service]\nalt\n" {
		t.Errorf("nach dem Rollback steht dort %q", got)
	}
}

// TestUnitNamesAreRestricted: ein Fahrplan darf nur die eigenen Units
// ersetzen. Ohne diese Schranke wäre ein Update ein Weg, eine beliebige Unit
// des Systems zu überschreiben — etwa die von SSH.
func TestUnitNamesAreRestricted(t *testing.T) {
	bad := []string{
		"ssh.service",
		"../ssh.service",
		"volt-web.conf",
		"/etc/systemd/system/volt-web.service",
		"",
	}
	for _, name := range bad {
		if err := checkUnitName(name); err == nil {
			t.Errorf("%q wurde zugelassen", name)
		}
	}

	for _, name := range []string{"volt-web.service", "volt-renew.timer"} {
		if err := checkUnitName(name); err != nil {
			t.Errorf("%q wurde abgelehnt: %v", name, err)
		}
	}
}
