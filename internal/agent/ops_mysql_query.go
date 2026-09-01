package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SQL aus der Oberfläche ausführen.
//
// Das ist die Operation, bei der Inhalt aus einer Anfrage am dichtesten an die
// Datenbank kommt: eine ganze SQL-Anweisung, vom Kunden getippt. Prüfen lässt
// sie sich nicht — eine Whitelist erlaubter SQL wäre entweder nutzlos oder ein
// zweiter SQL-Parser. Also wird nicht die Anweisung eingeschränkt, sondern das
// Konto, unter dem sie läuft.
//
// Der Agent spricht als root über den Unix-Socket mit MariaDB. Über diese
// Verbindung geht hier nichts. Sie legt nur ein Wegwerf-Konto an, das
// ausschliesslich auf die eine Datenbank Rechte hat; die Anweisung selbst läuft
// über eine zweite Verbindung unter diesem Konto und wird danach samt Konto
// weggeworfen.
//
// Was damit ausgeschlossen ist: der Blick in fremde Datenbanken, `CREATE USER`,
// `SELECT … INTO OUTFILE` und `LOAD_FILE()`. Die dafür nötigen Rechte sind in
// MariaDB global und lassen sich auf eine einzelne Datenbank gar nicht
// vergeben.

const (
	// queryUserPrefix kennzeichnet die Wegwerf-Konten dieser Operation. Er darf
	// sich nicht ändern, sonst bleiben alte Konten liegen.
	queryUserPrefix = "volt_query_"

	// Obergrenzen. Eine Abfrage ist etwas, das ein Mensch tippt und liest —
	// nicht etwas, das eine Tabelle mit zehn Millionen Zeilen durch den
	// Arbeitsspeicher des Panels zieht.
	queryMaxRows    = 500
	queryMaxCell    = 4096
	queryMaxLength  = 64 * 1024
	queryTimeout    = 30 * time.Second
	queryConnectDSN = "%s:%s@unix(/var/run/mysqld/mysqld.sock)/%s" +
		"?timeout=10s&readTimeout=30s&writeTimeout=30s&parseTime=false"
)

// MySQLQueryParams ist eine Anweisung an genau eine Datenbank.
type MySQLQueryParams struct {
	Database  string `json:"database"`
	Statement string `json:"statement"`
	// MaxRows begrenzt die Antwort. 0 bedeutet die Obergrenze oben.
	MaxRows int `json:"max_rows"`
}

// MySQLQueryResult ist entweder eine Ergebnismenge oder die Zahl der
// betroffenen Zeilen — je nachdem, was die Anweisung war.
type MySQLQueryResult struct {
	Columns []string    `json:"columns"`
	Rows    [][]*string `json:"rows"`
	// Truncated sagt, dass es mehr Zeilen gäbe. Ohne diese Angabe hielte
	// jemand die abgeschnittene Menge für die vollständige.
	Truncated    bool  `json:"truncated"`
	RowsAffected int64 `json:"rows_affected"`
	// HasResultSet unterscheidet "keine Zeilen gefunden" von "gab keine
	// Zeilen zurück". Ein SELECT ohne Treffer ist nicht dasselbe wie ein
	// UPDATE, das nichts geändert hat.
	HasResultSet bool   `json:"has_result_set"`
	DurationMS   int64  `json:"duration_ms"`
	Warning      string `json:"warning"`
}

