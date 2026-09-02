package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/release"
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

	// verifier prüft die Signatur über latest.json. Voreingestellt der
	// eingebettete Schlüssel; ein Test setzt seinen eigenen ein.
	verifier *release.Verifier

	// Der Kanal wird höchstens einmal je Stunde befragt. Ohne den
	// Zwischenspeicher fragte jeder Aufruf des Dashboards nach außen — bei
	// mehreren offenen Fenstern im Minutentakt.
	mu      sync.Mutex
	cached  *UpdateStatus
	fetched time.Time

	// self ist der Pfad des eigenen Binaries. Er ist überschreibbar, damit die
	// Tests den Tausch üben können, ohne das Testbinary zu ersetzen.
	self string
	// systemdDir ebenso — ein Test darf die Units des laufenden Systems nicht
	// anfassen.
	systemdDir string
	// reload wird nach geänderten Units aufgerufen. In Tests ersetzbar, damit
	// kein systemctl gebraucht wird.
	reload func() error
}

func NewUpdater(cfg *config.Config, st *store.Store, log *slog.Logger) *Updater {
	if log == nil {
		log = slog.Default()
	}
	return &Updater{
		cfg: cfg, store: st, log: log,
		http:     &http.Client{Timeout: 10 * time.Minute},
		verifier: release.Default(),
	}
}

// selfPath ist der Pfad des laufenden Binaries.
func (u *Updater) selfPath() (string, error) {
	if u.self != "" {
		return u.self, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	// Über einen Symlink zeigte der Tausch sonst auf den Link statt auf die
	// Datei — und der Rollback liefe ins Leere.
	return filepath.EvalSymlinks(self)
}

// Release beschreibt eine Version aus dem Update-Kanal.
type Release struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
	// URL zeigt auf die Release-Seite. Notes trägt den Text selbst, damit das
	// Panel ihn anzeigen kann, ohne nach außen zu telefonieren.
	URL string `json:"url,omitempty"`
	// Assets sind nach "linux_amd64" / "linux_arm64" benannt.
	Assets map[string]ReleaseAsset `json:"assets"`
	// Units sind die systemd-Dateien, nach Dateinamen benannt. Ohne sie
	// bliebe ein Server mit neuen Programmen und alten Units zurück — eine
	// Drift, die erst auffällt, wenn eine Operation unerwartet scheitert.
	Units map[string]ReleaseAsset `json:"units,omitempty"`
}

type ReleaseAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Agent ist das passende volt-agent-Binary. Ohne dieses Feld bliebe der
	// Agent auf dem alten Stand, während das Panel schon neu ist — die beiden
	// sprechen dann irgendwann verschiedene Protokolle.
	Agent *ReleaseAsset `json:"agent,omitempty"`
}

// UpdateStatus ist die Antwort auf "gibt es etwas Neues?".
type UpdateStatus struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"`
	Notes     string    `json:"notes,omitempty"`
	URL       string    `json:"url,omitempty"`
	Channel   string    `json:"channel"`
	CheckedAt time.Time `json:"checked_at"`
	// Error steht drin, wenn der Kanal nicht erreichbar war. Das ist kein
	// Fehler der Anfrage: das Panel läuft weiter, es weiß nur gerade nicht,
	// ob es etwas Neues gibt — und das soll man sehen.
	Error string `json:"error,omitempty"`
}

const updateCacheTTL = time.Hour

// UpdateStatus fragt den Kanal, höchstens einmal je Stunde.
//
// force übergeht den Zwischenspeicher — für den Knopf "Jetzt prüfen", bei dem
// eine Antwort von vor 50 Minuten wie ein kaputter Knopf aussähe.
func (u *Updater) UpdateStatus(ctx context.Context, force bool) UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()

	if !force && u.cached != nil && time.Since(u.fetched) < updateCacheTTL {
		return *u.cached
	}

	status := UpdateStatus{
		Current:   version.Version,
		Channel:   u.cfg.UpdateChannel,
		CheckedAt: time.Now(),
	}

	rel, err := u.LatestRelease(ctx)
	switch {
	case err != nil:
		status.Error = err.Error()
		// Einen alten Stand behalten statt ihn wegzuwerfen: eine kurze
		// Störung soll nicht so aussehen, als gäbe es kein Update mehr.
		if u.cached != nil {
			status.Latest, status.Available = u.cached.Latest, u.cached.Available
			status.Notes, status.URL = u.cached.Notes, u.cached.URL
		}
	default:
		status.Latest, status.Notes, status.URL = rel.Version, rel.Notes, rel.URL
		status.Available = rel.Version != "" && !sameVersion(rel.Version, version.Version)
	}

	u.cached, u.fetched = &status, time.Now()
	return status
}

