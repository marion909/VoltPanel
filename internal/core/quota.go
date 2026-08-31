package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// QuotaService misst den Verbrauch eines Tenants und prüft ihn gegen sein
// Hosting-Paket.
//
// Die Grenzen wirken auf Anwendungsebene: eine Aktion, die eine Grenze reißen
// würde, wird abgelehnt. Eine echte Dateisystem-Quota (XFS/ext4 Project Quota)
// wäre stärker — sie würde auch einen Prozess bremsen, der am Panel vorbei
// schreibt — braucht aber Mount-Optionen und Werkzeuge, die nicht überall da
// sind. Siehe docs/stand.md.
type QuotaService struct {
	store *store.Store
	agent *agent.Client
	cfg   *config.Config
	log   *slog.Logger
}

func NewQuotaService(st *store.Store, ag *agent.Client, cfg *config.Config, log *slog.Logger) *QuotaService {
	if log == nil {
		log = slog.Default()
	}
	return &QuotaService{store: st, agent: ag, cfg: cfg, log: log}
}

// Resource benennt, was begrenzt wird.
type Resource string

const (
	ResourceSites     Resource = "sites"
	ResourceDatabases Resource = "databases"
	ResourceCronjobs  Resource = "cronjobs"
	ResourceFTP       Resource = "ftp"
	ResourceDisk      Resource = "disk"
	ResourceTraffic   Resource = "traffic"
)

var resourceLabels = map[Resource]string{
	ResourceSites:     "websites",
	ResourceDatabases: "datenbanken",
	ResourceCronjobs:  "cronjobs",
	ResourceFTP:       "ftp-zugänge",
	ResourceDisk:      "speicherplatz",
	ResourceTraffic:   "traffic",
}

// QuotaEntry ist eine einzelne Zeile der Quota-Übersicht.
type QuotaEntry struct {
	Resource Resource `json:"resource"`
	Used     int64    `json:"used"`
	Limit    int64    `json:"limit"` // 0 = unbegrenzt
	// Percent ist -1, wenn es keine Grenze gibt — sonst zeigte die Oberfläche
	// überall 0 % an und würde Knappheit suggerieren, wo keine ist.
	Percent float64 `json:"percent"`
	Bytes   bool    `json:"bytes"` // Anzeige als Größe statt als Anzahl
}

// QuotaStatus ist die vollständige Quota-Übersicht eines Tenants.
type QuotaStatus struct {
	TenantID int64              `json:"tenant_id"`
	PlanID   *int64             `json:"plan_id"`
	PlanName string             `json:"plan_name"`
	Entries  []QuotaEntry       `json:"entries"`
	Usage    *store.TenantUsage `json:"usage"`
}

// ErrQuotaExceeded wird von den Check-Methoden zurückgegeben.
var ErrQuotaExceeded = errors.New("quota erreicht")

// Status liefert Verbrauch und Grenzen eines Tenants.
func (s *QuotaService) Status(ctx context.Context, sc store.Scope, tenantID int64) (*QuotaStatus, error) {
	usage, err := s.store.UsageForTenant(ctx, sc, tenantID)
	if err != nil {
		return nil, err
	}

	out := &QuotaStatus{TenantID: tenantID, Usage: usage}
	plan, err := s.store.PlanForTenant(ctx, sc, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		// Kein Paket zugeordnet: alles unbegrenzt.
		plan = &store.Plan{Name: "ohne Paket"}
	} else if err != nil {
		return nil, err
	} else {
		out.PlanID = &plan.ID
	}
	out.PlanName = plan.Name

	out.Entries = []QuotaEntry{
		quotaEntry(ResourceSites, int64(usage.Sites), int64(plan.MaxSites), false),
		quotaEntry(ResourceDatabases, int64(usage.Databases), int64(plan.MaxDatabases), false),
		quotaEntry(ResourceCronjobs, int64(usage.Cronjobs), int64(plan.MaxCronjobs), false),
		quotaEntry(ResourceFTP, int64(usage.FTPAccounts), int64(plan.MaxFTP), false),
		quotaEntry(ResourceDisk, usage.DiskBytes, plan.DiskQuotaMB*1024*1024, true),
		quotaEntry(ResourceTraffic, usage.TrafficBytes, plan.TrafficQuotaMB*1024*1024, true),
	}
	return out, nil
}

func quotaEntry(res Resource, used, limit int64, bytes bool) QuotaEntry {
	e := QuotaEntry{Resource: res, Used: used, Limit: limit, Percent: -1, Bytes: bytes}
	if !store.Unlimited(limit) {
		e.Percent = min(float64(used)/float64(limit)*100, 100)
	}
	return e
}

