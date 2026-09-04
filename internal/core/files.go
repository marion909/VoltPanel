package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// FileService ist der Dateimanager.
//
// Der Agent sperrt Dateizugriffe bereits auf /var/www und die anderen
// erlaubten Wurzeln ein. Das genügt für Multi-Tenant nicht: es würde jedem
// Kunden erlauben, im Verzeichnis eines anderen zu lesen. Deshalb gibt es hier
// ein zweites, engeres Gefängnis — je Site, gegen den Scope geprüft.
//
// Die API nimmt deshalb nie einen absoluten Pfad entgegen, sondern immer eine
// site_id plus einen relativen Pfad darin. Ein absoluter Pfad aus dem Browser
// wäre eine Einladung, ihn zu manipulieren.
type FileService struct {
	store *store.Store
	agent *agent.Client
	cfg   *config.Config
	quota *QuotaService
}

func NewFileService(st *store.Store, ag *agent.Client, cfg *config.Config) *FileService {
	return &FileService{
		store: st, agent: ag, cfg: cfg,
		quota: NewQuotaService(st, ag, cfg, nil),
	}
}

// maxEditableBytes begrenzt, was der Editor im Browser öffnet. Größere Dateien
// gibt es zum Herunterladen, aber nicht zum Bearbeiten.
const maxEditableBytes = 2 << 20 // 2 MiB

var errPathEscape = errors.New("pfad verlässt das site-verzeichnis")

// resolve übersetzt (site, relativer Pfad) in einen absoluten Pfad und prüft
// dabei sowohl den Tenant-Scope als auch die Pfadgrenzen.
func (s *FileService) resolve(ctx context.Context, sc store.Scope, siteID int64, rel string) (*store.Site, string, error) {
	// Der Scope entscheidet, ob die Site überhaupt sichtbar ist — eine fremde
	// site_id liefert hier ErrNotFound und kommt nie bis zum Agent.
	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return nil, "", err
	}

	abs, err := joinInside(site.RootPath, rel)
	if err != nil {
		return nil, "", err
	}
	return site, abs, nil
}

// joinInside hängt einen relativen Pfad an eine Wurzel und stellt sicher, dass
// er darin bleibt.
//
// Ein Pfad, der aus der Wurzel herausführen will, wird abgelehnt — nicht
// stillschweigend hineinnormalisiert. Beides wäre sicher, aber
// "../andere-site/config.php" als "andere-site/config.php" im eigenen
// Verzeichnis anzulegen wäre für den Benutzer überraschend und würde einen
// Angriffsversuch unsichtbar machen. Lieber laut scheitern.
//
// Innerhalb der Wurzel bleibt ".." erlaubt: "public/../public/x" ist eine
// gewöhnliche, harmlose Schreibweise.
func joinInside(root, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("%w: nullbyte im pfad", errPathEscape)
	}

	// Ein führender Schrägstrich bedeutet hier "ab der Wurzel der Site",
	// nicht "ab der Wurzel des Dateisystems".
	clean := filepath.Clean(strings.TrimPrefix(filepath.ToSlash(rel), "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: %q", errPathEscape, rel)
	}

	root = filepath.Clean(root)
	abs := filepath.Join(root, clean)

	// Gürtel zum Hosenträger: nach der Prüfung oben kann das nicht mehr
	// zutreffen, aber es kostet nichts.
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", errPathEscape, rel)
	}

	// Die Prüfung oben ist rein lexikalisch. Ein Symlink, den der Mandant
	// selbst innerhalb seiner eigenen Site anlegt (kein besonderes Privileg
	// nötig), kann auf das Verzeichnis einer fremden Site zeigen — der Agent
	// löst Symlinks zwar auf, prüft das Ergebnis danach aber nur gegen die
	// breite, mandantenübergreifende SitesDir-Wurzel, nicht gegen diese
	// einzelne Site. Deshalb hier zusätzlich beide Seiten symlink-fest
	// auflösen (so weit sie existieren) und erneut gegen die Site-Wurzel
	// prüfen, bevor der Pfad den Agent überhaupt erreicht.
	realRoot, err := resolveExistingPrefix(root)
	if err != nil {
		return "", fmt.Errorf("%w: site-wurzel nicht auflösbar", errPathEscape)
	}
	realAbs, err := resolveExistingPrefix(abs)
	if err != nil {
		return "", fmt.Errorf("%w: %q nicht auflösbar", errPathEscape, rel)
	}
	if realAbs != realRoot && !strings.HasPrefix(realAbs, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q verlässt die site-wurzel über einen symlink", errPathEscape, rel)
	}
	return abs, nil
}