// sameVersion vergleicht zwei Versionsangaben.
//
// Der Fahrplan führt "0.1.4", ein aus dem Tag gebautes Binary kann "v0.1.4"
// melden. Ein Vergleich auf Zeichengleichheit meldete dann dauerhaft ein
// Update, das keines ist — und ein Hinweis, der nie weggeht, wird ignoriert.
func sameVersion(a, b string) bool {
	norm := func(v string) string {
		return strings.TrimPrefix(strings.TrimSpace(v), "v")
	}
	return norm(a) == norm(b)
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

	// Der Rumpf zuerst als Bytes: signiert ist die Datei, nicht das Ergebnis
	// des Parsens. Wer erst parst und dann prüft, prüft etwas anderes als das,
	// wonach er sich richtet.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("release-info lesen: %w", err)
	}
	if err := u.verifyManifest(ctx, body); err != nil {
		return nil, err
	}

	var rel Release
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("release-info unlesbar: %w", err)
	}
	if rel.Version == "" {
		return nil, errors.New("release-info enthält keine version")
	}
	return &rel, nil
}

// verifyManifest prüft die Signatur über latest.json.
//
// latest.json enthält die Prüfsummen aller Bestandteile. Wer die Datei signiert
// hat, hat damit auch die Binaries signiert — und nur so ist der
// Prüfsummenvergleich beim Herunterladen mehr als eine Prüfung gegen dieselbe
// Quelle. Vorher stand daneben, nur wer den Release signiert habe, kenne die
// Summe; das stimmte nicht, beide kamen von derselben Adresse.
func (u *Updater) verifyManifest(ctx context.Context, body []byte) error {
	if u.cfg.UpdateAllowUnsigned {
		u.log.Warn("update-signatur wird nicht geprüft",
			"grund", "update_allow_unsigned steht in der config.yaml")
		return nil
	}
	if !u.verifier.HasKey() {
		return fmt.Errorf("dieses binary wurde ohne release-schlüssel gebaut — "+
			"ein update ließe sich damit nicht prüfen. %s", updateUnsignedHinweis)
	}

	url := fmt.Sprintf("%s/%s/latest.json.sig",
		strings.TrimRight(u.cfg.UpdateBaseURL, "/"), u.cfg.UpdateChannel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "volt/"+version.Version)

	resp, err := u.http.Do(req)
	if err != nil {
		return fmt.Errorf("signatur nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("zu den release-angaben gibt es keine signatur (%s). %s",
			resp.Status, updateUnsignedHinweis)
	}
	sig, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return fmt.Errorf("signatur lesen: %w", err)
	}
	if err := u.verifier.Verify(body, string(sig)); err != nil {
		return fmt.Errorf("%w — das update wird nicht ausgeführt", err)
	}
	return nil
}

// updateUnsignedHinweis steht in jeder Meldung, die an der Signatur scheitert.
// Ohne ihn sucht jemand an der falschen Stelle.
const updateUnsignedHinweis = "Wer bewusst einen unsignierten Kanal betreibt, " +
	"setzt update_allow_unsigned: true in der config.yaml."

