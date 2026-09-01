package agent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/bits"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Der Agent spricht MySQL über den lokalen Unix-Socket. Auf Debian und Ubuntu
// authentifiziert sich root dort per unix_socket-Plugin — es gibt also kein
// Passwort, das irgendwo hinterlegt werden müsste.
const mysqlDSN = "root@unix(/var/run/mysqld/mysqld.sock)/?timeout=10s&readTimeout=30s&writeTimeout=30s"

// Bezeichner in DDL-Anweisungen lassen sich nicht als Parameter übergeben.
// Statt auf Quoting zu vertrauen, wird hier eine sehr enge Zeichenmenge
// erzwungen: Was diese Regexe passiert, kann in Backticks nichts anrichten.
var (
	reMyDBName  = regexp.MustCompile(`^[a-z][a-z0-9_]{2,47}$`)
	reMyUser    = regexp.MustCompile(`^[a-z][a-z0-9_]{2,30}$`)
	reMyCharset = regexp.MustCompile(`^[a-z0-9_]{2,32}$`)
	reMyCollate = regexp.MustCompile(`^[a-z0-9_]{2,64}$`)

	// Das Passwort steht in einfachen Anführungszeichen in der Anweisung.
	// Alles, was daraus ausbrechen könnte — Anführungszeichen, Backslash,
	// Steuerzeichen — ist hier schlicht nicht erlaubt.
	reMyPassword = regexp.MustCompile(`^[A-Za-z0-9!#%()*+,\-./:;<=>?@^_{|}~]{12,128}$`)
)

// mysqlPool hält genau eine Verbindung offen. Der Agent macht selten und kurz
// etwas mit MySQL; ein größerer Pool brächte nichts außer Leerlaufverbindungen.
var (
	mysqlOnce sync.Once
	mysqlDB   *sql.DB
	mysqlErr  error
)

func mysqlConn() (*sql.DB, error) {
	mysqlOnce.Do(func() {
		mysqlDB, mysqlErr = sql.Open("mysql", mysqlDSN)
		if mysqlErr != nil {
			return
		}
		mysqlDB.SetMaxOpenConns(2)
		mysqlDB.SetMaxIdleConns(1)
		mysqlDB.SetConnMaxLifetime(5 * time.Minute)
	})
	if mysqlErr != nil {
		return nil, fmt.Errorf("mysql-verbindung: %w", mysqlErr)
	}
	return mysqlDB, nil
}

func checkMySQLName(kind, value string, re *regexp.Regexp) error {
	if !re.MatchString(value) {
		return fmt.Errorf("%w: %s %q", errBadInput, kind, value)
	}
	return nil
}

// checkMySQLHost prüft die Herkunft eines Kontos: 'benutzer'@'HIER'.
//
// Diese Prüfung wiederholt die aus dem Store — und das ist ihr Zweck. Der Agent
// ist der einzige Prozess, der root an MariaDB ist; er darf sich nicht darauf
// verlassen, dass der Web-Prozess vorher geprüft hat. Wäre der Web-Prozess über
// eine Lücke übernommen, wäre `'kunde'@'%'` sonst ein Konto, das von jeder
// Adresse der Welt Verbindungen annimmt.
//
// Erlaubt sind localhost, eine IP-Adresse und ein Netz in genau den beiden
// Formen, die der Store erzeugt: IPv4 mit Netzmaske (10.0.0.0/255.255.255.0)
// und IPv6 mit Präfixlänge (2001:db8::/64). Kein %, keine Hostnamen.
func checkMySQLHost(host string) error {
	if host == "localhost" {
		return nil
	}
	if host == "" || len(host) > 60 || strings.ContainsAny(host, "%'`\\ ") {
		return fmt.Errorf("%w: host-muster %q", errBadInput, host)
	}

	addrPart, maskPart, hasMask := strings.Cut(host, "/")
	addr, err := netip.ParseAddr(addrPart)
	if err != nil || addr.IsUnspecified() {
		return fmt.Errorf("%w: host-muster %q ist keine ip-adresse", errBadInput, host)
	}
	if !hasMask {
		return nil
	}

	if addr.Is4() {
		mask, err := netip.ParseAddr(maskPart)
		if err != nil || !mask.Is4() {
			return fmt.Errorf("%w: netzmaske %q", errBadInput, maskPart)
		}
		octets := mask.As4()
		v := uint32(octets[0])<<24 | uint32(octets[1])<<16 |
			uint32(octets[2])<<8 | uint32(octets[3])
		// Eine Maske mit Löchern (255.0.255.0) ist keine; MariaDB würde sie
		// stillschweigend anders auslegen als gemeint.
		if v != 0 && (^v+1)&(^v) != 0 {
			return fmt.Errorf("%w: netzmaske %q ist nicht zusammenhängend", errBadInput, maskPart)
		}
		// Die Länge, nicht nur die Form. 1.2.3.4/0.0.0.0 ist eine
		// wohlgeformte, zusammenhängende Maske — und bedeutet in MariaDB
		// jede Adresse, also dasselbe wie %.
		// Nach der Prüfung oben ist die Maske zusammenhängend; die Zahl der
		// gesetzten Bits ist damit die Präfixlänge.
		if bits.OnesCount32(v) < minMySQLPrefixV4 {
			return fmt.Errorf("%w: netzmaske %q fasst zu weit", errBadInput, maskPart)
		}
		return nil
	}

	length, err := strconv.Atoi(maskPart)
	if err != nil || length < minMySQLPrefixV6 || length > 128 {
		return fmt.Errorf("%w: präfixlänge %q", errBadInput, maskPart)
	}
	return nil
}

