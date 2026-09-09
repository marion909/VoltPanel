package core

import "context"

// DNSZone ist eine DNS-Zone bei einem Provider — im Kern eine Domain.
type DNSZone struct {
	ID       string
	Name     string
	Provider string
}

// DNSRecord ist ein einzelner Eintrag innerhalb einer Zone.
//
// Es gibt keine providerübergreifend stabile ID: Cloudflare identifiziert
// einen Eintrag über eine eigene ID, Hetzner über die Kombination aus Name
// und Typ (ein "RRSet", das mehrere Werte bündeln kann). Update/Delete
// adressieren deshalb über den bisherigen Wert, nicht über ein Fremd-ID-Feld.
type DNSRecord struct {
	Name  string
	Type  string
	Value string
	TTL   int
	// Priority gilt nur für MX und SRV. Bei aus Hetzner gelesenen Einträgen
	// bleibt sie leer — dort steckt die Priorität im Rohwert (Value).
	Priority *int
}

// dnsProvider ist das gemeinsame Interface für Cloudflare und Hetzner, damit
// DNSService beide gleich behandeln kann.
type dnsProvider interface {
	ListZones(ctx context.Context) ([]DNSZone, error)
	ListRecords(ctx context.Context, zoneID string) ([]DNSRecord, error)
	CreateRecord(ctx context.Context, zoneID string, r DNSRecord) error
	// UpdateRecord ersetzt den bisherigen Wert (oldValue) eines Eintrags
	// durch r. oldValue wird gebraucht, weil unter einem Namen+Typ mehrere
	// Werte liegen können — ohne ihn ließe sich nicht sagen, welcher gemeint
	// ist.
	UpdateRecord(ctx context.Context, zoneID string, oldValue string, r DNSRecord) error
	DeleteRecord(ctx context.Context, zoneID string, r DNSRecord) error
}