// CheckCount prüft eine Anzahl-Grenze vor dem Anlegen einer Ressource.
//
// Diese eine Funktion ersetzt die zuvor je Dienst kopierte Prüfung — dieselbe
// Logik dreimal zu pflegen war der sicherere Weg, sie irgendwann in einem
// davon zu vergessen.
func (s *QuotaService) CheckCount(ctx context.Context, sc store.Scope, tenantID int64, res Resource) error {
	tenantScope, err := sc.ForTenant(tenantID)
	if err != nil {
		return err
	}

	plan, err := s.store.PlanForTenant(ctx, tenantScope, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // kein Paket = kein Limit
	}
	if err != nil {
		return err
	}

	var limit int
	switch res {
	case ResourceSites:
		limit = plan.MaxSites
	case ResourceDatabases:
		limit = plan.MaxDatabases
	case ResourceCronjobs:
		limit = plan.MaxCronjobs
	case ResourceFTP:
		limit = plan.MaxFTP
	default:
		return fmt.Errorf("für %q gibt es keine anzahl-grenze", res)
	}
	if limit <= 0 {
		return nil
	}

	used, err := s.countFor(ctx, tenantScope, res)
	if err != nil {
		return err
	}
	if used >= limit {
		return fmt.Errorf("%w: paket %q erlaubt %d %s, %d sind bereits angelegt",
			ErrQuotaExceeded, plan.Name, limit, resourceLabels[res], used)
	}
	return nil
}

func (s *QuotaService) countFor(ctx context.Context, sc store.Scope, res Resource) (int, error) {
	switch res {
	case ResourceSites:
		return s.store.CountSites(ctx, sc)
	case ResourceDatabases:
		return s.store.CountDatabases(ctx, sc)
	case ResourceCronjobs:
		return s.store.CountCronjobs(ctx, sc)
	case ResourceFTP:
		return s.store.CountFTPAccounts(ctx, sc)
	}
	return 0, fmt.Errorf("unbekannte ressource %q", res)
}

// CheckDisk prüft, ob zusätzliche Bytes noch in die Quota passen.
//
// Gemessen wird der Stand der letzten Messung; zwischen zwei Messungen kann
// eine Quota also leicht überschritten werden. Das ist der bewusste Preis
// dafür, dass nicht bei jedem Upload ein Verzeichnisdurchlauf startet.
func (s *QuotaService) CheckDisk(ctx context.Context, sc store.Scope, tenantID, additional int64) error {
	tenantScope, err := sc.ForTenant(tenantID)
	if err != nil {
		return err
	}

	plan, err := s.store.PlanForTenant(ctx, tenantScope, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if store.Unlimited(plan.DiskQuotaMB) {
		return nil
	}

	usage, err := s.store.UsageForTenant(ctx, tenantScope, tenantID)
	if err != nil {
		return err
	}

	limit := plan.DiskQuotaMB * 1024 * 1024
	if usage.DiskBytes+additional > limit {
		return fmt.Errorf("%w: paket %q erlaubt %d MB, belegt sind bereits %d MB",
			ErrQuotaExceeded, plan.Name, plan.DiskQuotaMB, usage.DiskBytes/1024/1024)
	}
	return nil
}

// Measure misst den Verbrauch aller Sites und schreibt ihn zurück.
//
// Läuft periodisch im Hintergrund, nicht im Anfragepfad: ein Durchlauf über
// eine große Site dauert Sekunden.
func (s *QuotaService) Measure(ctx context.Context) (measured int, errs []error) {
	sites, err := s.store.ListSites(ctx, store.SystemScope())
	if err != nil {
		return 0, []error{err}
	}

	for _, site := range sites {
		if ctx.Err() != nil {
			return measured, append(errs, ctx.Err())
		}

		usage, err := s.agent.DiskUsage(ctx, site.RootPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", site.Domain, err))
			continue
		}
		if err := s.store.RecordSiteUsage(ctx, site.ID, usage.Bytes, usage.Files); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", site.Domain, err))
			continue
		}
		measured++
	}
	return measured, errs
}

// RunPeriodically misst in festem Abstand, bis der Context endet.
func (s *QuotaService) RunPeriodically(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Einmal kurz nach dem Start messen, damit das Panel nicht erst nach einer
	// Stunde Zahlen zeigt — aber nicht sofort, damit der Start schnell bleibt.
	first := time.NewTimer(30 * time.Second)
	defer first.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			s.measureAndLog(ctx)
		case <-ticker.C:
			s.measureAndLog(ctx)
		}
	}
}

func (s *QuotaService) measureAndLog(ctx context.Context) {
	measured, errs := s.Measure(ctx)
	if len(errs) > 0 {
		s.log.Warn("verbrauchsmessung teilweise fehlgeschlagen",
			"gemessen", measured, "fehler", len(errs), "erster", errs[0])
		return
	}
	s.log.Debug("verbrauch gemessen", "sites", measured)
}
