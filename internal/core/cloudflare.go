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

// Die DNS-Einträge, die zu einer Maildomäne gehören, über Cloudflare setzen.
//
// Die Zustellbarkeitsprüfung sagt, was fehlt — SPF, DKIM, DMARC. Sie
// abzuschreiben und von Hand einzutragen ist die Stelle, an der ein Tippfehler
// entsteht, den danach niemand sieht: ein DKIM-Eintrag mit einem falschen
// Zeichen prüft sich nicht mehr, und die Mail wird abgewertet statt gar nicht
// unterschrieben.
//
// Der Token liegt schon da — verschlüsselt beim Mandanten, seit Phase 2 für
// die Wildcard-Zertifikate. Hier wird er ein zweites Mal benutzt, und das ist
// der ganze Grund, warum das hier so kurz ist.
//
// Die Adresse ist fest. Kein SSRF-Thema: es gibt kein Feld, in das jemand
// einen anderen Host schreiben könnte.

// cloudflareAPIBase ist die Adresse der API — als Variable, damit der Test
// eine Attrappe unterschieben kann. Verändert wird sie ausschließlich dort;
// im laufenden Programm gibt es kein Feld und keinen Schalter dafür, und
// deshalb auch keine Möglichkeit, die Anfragen woanders hinzulenken.
var cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

type cloudflareClient struct {
	token string
	http  *http.Client
}

func newCloudflareClient(token string) *cloudflareClient {
	return &cloudflareClient{
		token: token,
		// Ein eigener Client mit Zeitgrenze: der Standardclient hat keine, und
		// eine hängende Anfrage hielte den Aufruf im Panel fest, bis der
		// Browser aufgibt.
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// cfAntwort ist der Umschlag, den Cloudflare um jede Antwort legt.
type cfAntwort struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (c *cloudflareClient) ruf(ctx context.Context, methode, pfad string, rumpf any) (
	json.RawMessage, error) {

	var body io.Reader
	if rumpf != nil {
		roh, err := json.Marshal(rumpf)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(roh)
	}

	req, err := http.NewRequestWithContext(ctx, methode, cloudflareAPIBase+pfad, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()

	roh, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var a cfAntwort
	if err := json.Unmarshal(roh, &a); err != nil {
		return nil, fmt.Errorf("cloudflare antwortet unlesbar (%s)", resp.Status)
	}
	if !a.Success {
		// Die Meldung von Cloudflare weitergeben: "invalid API token" ist eine
		// Auskunft, "Fehler 400" ist keine.
		var texte []string
		for _, e := range a.Errors {
			texte = append(texte, e.Message)
		}
		if len(texte) == 0 {
			texte = append(texte, resp.Status)
		}
		return nil, fmt.Errorf("cloudflare: %s", strings.Join(texte, "; "))
	}
	return a.Result, nil
}

// zoneID sucht die Zone, in der ein Name liegt.
//
// Gefragt wird nach der Domäne selbst und danach nach jedem übergeordneten
// Namen: "mail.example.at" liegt in der Zone "example.at", und ein Kunde trägt
// im Panel durchaus eine Subdomäne als Maildomäne ein.
func (c *cloudflareClient) zoneID(ctx context.Context, domain string) (string, error) {
	teile := strings.Split(domain, ".")
	for i := 0; i+1 < len(teile); i++ {
		kandidat := strings.Join(teile[i:], ".")
		roh, err := c.ruf(ctx, http.MethodGet,
			"/zones?name="+url.QueryEscape(kandidat), nil)
		if err != nil {
			return "", err
		}
		var zonen []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(roh, &zonen); err != nil {
			return "", err
		}
		if len(zonen) > 0 {
			return zonen[0].ID, nil
		}
	}
	return "", fmt.Errorf("für %s gibt es bei diesem Token keine Zone", domain)
}

// cfRecord ist ein DNS-Eintrag, soweit er hier gebraucht wird.
type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// txtRecords sind die vorhandenen TXT-Einträge zu einem Namen.
func (c *cloudflareClient) txtRecords(ctx context.Context, zone, name string) (
	[]cfRecord, error) {

	roh, err := c.ruf(ctx, http.MethodGet,
		"/zones/"+zone+"/dns_records?type=TXT&name="+url.QueryEscape(name), nil)
	if err != nil {
		return nil, err
	}
	var out []cfRecord
	if err := json.Unmarshal(roh, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// setzeTXT legt einen TXT-Eintrag an oder ändert ihn.
//
// Geändert wird nur, was diesem Panel gehört — der Aufrufer entscheidet das,
// indem er überhaupt fragt. Für SPF und DMARC fragt er nur, wenn nichts
// dasteht; einen vorhandenen SPF-Eintrag zu überschreiben hieße, andere
// Absender des Kunden auszusperren.
func (c *cloudflareClient) setzeTXT(ctx context.Context, zone, name, inhalt string) error {
	vorhanden, err := c.txtRecords(ctx, zone, name)
	if err != nil {
		return err
	}
	// TTL 1 heißt bei Cloudflare "automatisch".
	neu := cfRecord{Type: "TXT", Name: name, Content: inhalt, TTL: 1}

	if len(vorhanden) > 0 {
		if vorhanden[0].Content == inhalt {
			return nil // schon richtig, nichts zu tun
		}
		_, err = c.ruf(ctx, http.MethodPut,
			"/zones/"+zone+"/dns_records/"+vorhanden[0].ID, neu)
		return err
	}
	_, err = c.ruf(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", neu)
	return err
}
