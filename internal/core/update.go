package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/version"
)

// Updater aktualisiert das Binary und wandert dabei durch die Migrationen.
//
// Der Ablauf ist so gebaut, dass jeder Schritt umkehrbar bleibt (Prinzip 4 der
// Roadmap): Snapshot ziehen, tauschen, migrieren — und bei jedem Fehler alles
// zurücknehmen, bevor die Dienste wieder starten.
type Updater struct {
	cfg   *config.Config
	store *store.Store
	log   *slog.Logger
	http  *http.Client
}

func NewUpdater(cfg *config.Config, st *store.Store, log *slog.Logger) *Updater {
	if log == nil {
		log = slog.Default()
	}
	return &Updater{
		cfg: cfg, store: st, log: log,
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

// Release beschreibt eine Version aus dem Update-Kanal.
type Release struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
	// Assets sind nach "linux_amd64" / "linux_arm64" benannt.
	Assets map[string]ReleaseAsset `json:"assets"`
}

type ReleaseAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Platform ist der Asset-Schlüssel für das laufende System.
func Platform() string { return runtime.GOOS + "_" + runtime.GOARCH }

// LatestRelease fragt den konfigurierten Kanal ab.
func (u *Updater) LatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/%s/latest.json", strings.TrimRight(u.cfg.UpdateBaseURL, "/"), u.cfg.UpdateChannel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "volt/"+version.Version)

	resp, err := u.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update-kanal %s nicht erreichbar: %w", u.cfg.UpdateChannel, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update-kanal antwortet mit %s", resp.Status)
	}

	var rel Release
	if err := decodeJSON(resp.Body, &rel); err != nil {
		return nil, fmt.Errorf("release-info unlesbar: %w", err)
	}
	if rel.Version == "" {
		return nil, errors.New("release-info enthält keine version")
	}
	return &rel, nil
}

// Snapshot ist der Stand vor einem Update, auf den zurückgerollt werden kann.
type Snapshot struct {
	Dir        string
	BinaryPath string
	DBPath     string
	Version    string
	SchemaFrom int
	CreatedAt  time.Time
}

// Snapshot sichert Binary, Datenbank und Konfigurationen.
func (u *Updater) Snapshot(ctx context.Context) (*Snapshot, error) {
	stamp := time.Now().UTC().Format("20060102-150405")
	dir := filepath.Join(u.cfg.BackupDir, "pre-update-"+stamp)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("snapshot-verzeichnis: %w", err)
	}

	schemaVersion, err := u.store.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}

	snap := &Snapshot{
		Dir: dir, Version: version.Version, SchemaFrom: schemaVersion, CreatedAt: time.Now(),
		BinaryPath: filepath.Join(dir, "volt"),
		DBPath:     filepath.Join(dir, "volt.db"),
	}

	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("eigenen pfad ermitteln: %w", err)
	}
	if err := copyFile(self, snap.BinaryPath, 0o755); err != nil {
		return nil, fmt.Errorf("binary sichern: %w", err)
	}
	// VACUUM INTO liefert eine in sich konsistente Kopie, auch während Schreibzugriffe laufen.
	if err := u.store.Backup(ctx, snap.DBPath); err != nil {
		return nil, fmt.Errorf("datenbank sichern: %w", err)
	}
	if err := copyTree(u.cfg.ConfigDir, filepath.Join(dir, "etc")); err != nil {
		u.log.Warn("konfiguration konnte nicht gesichert werden", "err", err)
	}

	u.log.Info("snapshot angelegt", "verzeichnis", dir, "version", snap.Version, "schema", schemaVersion)
	return snap, nil
}

