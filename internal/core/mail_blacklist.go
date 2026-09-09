package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/marion909/voltpanel/internal/store"
)

// Die Domain-Blacklist-Abfrage, getrennt von der Zustellbarkeitsprüfung in
// mail_check.go: die dortige prüft, ob DNS/Postfix/PTR *für einen Versand
// bereit* sind, das hier prüft, ob eine einzelne Domäne schon *als
// Spam-Quelle bekannt* ist (dbl.spamhaus.org, Spamhaus' Domain Block List) —
// eine andere Frage, mit eigenem Ergebnis je Domäne statt einem
// serverweiten Befund.
const (
	BlacklistClean  = "sauber"
	BlacklistListed = "gelistet"
)

// dnsBlacklistLookup ist austauschbar für Tests — dieselbe Begründung wie
// dnsHost/dnsMX/dnsAddr in mail_check.go.
var dnsBlacklistLookup = dnsHost

// CheckBlacklist fragt dbl.spamhaus.org für eine Domäne ab und speichert das
// Ergebnis mit Zeitstempel. Auf Knopfdruck statt automatisch bei jedem
// Laden: eine DNS-Anfrage nach außen bei jedem Öffnen der Mail-Ansicht wäre
// unnötig langsam und unnötig Traffic für einen Wert, der sich nicht
// minütlich ändert.
func (s *MailService) CheckBlacklist(ctx context.Context, sc store.Scope, domainID int64) (*store.MailDomain, error) {
	d, err := s.store.GetMailDomain(ctx, sc, domainID)
	if err != nil {
		return nil, err
	}

	status := BlacklistClean
	if _, err := dnsBlacklistLookup(ctx, d.Domain+".dbl.spamhaus.org"); err == nil {
		status = BlacklistListed
	} else if !dnsNotFound(err) {
		// Ein echter Fehler (Resolver nicht erreichbar, Timeout) darf nicht als
		// "sauber" gespeichert werden — das wäre eine falsche Auskunft, die bis
		// zur nächsten Prüfung stehen bliebe.
		return nil, fmt.Errorf("blacklist-abfrage für %s: %w", d.Domain, err)
	}

	checkedAt := time.Now().Unix()
	if err := s.store.UpdateMailDomainBlacklist(ctx, sc, domainID, status, checkedAt); err != nil {
		return nil, err
	}
	d.BlacklistStatus, d.BlacklistCheckedAt = status, checkedAt
	return d, nil
}

// dnsNotFound erkennt "kein Eintrag" (NXDOMAIN) unter den Fehlern, die
// net.Resolver zurückgibt — das ist die normale, erwartete Antwort einer
// DNSBL für eine unauffällige Domäne, kein Ausfall.
func dnsNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}
