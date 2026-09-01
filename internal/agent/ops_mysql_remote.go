package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Zugriff auf MariaDB von außen.
//
// Zwei Dinge müssen dafür zusammenkommen, und sie liegen auf verschiedenen
// Ebenen: ein Konto, das eine fremde Herkunft zulässt (das macht die
// Whitelist), und ein Server, der überhaupt auf der Netzwerkschnittstelle
// horcht. Debian bindet MariaDB ab Werk an 127.0.0.1 — ohne den zweiten
// Schritt wäre die Whitelist eine Liste ohne Wirkung.
//
// Der zweite Schritt ist bewusst serverweit und dem Administrator vorbehalten.
// Er betrifft alle Mandanten gleichzeitig, und er ist die Entscheidung, den
// Datenbankserver ins Netz zu stellen.

const (
	// Ein eigenes Drop-in statt einer Änderung an 50-server.cnf: die Datei des
	// Pakets bleibt unangetastet, das Abschalten ist ein Löschen, und bei einem
	// Paket-Update gibt es keinen conffile-Konflikt.
	mysqlRemoteConf = "60-volt-remote.cnf"
	mysqlPort       = 3306
)

// mysqlConfDirs in der Reihenfolge, in der gesucht wird. MariaDB und MySQL
// legen ihre Drop-ins an verschiedenen Stellen ab.
var mysqlConfDirs = []string{
	"/etc/mysql/mariadb.conf.d",
	"/etc/mysql/mysql.conf.d",
	"/etc/mysql/conf.d",
}

// MySQLRemoteParams schaltet den Netzwerkzugang an oder ab.
type MySQLRemoteParams struct {
	Enabled bool `json:"enabled"`
}

// MySQLRemoteResult beschreibt, worauf MariaDB gerade horcht.
type MySQLRemoteResult struct {
	// Listening ist die Auskunft des laufenden Servers, nicht der Inhalt einer
	// Konfigurationsdatei. Eine Datei sagt nur, was beim nächsten Start gelten
	// soll.
	Listening    bool   `json:"listening"`
	BindAddress  string `json:"bind_address"`
	Port         int    `json:"port"`
	ConfigPath   string `json:"config_path"`
	FirewallHint string `json:"firewall_hint"`
}

// opMySQLRemoteStatus fragt den laufenden Server, nicht die Konfiguration.
func (s *Server) opMySQLRemoteStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	res, err := s.mysqlRemoteState(ctx)
	if err != nil {
		return nil, opErr(OpMySQLRemoteStatus, "%v", err)
	}
	return res, nil
}

func (s *Server) mysqlRemoteState(ctx context.Context) (MySQLRemoteResult, error) {
	res := MySQLRemoteResult{Port: mysqlPort, ConfigPath: mysqlRemoteConfPath()}

	db, err := mysqlConn()
	if err != nil {
		return res, err
	}
	vars := map[string]string{}
	rows, err := db.QueryContext(ctx,
		"SHOW VARIABLES WHERE Variable_name IN ('bind_address','port','skip_networking')")
	if err != nil {
		return res, fmt.Errorf("serverzustand abfragen: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return res, err
		}
		vars[strings.ToLower(name)] = value
	}
	if err := rows.Err(); err != nil {
		return res, err
	}

	res.BindAddress = vars["bind_address"]
	if p, err := strconv.Atoi(vars["port"]); err == nil && p > 0 {
		res.Port = p
	}
	res.Listening = mysqlListensOutside(res.BindAddress) && !isTrue(vars["skip_networking"])
	return res, nil
}

// mysqlListensOutside entscheidet aus bind_address, ob eine Verbindung von
// außen überhaupt ankommen kann.
//
// Leer bedeutet bei MariaDB "alle Schnittstellen" — das ist kein Sonderfall,
// den man übersehen darf, sondern der häufigste Zustand auf einem Server, den
// jemand schon einmal von Hand angefasst hat.
func mysqlListensOutside(bind string) bool {
	bind = strings.TrimSpace(bind)
	if bind == "" || bind == "*" {
		return true
	}
	for _, part := range strings.Split(bind, ",") {
		addr, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if !addr.IsLoopback() {
			return true
		}
	}
	return false
}