// resolveExistingPrefix löst Symlinks entlang von path auf, so weit die
// Segmente bereits existieren. Der noch nicht angelegte Teil (z. B. beim
// Schreiben einer neuen Datei) wird unverändert angehängt — er kann selbst
// noch kein Symlink sein, weil es ihn noch nicht gibt.
func resolveExistingPrefix(path string) (string, error) {
	var missing []string
	cur := path
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("pfad %q nicht auflösbar: %w", path, err)
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

// relativeTo bildet einen absoluten Pfad wieder auf die Site-relative Form ab,
// damit das Frontend nie absolute Serverpfade zu sehen bekommt.
func relativeTo(root, abs string) string {
	rel, err := filepath.Rel(filepath.Clean(root), abs)
	if err != nil || rel == "." {
		return ""
	}
	return rel
}

// Entry ist ein Eintrag, wie ihn der Dateimanager anzeigt.
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"` // relativ zur Site
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	IsDir   bool   `json:"is_dir"`
	ModTime int64  `json:"mod_time"`
	Owner   string `json:"owner"`
	Group   string `json:"group"`
}

func (s *FileService) List(ctx context.Context, sc store.Scope, siteID int64, rel string) ([]Entry, error) {
	site, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return nil, err
	}

	raw, err := s.agent.ListDir(ctx, abs)
	if err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(raw))
	for _, e := range raw {
		out = append(out, Entry{
			Name: e.Name, Path: relativeTo(site.RootPath, filepath.Join(abs, e.Name)),
			Size: e.Size, Mode: e.Mode, IsDir: e.IsDir,
			ModTime: e.ModTime, Owner: e.Owner, Group: e.Group,
		})
	}
	return out, nil
}

func (s *FileService) Read(ctx context.Context, sc store.Scope, siteID int64, rel string) (string, error) {
	_, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return "", err
	}

	info, err := s.agent.Stat(ctx, abs)
	if err != nil {
		return "", err
	}
	if info.IsDir {
		return "", fmt.Errorf("%s ist ein verzeichnis", rel)
	}
	if info.Size > maxEditableBytes {
		return "", fmt.Errorf("datei ist %d bytes groß — der editor öffnet bis %d",
			info.Size, int64(maxEditableBytes))
	}
	return s.agent.ReadFile(ctx, abs)
}

// Write legt eine Datei an oder überschreibt sie. Der Eigentümer ist immer der
// Systembenutzer der Site — sonst gehörten hochgeladene Dateien root und der
// PHP-Prozess der Site könnte sie nicht mehr ändern.
func (s *FileService) Write(ctx context.Context, sc store.Scope, siteID int64, rel, content string) error {
	site, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return err
	}
	return s.agent.WriteFile(ctx, abs, content, 0o644, site.SystemUser)
}

func (s *FileService) Mkdir(ctx context.Context, sc store.Scope, siteID int64, rel string) error {
	site, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return err
	}
	return s.agent.Mkdir(ctx, abs, 0o750, site.SystemUser)
}

