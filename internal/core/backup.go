package core

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/authn"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// BackupService sichert und stellt wieder her.
//
// Die Archive entstehen mit den Bordmitteln von Go (archive/tar), nicht über
// einen tar-Aufruf — Prinzip 3 der Roadmap: kein User-Input geht in eine Shell.
type BackupService struct {
	cfg   *config.Config
	store *store.Store
	log   *slog.Logger
	// secrets entschlüsselt die Zugangsdaten der Backup-Ziele. Ohne sie
	// funktioniert alles ausser dem Hochladen — die CLI-Befehle, die nur
	// lokal arbeiten, kommen deshalb auch mit nil durch.
	secrets *authn.SecretBox
}

func NewBackupService(cfg *config.Config, st *store.Store, log *slog.Logger,
	secrets *authn.SecretBox) *BackupService {

	if log == nil {
		log = slog.Default()
	}
	return &BackupService{cfg: cfg, store: st, log: log, secrets: secrets}
}

// CreateOptions steuert, was in ein Backup wandert.
type CreateOptions struct {
	IncludeConfig bool
	SiteDomains   []string // leer = keine Site-Dateien
	TenantID      int64
}

// Result beschreibt ein fertiges Backup.
type Result struct {
	Path      string
	SizeBytes int64
	Checksum  string
	Duration  time.Duration
}

// Create schreibt ein tar.gz mit Datenbank, Konfiguration und Site-Dateien.
func (s *BackupService) Create(ctx context.Context, opts CreateOptions) (*Result, error) {
	start := time.Now()
	stamp := time.Now().UTC().Format("20060102-150405")
	path := filepath.Join(s.cfg.BackupDir, fmt.Sprintf("volt-%s.tar.gz", stamp))

	if err := os.MkdirAll(s.cfg.BackupDir, 0o750); err != nil {
		return nil, fmt.Errorf("backup-verzeichnis: %w", err)
	}

	// Die Datenbank wird erst konsistent herauskopiert; ein laufender
	// Schreibvorgang würde sonst ein unbrauchbares Archiv erzeugen.
	tmpDB := filepath.Join(s.cfg.BackupDir, ".volt-"+stamp+".db")
	if err := s.store.Backup(ctx, tmpDB); err != nil {
		return nil, err
	}
	defer os.Remove(tmpDB)

	aw, err := newArchiveWriter(path)
	if err != nil {
		return nil, err
	}
	defer aw.Abort()
	tw := aw.tw

	if err := addFile(tw, tmpDB, "volt.db"); err != nil {
		return nil, fmt.Errorf("datenbank sichern: %w", err)
	}
	if opts.IncludeConfig {
		if err := addTree(ctx, tw, s.cfg.ConfigDir, "etc/volt"); err != nil {
			return nil, fmt.Errorf("konfiguration sichern: %w", err)
		}
	}
	for _, domain := range opts.SiteDomains {
		if !store.ValidDomain(domain) {
			return nil, fmt.Errorf("%q ist kein gültiger domainname", domain)
		}
		src := filepath.Join(s.cfg.SitesDir, domain)
		if err := addTree(ctx, tw, src, filepath.Join("sites", domain)); err != nil {
			return nil, fmt.Errorf("site %s sichern: %w", domain, err)
		}
	}

	size, checksum, err := aw.Close()
	if err != nil {
		return nil, err
	}
	res := &Result{Path: path, SizeBytes: size, Checksum: checksum, Duration: time.Since(start)}

	// Der Eintrag macht das Backup im Panel sichtbar.
	finished := time.Now().Unix()
	started := start.Unix()
	if err := s.store.CreateBackup(ctx, store.SystemScope(), &store.Backup{
		TenantID: opts.TenantID, Kind: "full", Destination: "local",
		Path: path, SizeBytes: res.SizeBytes, Checksum: res.Checksum,
		Status: "ok", StartedAt: &started, FinishedAt: &finished,
	}); err != nil {
		s.log.Warn("backup-eintrag nicht gespeichert", "err", err)
	}
	return res, nil
}

