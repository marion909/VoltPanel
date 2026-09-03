package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Was Rspamd tatsächlich aussortiert.
//
// Rspamd bringt seine Regeln selbst mit — das Panel trägt es beim
// Mail-Setup nur als zweiten Milter ein (siehe ops_mail.go). Damit war
// bisher nirgends sichtbar, was es damit anfängt: ob überhaupt etwas
// gescannt wird, wie viel als Spam gilt, wie viel abgewiesen statt nur
// markiert wird.
//
// Rspamds Controller-Worker beantwortet das lokal und unauthentifiziert
// (der Normalfall auf 127.0.0.1, siehe rspamd-Dokumentation) — kein
// zusätzliches Geheimnis, das der Agent kennen oder verwalten müsste. Ein
// geschütztes Controller-Passwort zu unterstützen wäre mehr Fläche für ein
// Werkzeug, dessen einzige Aufgabe hier ist, vier Zahlen abzulesen.

// rspamdControllerURL als Variable, nicht als Konstante: der Test ersetzt sie
// durch eine lokale Gegenstelle. Verändert wird sie sonst nirgends — im
// laufenden Agent ist es immer dieselbe feste, lokale Adresse.
var rspamdControllerURL = "http://127.0.0.1:11334/stat"

const rspamdTimeout = 5 * time.Second

// RspamdStats ist der Auszug aus Rspamds eigener Statistik, den das Panel
// zeigt.
type RspamdStats struct {
	Installed bool `json:"installed"`
	// Reachable heißt: der Controller hat geantwortet. false bei "installiert,
	// aber der Dienst läuft nicht" oder "durch ein eigenes Passwort
	// geschützt" — beides ist kein Fehler des Panels.
	Reachable bool  `json:"reachable"`
	Scanned   int64 `json:"scanned"`
	SpamCount int64 `json:"spam_count"`
	HamCount  int64 `json:"ham_count"`
	// Actions ist Rspamds eigene Aufschlüsselung — "no action", "add header",
	// "greylist", "reject" und was die installierte Fassung sonst kennt.
	// Unverändert durchgereicht statt selbst benannt: die Bezeichnungen
	// gehören Rspamd, und eine eigene Übersetzung liefe bei der nächsten
	// Fassung auseinander.
	Actions map[string]int64 `json:"actions,omitempty"`
	Hinweis string           `json:"hinweis,omitempty"`
}

// opMailSpamStats fragt Rspamds Controller nach seiner Statistik.
func (s *Server) opMailSpamStats(ctx context.Context, _ json.RawMessage) (any, error) {
	res := RspamdStats{Actions: map[string]int64{}}
	if !dirExists(rspamdDir) {
		return res, nil
	}
	res.Installed = true

	body, err := httpGetSigned(ctx, rspamdControllerURL, rspamdTimeout)
	if err != nil {
		res.Hinweis = "der rspamd-controller antwortet nicht: " + kurzeFehlermeldung(err)
		return res, nil
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		res.Hinweis = "antwort des rspamd-controllers nicht lesbar: " + err.Error()
		return res, nil
	}

	if err := parseRspamdStats(raw, &res); err != nil {
		res.Hinweis = err.Error()
		return res, nil
	}
	res.Reachable = true
	return res, nil
}

// rspamdRawStats ist der Ausschnitt aus /stat, den dieses Panel liest.
//
// Rspamds Antwort trägt deutlich mehr Felder (Uptime, Version, Sprachfilter,
// Fuzzy-Hashes …) — nur ausgelesen, was hier auch gezeigt wird. Ein
// zusätzliches Feld in einer neueren Rspamd-Fassung soll diese Auswertung
// nicht brechen, deshalb kein strikter Decoder.
type rspamdRawStats struct {
	Scanned   int64            `json:"scanned"`
	SpamCount int64            `json:"spam_count"`
	HamCount  int64            `json:"ham_count"`
	Actions   map[string]int64 `json:"actions"`
}

func parseRspamdStats(raw []byte, res *RspamdStats) error {
	var r rspamdRawStats
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("die antwort des rspamd-controllers ist kein json, das dieses panel kennt: %w", err)
	}
	res.Scanned, res.SpamCount, res.HamCount = r.Scanned, r.SpamCount, r.HamCount
	if r.Actions != nil {
		res.Actions = r.Actions
	}
	return nil
}

// kurzeFehlermeldung kürzt einen Verbindungsfehler auf das Wesentliche —
// "connection refused" statt der ganzen URL und Adressauflösung davor.
func kurzeFehlermeldung(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}