// Snapshot ist der Stand vor einem Update, auf den zurückgerollt werden kann.
type Snapshot struct {
	Dir        string
	BinaryPath string
	AgentPath  string // leer, wenn neben volt kein volt-agent lag
	UnitDir    string // Kopie der systemd-Units vor dem Update
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

	self, err := u.selfPath()
	if err != nil {
		return nil, fmt.Errorf("eigenen pfad ermitteln: %w", err)
	}
	if err := copyFile(self, snap.BinaryPath, 0o755); err != nil {
		return nil, fmt.Errorf("binary sichern: %w", err)
	}
	// Der Agent liegt neben dem Binary. Fehlt er, ist das kein Fehler: in der
	// Entwicklung läuft das Panel auch allein.
	if agent := agentPathNextTo(self); agent != "" {
		dst := filepath.Join(dir, "volt-agent")
		if err := copyFile(agent, dst, 0o755); err != nil {
			return nil, fmt.Errorf("agent sichern: %w", err)
		}
		snap.AgentPath = dst
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

	self, err := u.selfPath()
	if err != nil {
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

	// Der Agent muss zur selben Version gehören. Scheitert sein Tausch, wird
	// sofort zurückgerollt: ein neues Panel mit altem Agent ist ein Zustand,
	// in dem Operationen unvorhersehbar fehlschlagen.
	if agent := agentPathNextTo(self); agent != "" {
		switch {
		case asset.Agent == nil:
			u.log.Warn("release enthält kein agent-binary, der agent bleibt auf dem alten stand",
				"pfad", agent)
		default:
			agentTmp := agent + ".new"
			if err := u.download(ctx, *asset.Agent, agentTmp); err != nil {
				os.Remove(agentTmp)
				if rbErr := u.Rollback(ctx, snap); rbErr != nil {
					return fmt.Errorf("agent laden fehlgeschlagen (%w) UND rollback fehlgeschlagen: %v", err, rbErr)
				}
				return fmt.Errorf("agent laden fehlgeschlagen, stand wurde zurückgerollt: %w", err)
			}
			if err := os.Rename(agentTmp, agent); err != nil {
				os.Remove(agentTmp)
				if rbErr := u.Rollback(ctx, snap); rbErr != nil {
					return fmt.Errorf("agent tauschen fehlgeschlagen (%w) UND rollback fehlgeschlagen: %v", err, rbErr)
				}
				return fmt.Errorf("agent tauschen fehlgeschlagen, stand wurde zurückgerollt: %w", err)
			}
			u.log.Info("agent getauscht", "pfad", agent)
		}
	}

	// Units vor der Migration: schlägt hier etwas fehl, ist noch nichts an
	// der Datenbank geändert und der Rollback bleibt einfach.
	if _, err := u.syncUnits(ctx, rel, snap); err != nil {
		u.log.Error("units nicht aktualisiert, rolle zurück", "err", err)
		if rbErr := u.Rollback(ctx, snap); rbErr != nil {
			return fmt.Errorf("units fehlgeschlagen (%w) UND rollback fehlgeschlagen: %v", err, rbErr)
		}
		return fmt.Errorf("units fehlgeschlagen, stand wurde zurückgerollt: %w", err)
	}

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

	self, err := u.selfPath()
	if err != nil {
		return err
	}

	if err := copyFile(snap.BinaryPath, self+".rollback", 0o755); err != nil {
		return fmt.Errorf("altes binary bereitstellen: %w", err)
	}
	if err := os.Rename(self+".rollback", self); err != nil {
		return fmt.Errorf("altes binary zurücktauschen: %w", err)
	}

	if err := u.restoreUnits(snap); err != nil {
		u.log.Warn("units nicht zurückgespielt", "err", err)
	}

	if snap.AgentPath != "" {
		if agent := agentPathNextTo(self); agent != "" {
			if err := copyFile(snap.AgentPath, agent+".rollback", 0o755); err != nil {
				return fmt.Errorf("alten agent bereitstellen: %w", err)
			}
			if err := os.Rename(agent+".rollback", agent); err != nil {
				return fmt.Errorf("alten agent zurücktauschen: %w", err)
			}
		}
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

// agentPathNextTo liefert den Pfad des Agents neben dem übergebenen Binary,
// oder "", wenn dort keiner liegt.
//
// Beide Binaries im selben Verzeichnis zu erwarten ist keine Bequemlichkeit,
// sondern Voraussetzung: os.Rename arbeitet nur innerhalb eines Dateisystems,
// und nur ein Rename tauscht atomar.
func agentPathNextTo(self string) string {
	p := filepath.Join(filepath.Dir(self), "volt-agent")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
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