// Dieselben Untergrenzen wie im Store. Ein Netz, das darunter liegt, ist keine
// Herkunft mehr, sondern deren Abwesenheit.
const (
	minMySQLPrefixV4 = 16
	minMySQLPrefixV6 = 64
)

// quoteIdent setzt einen bereits validierten Bezeichner in Backticks.
//
// Die Verdopplung ist Gürtel zum Hosenträger: Nach den Regexen oben kann hier
// gar kein Backtick mehr ankommen.
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func (s *Server) opMySQLCreateDB(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLDBParams](raw, OpMySQLCreateDB)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLName("datenbankname", p.Name, reMyDBName); err != nil {
		return nil, err
	}
	if p.Charset == "" {
		p.Charset = "utf8mb4"
	}
	if p.Collation == "" {
		p.Collation = "utf8mb4_unicode_ci"
	}
	if err := checkMySQLName("zeichensatz", p.Charset, reMyCharset); err != nil {
		return nil, err
	}
	if err := checkMySQLName("sortierung", p.Collation, reMyCollate); err != nil {
		return nil, err
	}

	db, err := mysqlConn()
	if err != nil {
		return nil, err
	}
	// IF NOT EXISTS macht die Operation idempotent — ein zweiter Aufruf nach
	// einem Verbindungsabbruch soll nicht scheitern.
	stmt := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET %s COLLATE %s",
		quoteIdent(p.Name), p.Charset, p.Collation)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return nil, opErr(OpMySQLCreateDB, "%v", err)
	}
	return TextResult{Text: "datenbank " + p.Name + " angelegt"}, nil
}

func (s *Server) opMySQLDropDB(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLDBParams](raw, OpMySQLDropDB)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLName("datenbankname", p.Name, reMyDBName); err != nil {
		return nil, err
	}

	db, err := mysqlConn()
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(p.Name)); err != nil {
		return nil, opErr(OpMySQLDropDB, "%v", err)
	}
	return TextResult{Text: "datenbank " + p.Name + " entfernt"}, nil
}

// opMySQLCreateUser legt einen Benutzer an und erteilt ihm die Rechte auf genau
// einer Datenbank. Rechte auf "alles" gibt es hier bewusst nicht.
func (s *Server) opMySQLCreateUser(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLUserParams](raw, OpMySQLCreateUser)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLUser(p); err != nil {
		return nil, err
	}
	if !reMyPassword.MatchString(p.Password) {
		return nil, opErr(OpMySQLCreateUser,
			"passwort muss 12–128 zeichen lang sein und darf keine anführungszeichen oder backslashes enthalten")
	}

	db, err := mysqlConn()
	if err != nil {
		return nil, err
	}

	account := fmt.Sprintf("'%s'@'%s'", p.Username, p.HostPattern)
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY '%s'", account, p.Password)); err != nil {
		return nil, opErr(OpMySQLCreateUser, "%v", err)
	}
	// Ein zweiter Aufruf soll auch das Passwort aktualisieren, sonst wäre die
	// Operation nur halb idempotent.
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("ALTER USER %s IDENTIFIED BY '%s'", account, p.Password)); err != nil {
		return nil, opErr(OpMySQLCreateUser, "%v", err)
	}
	if err := applyGrants(ctx, db, p); err != nil {
		return nil, err
	}
	return TextResult{Text: "benutzer " + p.Username + " angelegt"}, nil
}