// Remove löscht eine Datei oder einen Baum. Das Wurzelverzeichnis der Site
// selbst ist ausgenommen — dafür gibt es das Entfernen der Site.
func (s *FileService) Remove(ctx context.Context, sc store.Scope, siteID int64, rel string, recursive bool) error {
	site, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return err
	}
	if abs == filepath.Clean(site.RootPath) {
		return errors.New("das wurzelverzeichnis der site lässt sich hier nicht löschen")
	}
	return s.agent.RemovePath(ctx, abs, recursive)
}

func (s *FileService) Move(ctx context.Context, sc store.Scope, siteID int64, from, to string) error {
	site, absFrom, err := s.resolve(ctx, sc, siteID, from)
	if err != nil {
		return err
	}
	// Auch das Ziel muss in derselben Site liegen: Verschieben ist sonst der
	// bequemste Weg, eine Datei in ein fremdes Verzeichnis zu bekommen.
	absTo, err := joinInside(site.RootPath, to)
	if err != nil {
		return err
	}
	return s.agent.MovePath(ctx, absFrom, absTo, false)
}

func (s *FileService) Copy(ctx context.Context, sc store.Scope, siteID int64, from, to string) error {
	site, absFrom, err := s.resolve(ctx, sc, siteID, from)
	if err != nil {
		return err
	}
	absTo, err := joinInside(site.RootPath, to)
	if err != nil {
		return err
	}
	if err := s.agent.CopyPath(ctx, absFrom, absTo, false); err != nil {
		return err
	}
	return s.agent.Call(ctx, agent.OpFileChown,
		agent.FileChownParams{Path: absTo, Owner: site.SystemUser, Recursive: true}, nil)
}

func (s *FileService) Chmod(ctx context.Context, sc store.Scope, siteID int64, rel string, mode uint32, recursive bool) error {
	_, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return err
	}
	return s.agent.Chmod(ctx, abs, mode, recursive)
}

// Archive packt ausgewählte Einträge in ein Archiv innerhalb derselben Site.
func (s *FileService) Archive(ctx context.Context, sc store.Scope, siteID int64, sources []string, dest string) (int64, error) {
	if len(sources) == 0 {
		return 0, errors.New("keine datei ausgewählt")
	}
	site, err := s.store.GetSite(ctx, sc, siteID)
	if err != nil {
		return 0, err
	}

	abs := make([]string, 0, len(sources))
	for _, src := range sources {
		p, err := joinInside(site.RootPath, src)
		if err != nil {
			return 0, err
		}
		abs = append(abs, p)
	}
	absDest, err := joinInside(site.RootPath, dest)
	if err != nil {
		return 0, err
	}
	return s.agent.Archive(ctx, abs, absDest, site.SystemUser)
}

func (s *FileService) Extract(ctx context.Context, sc store.Scope, siteID int64, archive, dest string) (int, error) {
	site, absArchive, err := s.resolve(ctx, sc, siteID, archive)
	if err != nil {
		return 0, err
	}
	absDest, err := joinInside(site.RootPath, dest)
	if err != nil {
		return 0, err
	}
	return s.agent.Extract(ctx, absArchive, absDest, site.SystemUser)
}

// Stat liefert die Angaben zu einem einzelnen Eintrag.
func (s *FileService) Stat(ctx context.Context, sc store.Scope, siteID int64, rel string) (*Entry, error) {
	site, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return nil, err
	}

	info, err := s.agent.Stat(ctx, abs)
	if err != nil {
		return nil, err
	}
	return &Entry{
		Name: info.Name, Path: relativeTo(site.RootPath, info.Path),
		Size: info.Size, Mode: info.Mode, IsDir: info.IsDir,
		ModTime: info.ModTime, Owner: info.Owner, Group: info.Group,
	}, nil
}

