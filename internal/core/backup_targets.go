package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marion909/voltpanel/internal/store"
	"github.com/marion909/voltpanel/internal/transfer"
)

// Backup-Ziele: Ablageorte ausserhalb dieses Servers.
//
// Ein Backup, das neben dem Original liegt, überlebt genau die Fehler, die
// ohnehin niemandem weh tun — ein versehentliches DROP TABLE. Es überlebt nicht
// den Ausfall der Platte, nicht den gelöschten Server und nicht den
// Verschlüsselungstrojaner, der als erstes nach Backups sucht.

const (
	// Grosszügig: ein Archiv mit Site-Dateien kann Gigabyte haben, und eine
	// Leitung mit zehn Megabit braucht dafür seine Zeit.
	uploadTimeout = 2 * time.Hour
	probeTimeout  = 30 * time.Second
)

// TargetInput ist, was aus der Oberfläche kommt.
type TargetInput struct {
	Name     string
	Kind     string
	Endpoint string
	Region   string
	Bucket   string
	// Secret ist leer, wenn es unverändert bleiben soll. Das ist der
	// Unterschied zwischen "kein Geheimnis" und "nicht angefasst", und ohne
	// ihn löschte jedes Speichern des Formulars die Zugangsdaten.
	Secret     string
	Username   string
	BasePath   string
	Host       string
	Port       int
	UseTLS     bool
	SkipVerify bool
	PathStyle  bool
	Enabled    bool
	TenantID   int64
}

// b2Endpoint ist das Muster von Backblaze. Der Kunde kennt seine Region als
// "eu-central-003" und muss den Host nicht auswendig können.
func b2Endpoint(region string) string {
	return "s3." + region + ".backblazeb2.com"
}

// CreateTarget legt ein Ziel an.
func (s *BackupService) CreateTarget(ctx context.Context, sc store.Scope,
	in TargetInput) (*store.BackupTarget, error) {

	t := &store.BackupTarget{TenantID: in.TenantID}
	if err := s.apply(t, in, true); err != nil {
		return nil, err
	}
	if err := s.store.CreateBackupTarget(ctx, sc, t); err != nil {
		return nil, err
	}
	t.SecretEnc, t.HasSecret = "", true
	return t, nil
}

// UpdateTarget ändert ein Ziel. Ein leeres Secret lässt das bestehende stehen.
func (s *BackupService) UpdateTarget(ctx context.Context, sc store.Scope, id int64,
	in TargetInput) (*store.BackupTarget, error) {

	t, err := s.store.GetBackupTarget(ctx, sc, id)
	if err != nil {
		return nil, err
	}
	if err := s.apply(t, in, false); err != nil {
		return nil, err
	}
	if err := s.store.UpdateBackupTarget(ctx, sc, t); err != nil {
		return nil, err
	}
	t.SecretEnc = ""
	return t, nil
}

// apply überträgt die Eingabe auf das Ziel und verschlüsselt das Geheimnis.
func (s *BackupService) apply(t *store.BackupTarget, in TargetInput, neu bool) error {
	t.Name, t.Kind = in.Name, in.Kind
	t.Region, t.Bucket, t.PathStyle = in.Region, in.Bucket, in.PathStyle
	t.Host, t.Port, t.UseTLS, t.SkipVerify = in.Host, in.Port, in.UseTLS, in.SkipVerify
	t.Username, t.BasePath, t.Enabled = in.Username, in.BasePath, in.Enabled

	t.Endpoint = in.Endpoint
	if in.Kind == "b2" && in.Endpoint == "" && in.Region != "" {
		t.Endpoint = b2Endpoint(in.Region)
	}

	if in.Secret != "" {
		if s.secrets == nil {
			return errors.New("der schlüssel für die verschlüsselung fehlt")
		}
		enc, err := s.secrets.Encrypt(in.Secret)
		if err != nil {
			return err
		}
		t.SecretEnc = enc
	} else if neu && in.Kind != "ftp" {
		// Ein FTP-Zugang ohne Passwort ist denkbar (Schlüssel, anonym); ein
		// S3-Zugang ohne Geheimnis kann nicht funktionieren.
		return errors.New("ohne geheimnis lässt sich keine anfrage signieren")
	}
	return nil
}

// TestTarget prüft ein Ziel, ohne etwas abzulegen.
func (s *BackupService) TestTarget(ctx context.Context, sc store.Scope, id int64) error {
	t, err := s.store.GetBackupTarget(ctx, sc, id)
	if err != nil {
		return err
	}
	err = s.probe(ctx, t)
	s.mark(ctx, sc, t, err)
	return err
}

func (s *BackupService) probe(ctx context.Context, t *store.BackupTarget) error {
	switch t.Kind {
	case "s3", "b2":
		cfg, err := s.s3Config(t)
		if err != nil {
			return err
		}
		return transfer.Probe(ctx, cfg, probeTimeout)
	case "ftp":
		cfg, err := s.ftpConfig(t)
		if err != nil {
			return err
		}
		c, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		return transfer.ProbeFTP(c, cfg)
	}
	return fmt.Errorf("die art %q ist unbekannt", t.Kind)
}

// UploadResult sagt, was wo gelandet ist.
type UploadResult struct {
	Target     string `json:"target"`
	RemotePath string `json:"remote_path"`
	SizeBytes  int64  `json:"size_bytes"`
	DurationMS int64  `json:"duration_ms"`
}