func (s *Server) opMySQLGrant(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLUserParams](raw, OpMySQLGrant)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLUser(p); err != nil {
		return nil, err
	}

	db, err := mysqlConn()
	if err != nil {
		return nil, err
	}
	if err := applyGrants(ctx, db, p); err != nil {
		return nil, err
	}
	return TextResult{Text: "rechte für " + p.Username + " gesetzt"}, nil
}

// applyGrants entzieht erst alles und erteilt dann neu — sonst würden alte
// Rechte einer früheren Stufe stehen bleiben.
func applyGrants(ctx context.Context, db *sql.DB, p MySQLUserParams) error {
	account := fmt.Sprintf("'%s'@'%s'", p.Username, p.HostPattern)
	target := quoteIdent(p.Database) + ".*"

	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("REVOKE ALL PRIVILEGES ON %s FROM %s", target, account)); err != nil {
		// Hatte der Benutzer noch keine Rechte, ist das kein Fehler.
		if !strings.Contains(err.Error(), "1141") && !strings.Contains(err.Error(), "no such grant") {
			return opErr(OpMySQLGrant, "rechte entziehen: %v", err)
		}
	}

	var privileges string
	switch strings.ToUpper(p.Grants) {
	case "READONLY":
		privileges = "SELECT, SHOW VIEW"
	case "READWRITE":
		privileges = "SELECT, INSERT, UPDATE, DELETE, SHOW VIEW"
	case "ALL", "":
		// Kein GRANT OPTION: der Benutzer darf seine Rechte nicht weiterreichen.
		privileges = "ALL PRIVILEGES"
	default:
		return opErr(OpMySQLGrant, "berechtigung %q ist unbekannt", p.Grants)
	}

	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("GRANT %s ON %s TO %s", privileges, target, account)); err != nil {
		return opErr(OpMySQLGrant, "rechte erteilen: %v", err)
	}
	if _, err := db.ExecContext(ctx, "FLUSH PRIVILEGES"); err != nil {
		return opErr(OpMySQLGrant, "flush privileges: %v", err)
	}
	return nil
}

func (s *Server) opMySQLSetPassword(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLUserParams](raw, OpMySQLSetPassword)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLName("benutzername", p.Username, reMyUser); err != nil {
		return nil, err
	}
	if err := checkMySQLHost(p.HostPattern); err != nil {
		return nil, err
	}
	if !reMyPassword.MatchString(p.Password) {
		return nil, opErr(OpMySQLSetPassword,
			"passwort muss 12–128 zeichen lang sein und darf keine anführungszeichen oder backslashes enthalten")
	}

	db, err := mysqlConn()
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER USER '%s'@'%s' IDENTIFIED BY '%s'",
		p.Username, p.HostPattern, p.Password)); err != nil {
		return nil, opErr(OpMySQLSetPassword, "%v", err)
	}
	return TextResult{Text: "passwort für " + p.Username + " gesetzt"}, nil
}

func (s *Server) opMySQLDropUser(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLUserParams](raw, OpMySQLDropUser)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLName("benutzername", p.Username, reMyUser); err != nil {
		return nil, err
	}
	if err := checkMySQLHost(p.HostPattern); err != nil {
		return nil, err
	}

	db, err := mysqlConn()
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'", p.Username, p.HostPattern)); err != nil {
		return nil, opErr(OpMySQLDropUser, "%v", err)
	}
	return TextResult{Text: "benutzer " + p.Username + " entfernt"}, nil
}

