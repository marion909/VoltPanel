package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

const dbRemoteCols = `id, tenant_id, db_user_id, host, note, created_at`

// Die kleinsten Netze, die noch als Whitelist-Eintrag durchgehen.
//
// Eine Whitelist, in die „das halbe Internet“ passt, ist keine. /16 sind bei
// IPv4 65.536 Adressen — schon großzügig, aber ein Firmennetz kann so groß
// sein. Alles darunter ist keine Herkunft mehr, sondern deren Abwesenheit.
const (
	minPrefixV4 = 16
	minPrefixV6 = 64
)

// NormalizeRemoteHost prüft eine Herkunft und bringt sie in die Form, die
// MariaDB im GRANT erwartet.
//
// Erlaubt sind ausschließlich Adressen:
//
//	203.0.113.5           eine einzelne Maschine
//	10.0.0.0/24           ein Netz, wird zu 10.0.0.0/255.255.255.0
//	2001:db8::1           IPv6 einzeln
//
// Drei Dinge sind bewusst nicht erlaubt, jedes aus einem eigenen Grund.
//
// **%** — MySQLs Platzhalter. `'kunde'@'%'` nimmt Verbindungen von überall an;
// die Whitelist wäre damit in dem Moment leer, in dem sie angelegt wird. Auch
// `192.168.1.%` fällt weg: MariaDB versteht dafür die Netzmaskenform, und eine
// Schreibweise für dieselbe Sache reicht.
//
// **Hostnamen** — MariaDB löst sie beim Verbindungsaufbau rückwärts auf. Wer
// den PTR-Eintrag seiner eigenen Adresse setzen kann — bei den meisten Anbietern
// ein Formularfeld —, bestimmt damit, für welchen Whitelist-Eintrag er gehalten
// wird. Eine Zugriffskontrolle, die auf fremdverwalteten DNS-Daten beruht, ist
// keine.
//
// **0.0.0.0/0 und ::/0** — dasselbe wie %, nur in anderer Schreibweise. Die
// Prüfung unten fängt sie über die Präfixlänge ab, damit nicht jede neue
// Schreibweise für „überall“ einzeln nachgetragen werden muss.
func NormalizeRemoteHost(input string) (string, error) {
	host := strings.TrimSpace(input)
	if host == "" {
		return "", errors.New("die herkunft fehlt")
	}
	if len(host) > 60 {
		return "", errors.New("die herkunft ist zu lang")
	}
	if strings.Contains(host, "%") {
		return "", errors.New("% ist nicht erlaubt — eine whitelist mit platzhalter lässt " +
			"jede herkunft zu. für ein ganzes netz die schreibweise 10.0.0.0/24 verwenden")
	}

	// Netz mit Präfixlänge.
	if strings.Contains(host, "/") {
		return normalizeRemoteNetwork(host)
	}

	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "", fmt.Errorf("%q ist keine ip-adresse — hostnamen sind nicht erlaubt, "+
			"weil mariadb sie über dns rückwärts auflöst und der eigentümer der adresse "+
			"diesen eintrag selbst bestimmen kann", host)
	}
	if err := checkRoutableAddr(addr); err != nil {
		return "", err
	}
	return addr.String(), nil
}

// normalizeRemoteNetwork macht aus 10.0.0.0/24 die Form, die MariaDB kennt.
//
// MariaDB versteht bei IPv4 nur die Netzmaskenschreibweise, nicht die kurze
// mit der Präfixlänge. Eingeben lässt sich hier trotzdem die kurze: sie ist
// die, die jemand kennt, und die Umrechnung ist eindeutig.
func normalizeRemoteNetwork(host string) (string, error) {
	prefix, err := netip.ParsePrefix(host)
	if err != nil {
		return "", fmt.Errorf("%q ist kein gültiges netz (erwartet z. B. 10.0.0.0/24)", host)
	}
	// Ein Netz, dessen Adresse gesetzte Bits außerhalb der Maske hat
	// (10.0.0.5/24), ist fast immer ein Tippfehler für den Host selbst.
	if prefix.Masked() != prefix {
		return "", fmt.Errorf("%q ist keine netzadresse — gemeint ist vermutlich %s oder %s",
			host, prefix.Addr(), prefix.Masked())
	}
	if err := checkRoutableAddr(prefix.Addr()); err != nil {
		return "", err
	}

	if prefix.Addr().Is6() {
		if prefix.Bits() < minPrefixV6 {
			return "", fmt.Errorf("/%d ist zu weit gefasst — höchstens /%d",
				prefix.Bits(), minPrefixV6)
		}
		return prefix.String(), nil
	}
	if prefix.Bits() < minPrefixV4 {
		return "", fmt.Errorf("/%d ist zu weit gefasst — höchstens /%d. ein netz dieser "+
			"größe ist keine whitelist mehr", prefix.Bits(), minPrefixV4)
	}
	// Ein einzelner Host als /32 ist dasselbe wie die nackte Adresse. Kürzer
	// ist besser: die Netzmaskenform macht die Liste unnötig schwer lesbar.
	if prefix.Bits() == 32 {
		return prefix.Addr().String(), nil
	}
	return prefix.Addr().String() + "/" + ipv4Netmask(prefix.Bits()), nil
}

