package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/config"
	"github.com/marion909/voltpanel/internal/store"
)

// CronService verwaltet die Cronjobs eines Tenants.
//
// Jeder Job wird als eigene Datei in /etc/cron.d abgelegt und läuft unter dem
// Systembenutzer der zugehörigen Site — nicht als root und nicht als der
// Panel-Benutzer. Ein Job kann damit nur das, was die Site selbst auch kann.
type CronService struct {
	store *store.Store
	agent *agent.Client
	cfg   *config.Config
	quota *QuotaService
}

func NewCronService(st *store.Store, ag *agent.Client, cfg *config.Config) *CronService {
	return &CronService{
		store: st, agent: ag, cfg: cfg,
		quota: NewQuotaService(st, ag, cfg, nil),
	}
}

type CreateCronjobInput struct {
	Name     string
	Schedule string
	Command  string
	SiteID   *int64
	Enabled  bool
	TenantID int64
}

// CreateCronjob legt einen Job an und schreibt ihn nach /etc/cron.d.
func (s *CronService) CreateCronjob(ctx context.Context, sc store.Scope, in CreateCronjobInput) (*store.Cronjob, error) {
	if in.TenantID == 0 {
		in.TenantID = sc.TenantID
	}
	tenantScope, err := sc.ForTenant(in.TenantID)
	if err != nil {
		return nil, err
	}
	if err := s.quota.CheckCount(ctx, sc, in.TenantID, ResourceCronjobs); err != nil {
		return nil, err
	}

	// Ein Job braucht einen Benutzer, unter dem er läuft. Ohne Site gäbe es
	// keinen — als root oder als Panel-Benutzer soll nichts laufen, was aus
	// dem Panel kommt.
	if in.SiteID == nil {
		return nil, errors.New("ein cronjob braucht eine site, unter deren benutzer er läuft")
	}
	site, err := s.store.GetSite(ctx, tenantScope, *in.SiteID)
	if err != nil {
		return nil, err
	}

	job := &store.Cronjob{
		TenantID: in.TenantID, SiteID: in.SiteID, Name: in.Name,
		Schedule: in.Schedule, Command: in.Command,
		RunAs: site.SystemUser, Enabled: in.Enabled,
	}
	if err := s.store.CreateCronjob(ctx, tenantScope, job); err != nil {
		return nil, err
	}

	if err := s.apply(ctx, job); err != nil {
		_ = s.store.DeleteCronjob(ctx, tenantScope, job.ID)
		return nil, err
	}
	return job, nil
}

// UpdateCronjob ändert einen Job und schreibt die Datei neu.
func (s *CronService) UpdateCronjob(ctx context.Context, sc store.Scope, job *store.Cronjob) error {
	if err := s.store.UpdateCronjob(ctx, sc, job); err != nil {
		return err
	}
	return s.apply(ctx, job)
}

func (s *CronService) DeleteCronjob(ctx context.Context, sc store.Scope, id int64) error {
	job, err := s.store.GetCronjob(ctx, sc, id)
	if err != nil {
		return err
	}
	if err := s.agent.RemoveCronjob(ctx, CronFileName(job.ID)); err != nil {
		return err
	}
	return s.store.DeleteCronjob(ctx, sc, id)
}

// Log liefert die Ausgabe der letzten Läufe.
func (s *CronService) Log(ctx context.Context, sc store.Scope, id int64, lines int) (string, error) {
	job, err := s.store.GetCronjob(ctx, sc, id)
	if err != nil {
		return "", err
	}
	return s.agent.CronLog(ctx, CronFileName(job.ID), lines)
}

// apply schreibt den Job nach /etc/cron.d — oder entfernt die Datei, wenn er
// abgeschaltet ist. Ein deaktivierter Job soll wirklich nicht laufen, nicht nur
// im Panel als inaktiv erscheinen.
func (s *CronService) apply(ctx context.Context, job *store.Cronjob) error {
	name := CronFileName(job.ID)
	if !job.Enabled {
		return s.agent.RemoveCronjob(ctx, name)
	}
	if err := s.agent.WriteCronjob(ctx, name, job.Schedule, job.Command, job.RunAs); err != nil {
		return fmt.Errorf("cronjob schreiben: %w", err)
	}
	return nil
}

// SyncAll schreibt alle Jobs neu — das Gegenstück zu `volt site rebuild`.
func (s *CronService) SyncAll(ctx context.Context) (int, []error) {
	jobs, err := s.store.ListCronjobs(ctx, store.SystemScope())
	if err != nil {
		return 0, []error{err}
	}

	var applied int
	var errs []error
	for _, job := range jobs {
		if err := s.apply(ctx, job); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", job.Name, err))
			continue
		}
		applied++
	}
	return applied, errs
}

// CronFileName ist der Dateiname in /etc/cron.d. Die ID im Namen macht ihn
// eindeutig und erlaubt es, den Job später wiederzufinden — auch wenn der
// Anzeigename geändert wurde.
func CronFileName(id int64) string { return fmt.Sprintf("volt_job_%d", id) }