// Download streamt eine Datei blockweise zum Aufrufer.
//
// Der Umweg über Blöcke ist nötig, weil das Socket-Protokoll eine Anfrage auf
// 8 MiB deckelt. So bleibt der Speicherbedarf konstant, egal wie groß die
// Datei ist.
func (s *FileService) Download(ctx context.Context, sc store.Scope, siteID int64, rel string, w io.Writer) (int64, error) {
	_, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return 0, err
	}

	info, err := s.agent.Stat(ctx, abs)
	if err != nil {
		return 0, err
	}
	if info.IsDir {
		return 0, fmt.Errorf("%s ist ein verzeichnis — bitte erst archivieren", rel)
	}

	return streamFromAgent(ctx, s.agent, abs, w)
}

// streamFromAgent holt eine Datei blockweise über den Socket und schreibt sie
// weiter. Auch der Dump einer Datenbank nimmt diesen Weg: er gehört root und
// ist für den Web-Prozess sonst nicht lesbar.
func streamFromAgent(ctx context.Context, ag *agent.Client, abs string, w io.Writer) (int64, error) {
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}

		chunk, err := ag.ReadChunk(ctx, abs, written, agent.ChunkSize)
		if err != nil {
			return written, err
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			return written, fmt.Errorf("block bei versatz %d ist unlesbar: %w", written, err)
		}

		if len(data) > 0 {
			n, err := w.Write(data)
			written += int64(n)
			if err != nil {
				return written, err
			}
		}
		if chunk.EOF || len(data) == 0 {
			return written, nil
		}
	}
}

// UploadOptions trennt die beiden Größen, die beim Upload eine Rolle spielen.
type UploadOptions struct {
	// Size ist die angekündigte Größe. Sie geht in die Quota-Prüfung, bevor
	// ein Byte fließt. 0 bedeutet "unbekannt" — dann greift nur MaxBytes.
	Size int64
	// MaxBytes ist die harte Obergrenze beim Schreiben, unabhängig davon, was
	// angekündigt wurde. 0 bedeutet unbegrenzt.
	MaxBytes int64
}

// Upload schreibt einen Datenstrom blockweise in eine Datei der Site.
//
// Wird MaxBytes überschritten, bleibt keine halbe Datei zurück.
func (s *FileService) Upload(ctx context.Context, sc store.Scope, siteID int64, rel string, r io.Reader, opts UploadOptions) (int64, error) {
	site, abs, err := s.resolve(ctx, sc, siteID, rel)
	if err != nil {
		return 0, err
	}

	// Gegen die Quota des Tenants prüfen, bevor überhaupt ein Byte fließt.
	// Grundlage ist der Stand der letzten Messung — zwischen zwei Messungen
	// lässt sich eine Quota also knapp überschreiten. Das ist der Preis dafür,
	// dass nicht jeder Upload einen Verzeichnisdurchlauf auslöst.
	if err := s.quota.CheckDisk(ctx, sc, site.TenantID, opts.Size); err != nil {
		return 0, err
	}

	buf := make([]byte, agent.ChunkSize)
	var written int64
	first := true

	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}

		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			if opts.MaxBytes > 0 && written+int64(n) > opts.MaxBytes {
				_ = s.agent.RemovePath(ctx, abs, false)
				return 0, fmt.Errorf("upload überschreitet das limit von %d bytes", opts.MaxBytes)
			}
			// truncate nur beim ersten Block: er ersetzt eine eventuell schon
			// vorhandene Datei, die folgenden hängen an.
			if err := s.agent.WriteChunk(ctx, abs, written, buf[:n], site.SystemUser, first); err != nil {
				return written, err
			}
			written += int64(n)
			first = false
		}

		switch readErr {
		case nil:
			continue
		case io.EOF, io.ErrUnexpectedEOF:
			// Eine leere Datei erzeugt keinen einzigen Block — sie muss
			// trotzdem angelegt werden.
			if first {
				if err := s.agent.WriteChunk(ctx, abs, 0, nil, site.SystemUser, true); err != nil {
					return 0, err
				}
			}
			return written, nil
		default:
			return written, readErr
		}
	}
}