// opMySQLQuery führt eine einzelne Anweisung aus.
func (s *Server) opMySQLQuery(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLQueryParams](raw, OpMySQLQuery)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLName("datenbankname", p.Database, reMyDBName); err != nil {
		return nil, err
	}

	statement := strings.TrimSpace(p.Statement)
	if statement == "" {
		return nil, opInputErr(OpMySQLQuery, "die anweisung ist leer")
	}
	if len(statement) > queryMaxLength {
		return nil, opInputErr(OpMySQLQuery,
			"die anweisung ist länger als %d zeichen — für einen ganzen dump gibt es den import",
			queryMaxLength)
	}

	limit := p.MaxRows
	if limit <= 0 || limit > queryMaxRows {
		limit = queryMaxRows
	}

	root, err := mysqlConn()
	if err != nil {
		return nil, err
	}
	username, password, drop, err := s.throwawayAccount(ctx, root, p.Database,
		queryUserPrefix, OpMySQLQuery)
	if err != nil {
		return nil, err
	}
	defer drop()

	// Eigene Verbindung unter dem begrenzten Konto. multiStatements steht
	// bewusst nicht im DSN: der Treiber weist damit alles ab, was mehr als eine
	// Anweisung ist, und ein angehängtes "; DROP TABLE …" scheitert schon dort.
	conn, err := sql.Open("mysql", fmt.Sprintf(queryConnectDSN, username, password, p.Database))
	if err != nil {
		return nil, opErr(OpMySQLQuery, "verbindung: %v", err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// Eine festgehaltene Verbindung, keine aus dem Pool. ROW_COUNT() unten gilt
	// je Verbindung und je Anweisung — auf einer anderen wäre die Zahl nicht
	// die der eben gelaufenen Anweisung.
	single, err := conn.Conn(ctx)
	if err != nil {
		return nil, opErr(OpMySQLQuery, "verbindung: %v", err)
	}
	defer single.Close()

	start := time.Now()
	res, err := runStatement(ctx, single, statement, limit)
	if err != nil {
		return nil, opInputErr(OpMySQLQuery, "%s", queryHint(err, p.Database))
	}
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

// runStatement entscheidet an der Antwort des Servers, nicht am Text der
// Anweisung, ob es eine Ergebnismenge gibt.
//
// Am ersten Wort zu raten wäre falsch: `WITH … SELECT`, `CALL`, `SHOW`,
// `EXPLAIN`, `DESCRIBE` und ein `INSERT … RETURNING` liefern alle Zeilen, und
// die Liste wäre nie vollständig. Query() funktioniert auch für ein UPDATE —
// es kommt dann nur eine leere Menge zurück.
func runStatement(ctx context.Context, conn *sql.Conn, statement string, limit int) (*MySQLQueryResult, error) {
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	res := &MySQLQueryResult{Columns: cols, Rows: [][]*string{}}
	if len(cols) == 0 {
		// Kein Ergebnis: eine schreibende Anweisung. Die Zahl der betroffenen
		// Zeilen liefert Query() nicht, also wird sie separat erfragt.
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// Die Ergebnismenge muss zu sein, bevor auf derselben Verbindung die
		// nächste Anweisung läuft.
		rows.Close()
		var affected int64
		if err := conn.QueryRowContext(ctx, "SELECT ROW_COUNT()").Scan(&affected); err == nil {
			res.RowsAffected = affected
		}
		return res, nil
	}
	res.HasResultSet = true

	// []byte statt string: der Treiber liefert alles als Bytes, und ein NULL
	// muss von einem leeren Text unterscheidbar bleiben. Deshalb *string.
	raw := make([]any, len(cols))
	for i := range raw {
		raw[i] = new(sql.RawBytes)
	}

	for rows.Next() {
		if len(res.Rows) >= limit {
			res.Truncated = true
			break
		}
		if err := rows.Scan(raw...); err != nil {
			return nil, err
		}
		row := make([]*string, len(cols))
		for i, cell := range raw {
			b := *(cell.(*sql.RawBytes))
			if b == nil {
				continue
			}
			text := string(b)
			if len(text) > queryMaxCell {
				text = text[:queryMaxCell] + "…"
			}
			row[i] = &text
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if res.Truncated {
		res.Warning = fmt.Sprintf("Nur die ersten %d Zeilen werden angezeigt. "+
			"Mit LIMIT eingrenzen oder den Export nutzen.", limit)
	}
	return res, nil
}

// queryHint erklärt die beiden Fehler, die durch das begrenzte Konto und die
// Ein-Anweisungs-Regel neu sind. Ohne die Erklärung sähen beide nach einem
// Fehler im Panel aus.
func queryHint(err error, database string) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Access denied"), strings.Contains(msg, "command denied"):
		return msg + fmt.Sprintf("\n\nDie Abfrage läuft mit einem Zugang, der nur auf %s "+
			"Rechte hat. Anweisungen über die Datenbank hinaus — andere Datenbanken, "+
			"Benutzerverwaltung, Dateizugriff — sind hier nicht möglich.", database)
	case strings.Contains(msg, "You have an error in your SQL syntax") &&
		strings.Contains(msg, ";"):
		return msg + "\n\nEs geht genau eine Anweisung auf einmal. Mehrere durch " +
			"Semikolon getrennte sind nicht zugelassen."
	}
	return msg
}