// opMySQLSizes liefert die Belegung je Datenbank für die Anzeige im Panel.
func (s *Server) opMySQLSizes(ctx context.Context, _ json.RawMessage) (any, error) {
	db, err := mysqlConn()
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT table_schema, COALESCE(SUM(data_length + index_length), 0)
		FROM information_schema.tables
		WHERE table_schema NOT IN ('mysql','information_schema','performance_schema','sys')
		GROUP BY table_schema`)
	if err != nil {
		return nil, opErr(OpMySQLSizes, "%v", err)
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var name string
		var size int64
		if err := rows.Scan(&name, &size); err != nil {
			return nil, opErr(OpMySQLSizes, "%v", err)
		}
		out[name] = size
	}
	return out, rows.Err()
}

// opMySQLDump schreibt einen Dump nach dest.
//
// mysqldump bekommt seine Argumente als argv, das Ziel ist ein Dateihandle —
// es gibt keine Shell und damit auch keine Umleitung, die man manipulieren könnte.
func (s *Server) opMySQLDump(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLDumpParams](raw, OpMySQLDump)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLName("datenbankname", p.Database, reMyDBName); err != nil {
		return nil, err
	}
	dest, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, opErr(OpMySQLDump, "zieldatei: %v", err)
	}
	defer f.Close()

	if err := runInto(ctx, longTimeout, f, nil, "mysqldump",
		"--defaults-file=/dev/null", "--protocol=socket",
		"--socket=/var/run/mysqld/mysqld.sock", "--user=root",
		"--single-transaction", "--quick", "--routines", "--events",
		"--default-character-set=utf8mb4", p.Database); err != nil {
		return nil, opErr(OpMySQLDump, "%v", err)
	}

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": dest, "size_bytes": info.Size()}, nil
}

// opMySQLImport spielt eine SQL-Datei ein.
//
// Der Import läuft NICHT als root, sondern unter einem Wegwerf-Konto, das nur
// auf die Zieldatenbank Rechte hat. Der Grund ist die Mandantentrennung: eine
// SQL-Datei ist Programmtext, kein Datenblock. Ein "USE fremde_db;" mitten im
// Dump würde als root anstandslos ausgeführt — der Kunde könnte also über den
// Import in die Datenbank eines anderen Kunden schreiben. Mit dem begrenzten
// Konto scheitert genau diese Zeile, und der Rest läuft normal durch.
func (s *Server) opMySQLImport(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLDumpParams](raw, OpMySQLImport)
	if err != nil {
		return nil, err
	}
	if err := checkMySQLName("datenbankname", p.Database, reMyDBName); err != nil {
		return nil, err
	}
	src, err := jail(p.Path, s.roots)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(src)
	if err != nil {
		return nil, opErr(OpMySQLImport, "quelldatei: %v", err)
	}
	defer f.Close()

	db, err := mysqlConn()
	if err != nil {
		return nil, err
	}

	cnf, drop, err := s.importAccount(ctx, db, p.Database)
	if err != nil {
		return nil, err
	}
	defer drop()
	defer os.Remove(cnf)

	if err := runInto(ctx, longTimeout, nil, f, "mysql",
		"--defaults-file="+cnf, "--protocol=socket",
		"--socket=/var/run/mysqld/mysqld.sock",
		"--default-character-set=utf8mb4", p.Database); err != nil {
		// Als Eingabefehler: scheitert der Import, liegt es fast immer an der
		// Datei. Der Aufrufer soll die Meldung von mysql lesen können und
		// keinen Gateway-Fehler bekommen, der den Server verdächtigt.
		return nil, opInputErr(OpMySQLImport, "%s", importHint(err, p.Database))
	}
	return TextResult{Text: "import in " + p.Database + " abgeschlossen"}, nil
}

// importAccount legt das Wegwerf-Konto an und schreibt die Optionsdatei, über
// die der mysql-Client sein Passwort bekommt.
//
// Die Optionsdatei statt "-p<passwort>": Argumente stehen in der Prozessliste,
// jeder Benutzer des Servers könnte das Passwort dort mitlesen. Sie liegt im
// Laufzeitverzeichnis des Agents, das nur root beschreiben kann — dort ist auch
// kein Symlink-Trick möglich.
func (s *Server) importAccount(ctx context.Context, db *sql.DB, database string) (string, func(), error) {
	username, password, drop, err := s.throwawayAccount(ctx, db, database, importUserPrefix, OpMySQLImport)
	if err != nil {
		return "", nil, err
	}
	cnf, err := s.writeClientConfig(username, password)
	if err != nil {
		drop()
		return "", nil, err
	}
	return cnf, drop, nil
}

// throwawayAccount legt ein Konto an, das ausschliesslich auf eine Datenbank
// Rechte hat, und gibt die Funktion zurück, die es wieder entfernt.
//
// Der Agent selbst spricht als root mit MariaDB. Alles, was Inhalt aus einer
// Anfrage ausführt — ein Dump, eine Abfrage aus dem Browser — läuft deshalb
// nicht über diese Verbindung, sondern über ein Konto, das die Datenbank nicht
// verlassen kann. Ein "USE fremde_db" scheitert damit, statt zu gelingen.
//
// Rechte auf eine Datenbank schliessen die globalen aus: FILE, PROCESS und
// SUPER lassen sich gar nicht auf eine einzelne Datenbank vergeben. Damit sind
// SELECT … INTO OUTFILE und LOAD_FILE() für dieses Konto keine Option.
func (s *Server) throwawayAccount(ctx context.Context, db *sql.DB, database, prefix string,
	op Op) (string, string, func(), error) {

	// Reste eines abgestürzten Laufs zuerst weg. Sonst sammeln sich Konten an,
	// deren Passwort niemand mehr kennt, die aber weiter Rechte hätten.
	s.dropStaleAccounts(ctx, db)

	suffix, err := randomHex(6)
	if err != nil {
		return "", "", nil, opErr(op, "zufall: %v", err)
	}
	password, err := randomHex(16)
	if err != nil {
		return "", "", nil, opErr(op, "zufall: %v", err)
	}

	username := prefix + suffix
	account := fmt.Sprintf("'%s'@'localhost'", username)
	drop := func() {
		// Eigener Kontext: läuft die Operation in einen Timeout, ist der
		// übergebene Kontext abgelaufen — das Konto muss trotzdem weg.
		c, cancel := context.WithTimeout(context.Background(), shortTimeout)
		defer cancel()
		if _, err := db.ExecContext(c, "DROP USER IF EXISTS "+account); err != nil {
			s.log.Error("wegwerf-konto nicht entfernt", "konto", username, "err", err)
		}
	}

	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("CREATE USER %s IDENTIFIED BY '%s'", account, password)); err != nil {
		return "", "", nil, opErr(op, "wegwerf-konto: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO %s", quoteIdent(database), account)); err != nil {
		drop()
		return "", "", nil, opErr(op, "rechte für das wegwerf-konto: %v", err)
	}
	return username, password, drop, nil
}

// importUserPrefix kennzeichnet die Wegwerf-Konten, damit sie sich wiederfinden
// lassen. Er darf sich nicht ändern, sonst bleiben alte Konten liegen.
const importUserPrefix = "volt_import_"

// throwawayPrefixes: jeder Lauf räumt die Reste aller Arten auf, nicht nur die
// der eigenen. Ein abgebrochener Import würde sonst darauf warten, dass jemand
// wieder importiert.
var throwawayPrefixes = []string{importUserPrefix, queryUserPrefix}

func (s *Server) dropStaleAccounts(ctx context.Context, db *sql.DB) {
	var stale []string
	for _, prefix := range throwawayPrefixes {
		rows, err := db.QueryContext(ctx,
			"SELECT user FROM mysql.user WHERE host = 'localhost' AND user LIKE ?", prefix+"%")
		if err != nil {
			continue
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil && reMyUser.MatchString(name) {
				stale = append(stale, name)
			}
		}
		rows.Close()
	}

	for _, name := range stale {
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf("DROP USER IF EXISTS '%s'@'localhost'", name)); err != nil {
			s.log.Warn("altes wegwerf-konto nicht entfernt", "konto", name, "err", err)
		}
	}
}

// writeClientConfig legt eine Optionsdatei für den mysql-Client an.
func (s *Server) writeClientConfig(username, password string) (string, error) {
	dir := filepath.Dir(s.socketPath)
	f, err := os.CreateTemp(dir, "import-*.cnf")
	if err != nil {
		return "", opErr(OpMySQLImport, "optionsdatei: %v", err)
	}
	defer f.Close()

	if err := f.Chmod(0o600); err != nil {
		os.Remove(f.Name())
		return "", opErr(OpMySQLImport, "optionsdatei: %v", err)
	}
	// Beide Werte sind durch reMyUser und randomHex auf [a-z0-9_] begrenzt —
	// in dieser Datei kann damit keine zweite Option entstehen.
	body := fmt.Sprintf("[client]\nuser=%s\npassword=%s\n", username, password)
	if _, err := f.WriteString(body); err != nil {
		os.Remove(f.Name())
		return "", opErr(OpMySQLImport, "optionsdatei: %v", err)
	}
	return f.Name(), nil
}

// importHint erklärt den einen Fehler, der beim begrenzten Konto neu ist.
func importHint(err error, database string) string {
	msg := err.Error()
	if !strings.Contains(msg, "Access denied") && !strings.Contains(msg, "command denied") {
		return msg
	}
	return msg + fmt.Sprintf("\n\nDer Import läuft mit einem Zugang, der nur auf %s Rechte hat. "+
		"Die Datei enthält vermutlich Anweisungen außerhalb dieser Datenbank — "+
		"typisch sind CREATE DATABASE, USE oder ein DEFINER in einer Prozedur. "+
		"Bitte diese Zeilen aus dem Dump entfernen.", database)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func checkMySQLUser(p MySQLUserParams) error {
	if err := checkMySQLName("benutzername", p.Username, reMyUser); err != nil {
		return err
	}
	if err := checkMySQLHost(p.HostPattern); err != nil {
		return err
	}
	return checkMySQLName("datenbankname", p.Database, reMyDBName)
}