// checkRoutableAddr weist Adressen ab, die als Herkunft nichts bedeuten.
func checkRoutableAddr(addr netip.Addr) error {
	switch {
	case !addr.IsValid() || addr.IsUnspecified():
		return errors.New("0.0.0.0 steht für jede herkunft und ist damit keine")
	case addr.IsLoopback():
		return errors.New("localhost braucht keinen eintrag — der zugang von diesem " +
			"server aus besteht bereits")
	case addr.IsMulticast():
		return errors.New("eine multicast-adresse ist keine herkunft")
	case addr.Is4In6():
		// ::ffff:203.0.113.5 wäre für MariaDB eine andere Zeichenkette als
		// 203.0.113.5, für die Firewall aber dieselbe Maschine. Eine Form.
		return errors.New("ipv4-adressen bitte ohne ipv6-präfix angeben")
	}
	return nil
}

func ipv4Netmask(bits int) string {
	mask := ^uint32(0) << (32 - bits)
	parts := make([]string, 4)
	for i := 0; i < 4; i++ {
		parts[i] = strconv.Itoa(int(mask >> (24 - 8*i) & 0xff))
	}
	return strings.Join(parts, ".")
}

func (s *Store) CreateRemoteHost(ctx context.Context, sc Scope, h *DBRemoteHost) error {
	if err := sc.owns(h.TenantID); err != nil {
		return err
	}
	host, err := NormalizeRemoteHost(h.Host)
	if err != nil {
		return err
	}
	h.Host = host
	if len(h.Note) > 200 {
		h.Note = h.Note[:200]
	}
	h.CreatedAt = now()

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO db_remote_hosts (tenant_id, db_user_id, host, note, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		h.TenantID, h.DBUserID, h.Host, h.Note, h.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: %s steht schon in der liste", ErrConflict, h.Host)
		}
		return err
	}
	h.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetRemoteHost(ctx context.Context, sc Scope, id int64) (*DBRemoteHost, error) {
	where, args, err := sc.where("db_remote_hosts", "id = ?")
	if err != nil {
		return nil, err
	}
	return scanRemoteHost(s.db.QueryRowContext(ctx,
		`SELECT `+dbRemoteCols+` FROM db_remote_hosts`+where, append(args, id)...))
}

// ListRemoteHosts liefert die Herkünfte eines Benutzers, oder alle des Scopes,
// wenn userID 0 ist.
func (s *Store) ListRemoteHosts(ctx context.Context, sc Scope, userID int64) ([]*DBRemoteHost, error) {
	var extra []string
	if userID > 0 {
		extra = append(extra, "db_user_id = ?")
	}
	where, args, err := sc.where("db_remote_hosts", extra...)
	if err != nil {
		return nil, err
	}
	if userID > 0 {
		args = append(args, userID)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+dbRemoteCols+` FROM db_remote_hosts`+where+` ORDER BY host`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*DBRemoteHost{}
	for rows.Next() {
		h, err := scanRemoteHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) DeleteRemoteHost(ctx context.Context, sc Scope, id int64) error {
	where, args, err := sc.where("db_remote_hosts", "id = ?")
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM db_remote_hosts`+where, append(args, id)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanRemoteHost(sc scanner) (*DBRemoteHost, error) {
	var h DBRemoteHost
	err := sc.Scan(&h.ID, &h.TenantID, &h.DBUserID, &h.Host, &h.Note, &h.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}
