package core

import (
	"context"
	"strings"

	"github.com/marion909/voltpanel/internal/agent"
	"github.com/marion909/voltpanel/internal/store"
)

// SQL-Browser.
//
// Der Aufrufer nennt eine Datenbank-ID und eine Anweisung. Den Namen der
// Datenbank schickt er nicht mit — er wird hier aus der ID im Zugriffsbereich
// des Mandanten nachgeschlagen. Käme er aus der Anfrage, wäre "führe diese
// Abfrage aus" der Weg in die Datenbank eines anderen Kunden, und die
// ID-Prüfung darüber wäre reine Zierde.
//
// Dieselbe Regel wie bei den FTP-Zugängen, dem Terminal und dem Update: was
// bestimmt, worauf eine Operation wirkt, wird abgeleitet, nicht übernommen.

// QueryResult reicht die Antwort des Agents durch, ergänzt um den Namen der
// Datenbank, gegen die tatsächlich gelaufen wurde.
type QueryResult struct {
	*agent.MySQLQueryResult
	Database string `json:"database"`
}

// RunQuery führt eine Anweisung gegen eine Datenbank des Mandanten aus.
func (s *DatabaseService) RunQuery(ctx context.Context, sc store.Scope, databaseID int64,
	statement string, maxRows int) (*QueryResult, error) {

	db, err := s.store.GetDatabase(ctx, sc, databaseID)
	if err != nil {
		return nil, err
	}

	res, err := s.agent.RunQuery(ctx, db.Name, statement, maxRows)
	if err != nil {
		return nil, err
	}
	return &QueryResult{MySQLQueryResult: res, Database: db.Name}, nil
}

// AuditStatement kürzt eine Anweisung für das Audit-Log.
//
// Vollständig gehörte sie dort nicht hin: ein INSERT kann ein ganzes Formular
// enthalten, und das Log ist nicht der Ort für Kundendaten. Der Anfang genügt,
// um zu sehen, was jemand getan hat.
func AuditStatement(statement string) string {
	s := strings.Join(strings.Fields(statement), " ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