// Upload bringt ein vorhandenes Archiv an ein Ziel.
//
// Der lokale Pfad kommt nicht aus der Anfrage: der Aufrufer nennt den
// Dateinamen, und der wird gegen das Backup-Verzeichnis gehalten. Andernfalls
// wäre "lade dieses Backup hoch" ein Weg, jede Datei des Servers in einen
// fremden Bucket zu schieben — /etc/shadow zum Beispiel.
func (s *BackupService) Upload(ctx context.Context, sc store.Scope, targetID int64,
	filename string) (*UploadResult, error) {

	t, err := s.store.GetBackupTarget(ctx, sc, targetID)
	if err != nil {
		return nil, err
	}
	if !t.Enabled {
		return nil, fmt.Errorf("das ziel %q ist abgeschaltet", t.Name)
	}

	local, err := s.archivePath(filename)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(local)
	if err != nil {
		return nil, fmt.Errorf("das archiv %q gibt es nicht", filepath.Base(local))
	}

	start := time.Now()
	remote, err := s.send(ctx, t, local, filename)
	s.mark(ctx, sc, t, err)
	if err != nil {
		return nil, err
	}

	// Der Eintrag macht sichtbar, dass es das Archiv auch anderswo gibt. Ohne
	// ihn stünde im Panel weiter nur die lokale Kopie.
	finished := time.Now().Unix()
	started := start.Unix()
	if err := s.store.CreateBackup(ctx, store.SystemScope(), &store.Backup{
		TenantID: t.TenantID, Kind: "full", Destination: t.Kind,
		Path: local, SizeBytes: info.Size(), Status: "ok",
		StartedAt: &started, FinishedAt: &finished,
		TargetID: &t.ID, RemotePath: remote,
	}); err != nil {
		s.log.Warn("backup-eintrag nicht gespeichert", "err", err)
	}

	return &UploadResult{
		Target: t.Name, RemotePath: remote, SizeBytes: info.Size(),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func (s *BackupService) send(ctx context.Context, t *store.BackupTarget,
	local, filename string) (string, error) {

	name := transfer.ObjectName(time.Now(), filename)

	switch t.Kind {
	case "s3", "b2":
		cfg, err := s.s3Config(t)
		if err != nil {
			return "", err
		}
		return transfer.PutFile(ctx, cfg, local, name, uploadTimeout)
	case "ftp":
		cfg, err := s.ftpConfig(t)
		if err != nil {
			return "", err
		}
		c, cancel := context.WithTimeout(ctx, uploadTimeout)
		defer cancel()
		return transfer.PutFileFTP(c, cfg, local, name)
	}
	return "", fmt.Errorf("die art %q ist unbekannt", t.Kind)
}

// archivePath macht aus einem Dateinamen einen Pfad im Backup-Verzeichnis.
//
// filepath.Base wirft jeden Verzeichnisanteil weg — "../../etc/shadow" wird zu
// "shadow", und das gibt es dort nicht. Danach folgt trotzdem die Prüfung auf
// das Präfix: eine einzelne Massnahme, die man beim Lesen für ausreichend hält,
// ist die, die beim nächsten Umbau still wegfällt.
func (s *BackupService) archivePath(filename string) (string, error) {
	base := filepath.Base(filename)
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return "", fmt.Errorf("%q ist kein dateiname", filename)
	}
	if filepath.Ext(base) != ".gz" {
		return "", fmt.Errorf("%q ist kein archiv", base)
	}

	full := filepath.Join(s.cfg.BackupDir, base)
	root := filepath.Clean(s.cfg.BackupDir) + string(filepath.Separator)
	if !strings.HasPrefix(full, root) {
		return "", fmt.Errorf("%q liegt nicht im backup-verzeichnis", base)
	}
	return full, nil
}

func (s *BackupService) s3Config(t *store.BackupTarget) (transfer.S3Config, error) {
	secret, err := s.secret(t)
	if err != nil {
		return transfer.S3Config{}, err
	}
	return transfer.S3Config{
		Endpoint: t.Endpoint, Region: t.Region, Bucket: t.Bucket,
		Prefix: t.BasePath, AccessKey: t.Username, Secret: secret,
		PathStyle: t.PathStyle,
	}, nil
}

func (s *BackupService) ftpConfig(t *store.BackupTarget) (transfer.FTPConfig, error) {
	secret, err := s.secret(t)
	if err != nil {
		return transfer.FTPConfig{}, err
	}
	return transfer.FTPConfig{
		Host: t.Host, Port: t.Port, User: t.Username, Pass: secret,
		BaseDir: t.BasePath, TLS: t.UseTLS, InsecureSkipVerify: t.SkipVerify,
	}, nil
}

func (s *BackupService) secret(t *store.BackupTarget) (string, error) {
	if t.SecretEnc == "" {
		return "", nil
	}
	if s.secrets == nil {
		return "", errors.New("der schlüssel für die verschlüsselung fehlt")
	}
	return s.secrets.Decrypt(t.SecretEnc)
}

// mark hält fest, wie es ausging — auch der Erfolg, denn "seit drei Wochen
// nichts mehr" ist die Auskunft, die bei Backups zählt.
func (s *BackupService) mark(ctx context.Context, sc store.Scope, t *store.BackupTarget, err error) {
	var text string
	if err != nil {
		text = err.Error()
	}
	// Eigener Kontext: läuft der Upload in einen Timeout, ist der übergebene
	// abgelaufen — und dann bliebe ausgerechnet der Fehler ungespeichert.
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.store.MarkBackupTarget(c, sc, t.ID, text); err != nil {
		s.log.Warn("zustand des backup-ziels nicht gespeichert", "ziel", t.Name, "err", err)
	}
}
