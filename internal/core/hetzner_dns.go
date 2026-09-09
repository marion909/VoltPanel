package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hetznerDNSClient bedient Hetzners Cloud-API-DNS (seit November 2025 GA,
// ersetzt die alte dns.hetzner.com-API). Ein Eintrag heißt hier "RRSet" und
// bündelt alle Werte eines Namens+Typs (mehrere A-Records unter demselben
// Namen liegen in einem RRSet, nicht in mehreren wie bei Cloudflare) — daher
// die Umsetzung über add_records/remove_records statt eines einzelnen
// PUT/DELETE pro Wert.
//
// Unverified: ohne einen echten Hetzner-Account ließ sich das nur gegen
// öffentlich einsehbare Dokumentation (Changelog, Ansible-Collection)
// gegenprüfen, nicht gegen eine echte Antwort. Vor dem produktiven Einsatz
// einmal mit einem echten Token durchklicken.
var hetznerAPIBase = "https://api.hetzner.cloud/v1"

type hetznerDNSClient struct {
	token string
	http  *http.Client
}

func newHetznerDNSClient(token string) *hetznerDNSClient {
	return &hetznerDNSClient{token: token, http: &http.Client{Timeout: 20 * time.Second}}
}

func (c *hetznerDNSClient) ruf(ctx context.Context, methode, pfad string, rumpf any) (json.RawMessage, error) {
	var body io.Reader
	if rumpf != nil {
		roh, err := json.Marshal(rumpf)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(roh)
	}

	req, err := http.NewRequestWithContext(ctx, methode, hetznerAPIBase+pfad, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hetzner nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()

	roh, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var fehler struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(roh, &fehler) == nil && fehler.Error.Message != "" {
			return nil, fmt.Errorf("hetzner: %s", fehler.Error.Message)
		}
		return nil, fmt.Errorf("hetzner: %s", resp.Status)
	}
	if len(roh) == 0 {
		return json.RawMessage("{}"), nil
	}
	return roh, nil
}

func (c *hetznerDNSClient) ListZones(ctx context.Context) ([]DNSZone, error) {
	roh, err := c.ruf(ctx, http.MethodGet, "/zones?per_page=50", nil)
	if err != nil {
		return nil, err
	}
	var antwort struct {
		Zones []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"zones"`
	}
	if err := json.Unmarshal(roh, &antwort); err != nil {
		return nil, err
	}
	out := make([]DNSZone, 0, len(antwort.Zones))
	for _, z := range antwort.Zones {
		out = append(out, DNSZone{ID: fmt.Sprint(z.ID), Name: z.Name, Provider: "hetzner"})
	}
	return out, nil
}

type hetznerRRSetRecord struct {
	Value   string `json:"value"`
	Comment string `json:"comment,omitempty"`
}

func (c *hetznerDNSClient) ListRecords(ctx context.Context, zoneID string) ([]DNSRecord, error) {
	roh, err := c.ruf(ctx, http.MethodGet, "/zones/"+zoneID+"/rrsets?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	var antwort struct {
		RRSets []struct {
			Name    string               `json:"name"`
			Type    string               `json:"type"`
			TTL     int                  `json:"ttl"`
			Records []hetznerRRSetRecord `json:"records"`
		} `json:"rrsets"`
	}
	if err := json.Unmarshal(roh, &antwort); err != nil {
		return nil, err
	}
	var out []DNSRecord
	for _, rrset := range antwort.RRSets {
		for _, rec := range rrset.Records {
			// Priority bleibt leer: bei MX/SRV steckt sie im Rohwert
			// ("10 mail.example.com"), Hetzner liefert kein eigenes Feld dafür.
			out = append(out, DNSRecord{Name: rrset.Name, Type: rrset.Type, Value: rec.Value, TTL: rrset.TTL})
		}
	}
	return out, nil
}

// rrsetPath baut den Pfad zu genau einem RRSet — Hetzner adressiert es über
// Name+Typ, nicht über eine eigene ID im Pfad.
func rrsetPath(zoneID, name, recType string) string {
	return "/zones/" + zoneID + "/rrsets/" + url.PathEscape(name) + "/" + url.PathEscape(recType)
}

func (c *hetznerDNSClient) CreateRecord(ctx context.Context, zoneID string, r DNSRecord) error {
	// Erst die echte Anlege-Operation versuchen — die schlägt fehl, wenn es
	// den Namen+Typ schon gibt, statt (wie set_records es täte) bestehende
	// Werte unter demselben RRSet stillschweigend zu überschreiben.
	_, err := c.ruf(ctx, http.MethodPost, "/zones/"+zoneID+"/rrsets", map[string]any{
		"name":    r.Name,
		"type":    r.Type,
		"ttl":     ttlOrNil(r.TTL),
		"records": []hetznerRRSetRecord{{Value: r.Value}},
	})
	if err == nil {
		return nil
	}
	// Gibt es das RRSet schon (weiterer Wert unter demselben Namen+Typ, z. B.
	// ein zweiter MX), ergänzt add_records ohne die vorhandenen zu berühren.
	_, err = c.ruf(ctx, http.MethodPost, rrsetPath(zoneID, r.Name, r.Type)+"/actions/add_records", map[string]any{
		"records": []hetznerRRSetRecord{{Value: r.Value}},
	})
	return err
}

func (c *hetznerDNSClient) UpdateRecord(ctx context.Context, zoneID, oldValue string, r DNSRecord) error {
	if _, err := c.ruf(ctx, http.MethodPost, rrsetPath(zoneID, r.Name, r.Type)+"/actions/remove_records",
		map[string]any{"records": []hetznerRRSetRecord{{Value: oldValue}}}); err != nil {
		return err
	}
	if _, err := c.ruf(ctx, http.MethodPost, rrsetPath(zoneID, r.Name, r.Type)+"/actions/add_records",
		map[string]any{"records": []hetznerRRSetRecord{{Value: r.Value}}}); err != nil {
		return err
	}
	if r.TTL <= 0 {
		return nil
	}
	_, err := c.ruf(ctx, http.MethodPost, rrsetPath(zoneID, r.Name, r.Type)+"/actions/change_ttl",
		map[string]any{"ttl": r.TTL})
	return err
}

func (c *hetznerDNSClient) DeleteRecord(ctx context.Context, zoneID string, r DNSRecord) error {
	if _, err := c.ruf(ctx, http.MethodPost, rrsetPath(zoneID, r.Name, r.Type)+"/actions/remove_records",
		map[string]any{"records": []hetznerRRSetRecord{{Value: r.Value}}}); err != nil {
		return err
	}

	// War das der letzte Wert, bleibt sonst ein leeres RRSet zurück — beim
	// erneuten Anlegen desselben Namens+Typs würde CreateRecord dann auf ein
	// leeres statt gar kein RRSet treffen. Aufräumen, Fehler dabei ignorieren
	// (typischerweise: es gibt noch andere Werte, oder es ist schon weg).
	roh, err := c.ruf(ctx, http.MethodGet, "/zones/"+zoneID+"/rrsets?per_page=100", nil)
	if err != nil {
		return nil
	}
	var antwort struct {
		RRSets []struct {
			Name    string               `json:"name"`
			Type    string               `json:"type"`
			Records []hetznerRRSetRecord `json:"records"`
		} `json:"rrsets"`
	}
	if json.Unmarshal(roh, &antwort) != nil {
		return nil
	}
	for _, rrset := range antwort.RRSets {
		if rrset.Name == r.Name && strings.EqualFold(rrset.Type, r.Type) && len(rrset.Records) == 0 {
			_, _ = c.ruf(ctx, http.MethodDelete, rrsetPath(zoneID, r.Name, r.Type), nil)
			break
		}
	}
	return nil
}

func ttlOrNil(ttl int) any {
	if ttl <= 0 {
		return nil
	}
	return ttl
}