// Apply lädt das neue Binary, tauscht es und migriert die Datenbank.
//
// Scheitert die Migration, wird das alte Binary und die gesicherte Datenbank
// zurückgespielt. Der Aufrufer bekommt dann den ursprünglichen Fehler.
func (u *Updater) Apply(ctx context.Context, rel *Release, snap *Snapshot) error {
	asset, ok := rel.Assets[Platform()]
	if !ok {
		return fmt.Errorf("release %s enthält kein paket für %s", rel.Version, Platform())
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}

	// Die temporäre Datei muss im selben Dateisystem liegen, sonst schlägt das
	// Rename fehl (cross-device link).
	tmpPath := self + ".new"
	if err := u.download(ctx, asset, tmpPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, self); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("binary tauschen: %w", err)
	}
	u.log.Info("binary getauscht", "pfad", self, "version", rel.Version)

	from, to, err := u.store.Migrate(ctx)
	if err != nil {
		u.log.Error("migration fehlgeschlagen, rolle zurück", "err", err)
		if rbErr := u.Rollback(ctx, snap); rbErr != nil {
			return fmt.Errorf("migration fehlgeschlagen (%w) UND rollback fehlgeschlagen: %v", err, rbErr)
		}
		return fmt.Errorf("migration fehlgeschlagen, stand wurde zurückgerollt: %w", err)
	}
	if from != to {
		u.log.Info("schema migriert", "von", from, "auf", to)
	}
	return nil
}

// Rollback spielt Binary und Datenbank aus dem Snapshot zurück.
func (u *Updater) Rollback(_ context.Context, snap *Snapshot) error {
	if snap == nil {
		return errors.New("kein snapshot vorhanden")
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}

	if err := copyFile(snap.BinaryPath, self+".rollback", 0o755); err != nil {
		return fmt.Errorf("altes binary bereitstellen: %w", err)
	}
	if err := os.Rename(self+".rollback", self); err != nil {
		return fmt.Errorf("altes binary zurücktauschen: %w", err)
	}

	// Die Datenbank zuletzt: erst wenn das alte Binary wieder liegt, passt das
	// alte Schema dazu.
	if err := u.store.Close(); err != nil {
		u.log.Warn("datenbank nicht sauber geschlossen", "err", err)
	}
	if err := copyFile(snap.DBPath, u.cfg.DBPath, 0o600); err != nil {
		return fmt.Errorf("datenbank zurückspielen: %w", err)
	}
	// Die WAL-Dateien gehören zum alten Stand und würden die Kopie überschreiben.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(u.cfg.DBPath + suffix)
	}

	u.log.Info("rollback abgeschlossen", "version", snap.Version, "snapshot", snap.Dir)
	return nil
}

// download lädt das Asset und prüft die Prüfsumme, bevor die Datei benutzt wird.
func (u *Updater) download(ctx context.Context, asset ReleaseAsset, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "volt/"+version.Version)

	resp, err := u.http.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download antwortet mit %s", resp.Status)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()

	hasher := sha256.New()
	// Begrenzt auf die angekündigte Größe plus Reserve — ein manipulierter
	// Server soll die Platte nicht vollschreiben können.
	limit := asset.Size + 1
	if asset.Size <= 0 {
		limit = 512 << 20
	}
	written, err := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(resp.Body, limit))
	if err != nil {
		return fmt.Errorf("download schreiben: %w", err)
	}
	if asset.Size > 0 && written != asset.Size {
		return fmt.Errorf("download ist %d bytes groß, erwartet waren %d", written, asset.Size)
	}

	// Die Prüfsumme ist der eigentliche Schutz: nur wer den Release signiert
	// hat, kennt sie — ein manipuliertes Binary fällt hier auf.
	if got := hex.EncodeToString(hasher.Sum(nil)); !strings.EqualFold(got, asset.SHA256) {
		return fmt.Errorf("prüfsumme stimmt nicht: erwartet %s, bekommen %s", asset.SHA256, got)
	}
	return f.Sync()
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// copyTree kopiert ein Verzeichnis flach rekursiv, ohne Symlinks zu verfolgen.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o750)
		case info.Mode()&os.ModeSymlink != 0:
			return nil // Symlinks werden nicht mitgesichert
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}