func isTrue(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "ON", "1", "TRUE", "YES":
		return true
	}
	return false
}

func mysqlRemoteConfPath() string {
	for _, dir := range mysqlConfDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return filepath.Join(dir, mysqlRemoteConf)
		}
	}
	return filepath.Join(mysqlConfDirs[0], mysqlRemoteConf)
}

// mysqlRemoteBody ist der gesamte Inhalt der Datei. Er enthält keinen Wert aus
// einer Anfrage — die Operation kennt nur an und aus.
const mysqlRemoteBody = `# Von VoltPanel geschrieben. Änderungen gehen beim nächsten Schalten verloren.
#
# Der Zugang von außen wird nicht hier entschieden, sondern über die
# Herkunftsliste der einzelnen Datenbankbenutzer. Diese Datei sorgt nur dafür,
# dass eine Verbindung überhaupt ankommen kann.
[mysqld]
bind-address = 0.0.0.0
skip-networking = 0
`

// opMySQLRemoteSet schaltet den Netzwerkzugang an oder ab.
func (s *Server) opMySQLRemoteSet(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[MySQLRemoteParams](raw, OpMySQLRemoteSet)
	if err != nil {
		return nil, err
	}

	path := mysqlRemoteConfPath()
	if p.Enabled {
		if err := os.WriteFile(path, []byte(mysqlRemoteBody), 0o644); err != nil {
			return nil, opErr(OpMySQLRemoteSet, "konfiguration schreiben: %v", err)
		}
	} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, opErr(OpMySQLRemoteSet, "konfiguration entfernen: %v", err)
	}

	// Der Neustart ist unvermeidlich: bind-address lässt sich zur Laufzeit
	// nicht setzen. Deshalb ist das hier eine eigene, seltene Operation und
	// nicht etwas, das beim Anlegen einer Herkunft nebenbei passiert.
	unit := "mariadb"
	if _, err := run(ctx, shortTimeout, "systemctl", "is-active", unit); err != nil {
		unit = "mysql"
	}
	if out, err := run(ctx, longTimeout, "systemctl", "restart", unit); err != nil {
		return nil, opErr(OpMySQLRemoteSet, "%s neu starten: %s", unit, truncate(out, 300))
	}

	// Die offene Verbindung des Agents ist nach dem Neustart tot. database/sql
	// merkt das beim nächsten Versuch selbst und holt eine frische — die
	// Abfrage darunter braucht deshalb kein Zutun.
	res, err := s.mysqlRemoteState(ctx)
	if err != nil {
		return nil, opErr(OpMySQLRemoteSet, "%s neu gestartet, zustand unklar: %v", unit, err)
	}
	res.FirewallHint = s.setMySQLPort(ctx, p.Enabled, res.Port)
	return res, nil
}

// setMySQLPort öffnet oder schließt den Port in ufw, sofern ufw läuft.
//
// Wie bei FTP: feste Argumente, und bei nftables geschieht nichts außer einer
// Auskunft. In ein fremdes Regelwerk schreibt der Agent nicht.
func (s *Server) setMySQLPort(ctx context.Context, open bool, port int) string {
	rule := strconv.Itoa(port) + "/tcp"

	out, err := run(ctx, shortTimeout, "ufw", "status")
	if err != nil || !strings.Contains(out, "Status: active") {
		if open {
			return fmt.Sprintf("Port %d muss in der Firewall offen sein — "+
				"und nur für die Adressen, die in den Herkunftslisten stehen.", port)
		}
		return fmt.Sprintf("Port %d kann in der Firewall wieder zu.", port)
	}

	action := "allow"
	if !open {
		action = "deny"
	}
	if out, err := run(ctx, shortTimeout, "ufw", action, rule); err != nil {
		s.log.Warn("ufw-regel nicht gesetzt", "regel", rule, "aktion", action,
			"err", err, "out", truncate(out, 200))
		return fmt.Sprintf("Die Firewall-Regel für Port %d konnte nicht gesetzt werden.", port)
	}
	if open {
		return fmt.Sprintf("ufw: Port %d ist offen. Enger wäre eine Regel je Adresse.", port)
	}
	return fmt.Sprintf("ufw: Port %d ist gesperrt.", port)
}
