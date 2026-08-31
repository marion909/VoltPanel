package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// defaultSystemdDir ist der Ort, an den install.sh die Units legt.
const defaultSystemdDir = "/etc/systemd/system"

// unitPrefix begrenzt, welche Dateien ein Release überhaupt ersetzen darf.
// Ohne diese Schranke könnte ein Fahrplan eine beliebige Unit des Systems
// überschreiben — etwa die von SSH.
const unitPrefix = "volt-"

func (u *Updater) unitDir() string {
	if u.systemdDir != "" {
		return u.systemdDir
	}
	return defaultSystemdDir
}

// checkUnitName lässt nur die eigenen Units durch.
func checkUnitName(name string) error {
	switch {
	case name != filepath.Base(name) || name == "":
		return fmt.Errorf("unit-name %q enthält einen pfad", name)
	case !strings.HasPrefix(name, unitPrefix):
		return fmt.Errorf("unit %q gehört nicht zu VoltPanel", name)
	case !strings.HasSuffix(name, ".service") && !strings.HasSuffix(name, ".timer"):
		return fmt.Errorf("unit %q ist weder service noch timer", name)
	}
	return nil
}

// syncUnits bringt die systemd-Units auf den Stand des Releases.
//
// Ohne diesen Schritt liefe nach einem Update ein neues Programm unter einer
// alten Unit weiter. Das ist kein theoretischer Fall: eine geänderte
// Sandbox-Regel oder ein neuer Schreibpfad wirkt erst mit der neuen Unit, und
// bis dahin scheitern Operationen aus Gründen, die niemand in der Unit sucht.
//
// Geändert wird nur, was sich unterscheidet. Die alte Fassung wandert vorher
// in den Snapshot, damit ein Rollback sie zurückholen kann.
func (u *Updater) syncUnits(ctx context.Context, rel *Release, snap *Snapshot) (changed []string, err error) {
	if len(rel.Units) == 0 {
		return nil, nil
	}

	dir := u.unitDir()
	if _, err := os.Stat(dir); err != nil {
		// Kein systemd-Verzeichnis: eine Entwicklungsumgebung, kein Fehler.
		u.log.Debug("kein systemd-verzeichnis, units bleiben unangetastet", "pfad", dir)
		return nil, nil
	}

	for name, asset := range rel.Units {
		if err := checkUnitName(name); err != nil {
			return changed, err
		}

		target := filepath.Join(dir, name)
		tmp := target + ".new"
		if err := u.download(ctx, asset, tmp); err != nil {
			os.Remove(tmp)
			return changed, fmt.Errorf("unit %s laden: %w", name, err)
		}

		same, err := sameContent(target, tmp)
		if err != nil {
			os.Remove(tmp)
			return changed, err
		}
		if same {
			os.Remove(tmp)
			continue
		}

		if err := backupUnit(target, snap); err != nil {
			os.Remove(tmp)
			return changed, err
		}
		// Units sind Konfiguration, kein Programm: 0644, nicht ausführbar.
		if err := os.Chmod(tmp, 0o644); err != nil {
			os.Remove(tmp)
			return changed, err
		}
		if err := os.Rename(tmp, target); err != nil {
			os.Remove(tmp)
			return changed, fmt.Errorf("unit %s ersetzen: %w", name, err)
		}
		changed = append(changed, name)
	}

	if len(changed) > 0 {
		if err := u.daemonReload(); err != nil {
			// Kein Abbruch: die Dateien liegen richtig, systemd holt sie
			// spätestens beim nächsten reload. Wer das Update auslöst, soll
			// es aber erfahren.
			u.log.Warn("daemon-reload fehlgeschlagen — units erst nach `systemctl daemon-reload` wirksam",
				"err", err)
		}
		u.log.Info("systemd-units aktualisiert", "dateien", changed)
	}
	return changed, nil
}

// restoreUnits spielt die gesicherten Units zurück.
func (u *Updater) restoreUnits(snap *Snapshot) error {
	if snap == nil || snap.UnitDir == "" {
		return nil
	}
	entries, err := os.ReadDir(snap.UnitDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	dir := u.unitDir()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := checkUnitName(e.Name()); err != nil {
			continue
		}
		if err := copyFile(filepath.Join(snap.UnitDir, e.Name()),
			filepath.Join(dir, e.Name()), 0o644); err != nil {
			return fmt.Errorf("unit %s zurückspielen: %w", e.Name(), err)
		}
	}
	if len(entries) > 0 {
		_ = u.daemonReload()
	}
	return nil
}

func (u *Updater) daemonReload() error {
	if u.reload != nil {
		return u.reload()
	}
	// Fester Pfad und festes argv — hier geht kein Wert von außen ein.
	return exec.Command("/usr/bin/systemctl", "daemon-reload").Run()
}

// backupUnit legt die bisherige Fassung in den Snapshot, bevor sie ersetzt
// wird. Fehlt die Datei bisher, gibt es nichts zu sichern.
func backupUnit(target string, snap *Snapshot) error {
	if snap == nil {
		return nil
	}
	if _, err := os.Stat(target); err != nil {
		return nil
	}
	if snap.UnitDir == "" {
		snap.UnitDir = filepath.Join(snap.Dir, "systemd")
	}
	if err := os.MkdirAll(snap.UnitDir, 0o750); err != nil {
		return fmt.Errorf("snapshot-verzeichnis für units: %w", err)
	}
	return copyFile(target, filepath.Join(snap.UnitDir, filepath.Base(target)), 0o644)
}

func sameContent(a, b string) (bool, error) {
	left, err := os.ReadFile(a)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	right, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return string(left) == string(right), nil
}