// Restore spielt die Datenbank aus einem Archiv zurück.
//
// Die Site-Dateien werden bewusst nicht automatisch überschrieben — das wäre
// unumkehrbar. Sie liegen im Archiv und lassen sich gezielt herausholen.
func (s *BackupService) Restore(ctx context.Context, archivePath string) error {
	// Vor dem Überschreiben eine Kopie des aktuellen Stands ziehen: ein
	// Restore aus dem falschen Archiv soll nicht der letzte Schritt sein.
	safety := s.cfg.DBPath + ".vor-restore"
	if err := s.store.Backup(ctx, safety); err != nil {
		return fmt.Errorf("sicherheitskopie vor dem restore: %w", err)
	}
	s.log.Info("sicherheitskopie angelegt", "pfad", safety)

	found := false
	err := eachEntry(archivePath, func(header *tar.Header, tr io.Reader) error {
		if header.Name != "volt.db" {
			return nil
		}
		found = true

		// Eine nicht sauber geschlossene DB (offene WAL-Segmente, hängende
		// Transaktion) darf nicht überschrieben werden — genau die
		// Sicherheitskopie oben soll ja vor einem inkonsistenten Zustand
		// schützen, nicht selbst einen erzeugen.
		if err := s.store.Close(); err != nil {
			return fmt.Errorf("datenbank nicht sauber geschlossen, restore abgebrochen: %w", err)
		}
		// Über eine temporäre Datei plus os.Rename statt direkt an s.cfg.DBPath
		// zu schreiben: ein Prozessabbruch mitten im Kopieren (OOM, Stromausfall,
		// volle Platte) hinterließe sonst eine abgeschnittene volt.db genau an
		// dem Pfad, den jeder künftige store.Open verwendet — und genau das soll
		// die zuvor gezogene Sicherheitskopie eigentlich verhindern helfen.
		tmp := s.cfg.DBPath + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		// Begrenzt auf die im Header angegebene Größe — ein manipuliertes
		// Archiv soll die Platte nicht füllen.
		if _, err := io.Copy(out, io.LimitReader(tr, header.Size)); err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
		if err := out.Sync(); err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
		if err := out.Close(); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, s.cfg.DBPath); err != nil {
			os.Remove(tmp)
			return err
		}
		// Die alten WAL-Dateien gehören nicht mehr zur zurückgespielten Datenbank.
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Remove(s.cfg.DBPath + suffix)
		}
		return errStopEachEntry
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("archiv %s enthält keine volt.db", archivePath)
	}
	s.log.Info("datenbank zurückgespielt", "aus", archivePath)
	return nil
}

// archiveWriter bündelt das wiederkehrende "Datei mit 0600 öffnen,
// sha256-Hasher + gzip.Writer + tar.Writer davorschalten" von Create
// (hier) und ExportTenant (tenant_export.go).
//
// Der Aufrufer befüllt tw, ruft am Ende Close() für Größe und Prüfsumme —
// und defer Abort() direkt nach newArchiveWriter, damit ein früher
// Rückgabefehler die Datei trotzdem schließt. Abort ist nach einem
// erfolgreichen Close ein No-op.
type archiveWriter struct {
	tw *tar.Writer

	f      *os.File
	gz     *gzip.Writer
	hasher hash.Hash
	closed bool
}

// newArchiveWriter öffnet path. 0600, weil ein Backup oder ein
// Mandanten-Export Passwort-Hashes, Secrets oder Datenbankauszüge enthält.
func newArchiveWriter(path string) (*archiveWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	hasher := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, hasher))
	return &archiveWriter{tw: tar.NewWriter(gz), f: f, gz: gz, hasher: hasher}, nil
}

// Abort schließt die Datei, falls Close() nie erfolgreich durchlief — für
// den defer direkt nach newArchiveWriter.
func (a *archiveWriter) Abort() {
	if !a.closed {
		a.f.Close()
	}
}

// Close schreibt tar und gzip fertig, synchronisiert und liefert Größe und
// Prüfsumme des Archivs. Reihenfolge zählt: tar vor gzip, sonst fehlt der
// Abschluss im Archiv und es ist beim Entpacken beschädigt.
func (a *archiveWriter) Close() (size int64, checksum string, err error) {
	a.closed = true
	defer a.f.Close()
	if err := a.tw.Close(); err != nil {
		return 0, "", err
	}
	if err := a.gz.Close(); err != nil {
		return 0, "", err
	}
	if err := a.f.Sync(); err != nil {
		return 0, "", err
	}
	info, err := a.f.Stat()
	if err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(a.hasher.Sum(nil)), nil
}

func addFile(tw *tar.Writer, src, name string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = name

	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(tw, f)
	return err
}

// addTree packt ein Verzeichnis rekursiv. Symlinks werden als Symlink
// gespeichert, nicht verfolgt — sonst landet ein Link auf / im Archiv.
func addTree(ctx context.Context, tw *tar.Writer, src, prefix string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		name := filepath.Join(prefix, rel)

		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = name

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			// Eine einzelne unlesbare Datei darf das ganze Backup nicht kippen.
			if os.IsPermission(err) {
				return nil
			}
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}

// ListArchives liefert die lokal vorhandenen Backups, neueste zuerst.
func (s *BackupService) ListArchives() ([]os.FileInfo, error) {
	entries, err := os.ReadDir(s.cfg.BackupDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	out := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		if info, err := e.Info(); err == nil {
			out = append(out, info)
		}
	}
	return out, nil
}
