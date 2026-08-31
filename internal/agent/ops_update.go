package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// updateTimeout deckt Download beider Binaries plus Migration ab. Großzügig,
// weil ein Abbruch mitten im Tausch teurer ist als ein langes Warten.
const updateTimeout = 15 * time.Minute

// opSystemUpdate stößt das Update an.
//
// Der Aufruf nimmt bewusst keine Parameter. Welche Version installiert wird,
// steht im Kanal aus der Konfiguration des Agents — nicht in der Anfrage.
// Dürfte der Web-Prozess eine Quelle oder Prüfsumme mitgeben, wäre jede
// Übernahme des Panels ein Weg, beliebigen Code als root auszuführen; das
// Update wäre dann die Hintertür, die die ganze Trennung aufhebt.
//
// Getauscht wird über `volt update`, nicht über eine eigene Nachbildung:
// Snapshot vor dem Tausch, Prüfsumme gegen den Fahrplan und automatischer
// Rollback bei fehlgeschlagener Migration stecken dort und sind getestet.
func (s *Server) opSystemUpdate(ctx context.Context, raw json.RawMessage) (any, error) {
	// Der Aufruf hat keine Parameter — mitgeschickte werden abgewiesen statt
	// stillschweigend ignoriert.
	if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
		return nil, opErr(OpSystemUpdate, "diese operation nimmt keine parameter entgegen")
	}

	before := voltVersion(ctx)

	out, err := run(ctx, updateTimeout, "volt", "update", "--yes")
	if err != nil {
		return nil, opErr(OpSystemUpdate, "update fehlgeschlagen: %s", truncate(out, 2000))
	}

	after := voltVersion(ctx)
	res := UpdateResult{
		From: before, To: after,
		Changed: before != after && after != "",
		Output:  truncate(strings.TrimSpace(out), 4000),
	}

	if res.Changed {
		res.Restarted = true
		s.restartAfterUpdate()
	}
	return res, nil
}

// restartAfterUpdate startet die Dienste, nachdem die Antwort draußen ist.
//
// Die Verzögerung ist der Punkt: der Neustart von volt-web reißt die
// Verbindung ab, über die gerade das Ergebnis läuft, und der Neustart des
// Agents beendet diesen Prozess. Beides darf erst passieren, wenn der
// Aufrufer seine Antwort hat.
func (s *Server) restartAfterUpdate() {
	go func() {
		time.Sleep(3 * time.Second)

		// Ohne Bezug zum Anfrage-Context: der ist längst beendet.
		ctx, cancel := context.WithTimeout(context.Background(), longTimeout)
		defer cancel()

		if out, err := run(ctx, shortTimeout, "systemctl", "restart", "volt-web"); err != nil {
			s.log.Error("volt-web nach dem update nicht neu gestartet", "err", err, "out", truncate(out, 300))
		} else {
			s.log.Info("volt-web mit der neuen version gestartet")
		}

		// Zuletzt der Agent: der Aufruf beendet diesen Prozess, systemd
		// startet das getauschte Binary.
		s.log.Info("agent startet sich neu, um die neue version zu übernehmen")
		if out, err := run(ctx, shortTimeout, "systemctl", "restart", "volt-agent"); err != nil {
			s.log.Error("agent-neustart fehlgeschlagen — bitte von Hand", "err", err, "out", truncate(out, 300))
		}
	}()
}

// voltVersion liest die installierte Version. Ein Fehler ist kein Abbruch:
// die Angabe dient nur dem Vergleich vorher/nachher.
func voltVersion(ctx context.Context) string {
	out, err := run(ctx, shortTimeout, "volt", "--version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
