package store

import (
	"fmt"
	"strings"
)

type Role string

const (
	RoleOwner    Role = "owner"    // betreibt den Server, sieht alle Tenants
	RoleAdmin    Role = "admin"    // wie Owner, aber ohne Server-Destruktives
	RoleReseller Role = "reseller" // eigener Tenant + eigene Sub-Tenants
	RoleCustomer Role = "customer" // ausschließlich der eigene Tenant
)

func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleReseller, RoleCustomer:
		return true
	}
	return false
}

// CanCrossTenant sagt, ob die Rolle grundsätzlich über Tenant-Grenzen schauen darf.
func (r Role) CanCrossTenant() bool { return r == RoleOwner || r == RoleAdmin }

// Scope ist der Tenant-Filter, den jede Query dieses Pakets verlangt.
//
// Der Nullwert ist absichtlich unbrauchbar: wer vergisst einen Scope zu setzen,
// bekommt ErrNoTenant statt versehentlich alle Tenants zu lesen. Das ist die
// einzige Stelle, an der Multi-Tenancy durchgesetzt wird — deshalb geht hier
// nichts an der Prüfung vorbei.
type Scope struct {
	UserID   int64
	TenantID int64
	Role     Role

	// allTenants wird nur von SystemScope und Elevate gesetzt, nie aus einem
	// Request-Feld übernommen. Sonst wäre es genau der IDOR, den wir vermeiden.
	allTenants bool
}

// UserScope ist der Scope eines eingeloggten Users: strikt sein eigener Tenant.
func UserScope(userID, tenantID int64, role Role) Scope {
	return Scope{UserID: userID, TenantID: tenantID, Role: role}
}

// SystemScope gilt für CLI, Migrationen und Hintergrund-Jobs (Cert-Renewal,
// Metrics). Nur aus vertrauenswürdigem Code aufrufen, nie aus einem Handler.
func SystemScope() Scope {
	return Scope{TenantID: 0, Role: RoleOwner, allTenants: true}
}

// Elevate hebt einen Owner/Admin auf tenant-übergreifende Sicht. Eine Rolle, die
// das nicht darf, bekommt ihren unveränderten Scope zurück — der Aufrufer kann
// die Prüfung also nicht versehentlich überspringen.
func (s Scope) Elevate() Scope {
	if !s.Role.CanCrossTenant() {
		return s
	}
	s.allTenants = true
	return s
}

// ForTenant wechselt den Scope auf einen konkreten Tenant. Nur erlaubt, wenn die
// Rolle tenant-übergreifend arbeiten darf — so kann ein Kunde nicht per
// ?tenant_id=2 in einen fremden Mandanten springen.
func (s Scope) ForTenant(tenantID int64) (Scope, error) {
	if s.TenantID == tenantID {
		return s, nil
	}
	if !s.Role.CanCrossTenant() {
		return Scope{}, fmt.Errorf("%w: rolle %s darf tenant %d nicht wechseln", ErrForbidden, s.Role, tenantID)
	}
	s.TenantID = tenantID
	s.allTenants = false
	return s, nil
}

func (s Scope) IsSystem() bool { return s.allTenants }

// valid stellt sicher, dass ein Scope überhaupt etwas eingrenzt.
func (s Scope) valid() error {
	if s.allTenants {
		return nil
	}
	if s.TenantID <= 0 {
		return ErrNoTenant
	}
	if !s.Role.Valid() {
		return fmt.Errorf("%w: unbekannte rolle %q", ErrNoTenant, s.Role)
	}
	return nil
}

// where hängt den Tenant-Filter an eine WHERE-Klausel an.
//
// table ist der Tabellen-Alias, damit Joins eindeutig bleiben. Der Rückgabewert
// ist immer ein vollständiges " WHERE ..."-Fragment, sodass ein Aufrufer den
// Filter nicht durch Weglassen umgehen kann.
func (s Scope) where(table string, extra ...string) (string, []any, error) {
	if err := s.valid(); err != nil {
		return "", nil, err
	}

	conds := make([]string, 0, len(extra)+1)
	args := make([]any, 0, len(extra)+1)

	if !s.allTenants {
		conds = append(conds, qualify(table, "tenant_id")+" = ?")
		args = append(args, s.TenantID)
	}
	conds = append(conds, extra...)

	if len(conds) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}

// owns prüft eine bereits geladene Zeile gegen den Scope. Der Gürtel zum
// Hosenträger der WHERE-Klausel: greift, wenn eine Query mal ohne where()
// geschrieben wurde.
func (s Scope) owns(rowTenantID int64) error {
	if err := s.valid(); err != nil {
		return err
	}
	if s.allTenants || rowTenantID == s.TenantID {
		return nil
	}
	return fmt.Errorf("%w: zeile gehört tenant %d, scope ist tenant %d", ErrForbidden, rowTenantID, s.TenantID)
}

func qualify(table, col string) string {
	if table == "" {
		return col
	}
	return table + "." + col
}
