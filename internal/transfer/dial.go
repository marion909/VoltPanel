// Package transfer bringt fertige Archive an einen fremden Ort — einen
// S3-Speicher oder einen FTP-Server.
//
// Alles hier ist ein ausgehender Verbindungsaufbau zu einer Adresse, die aus
// der Oberfläche kommt. Das ist eine eigene Art von Angriffsfläche: der
// Panel-Server steht in einem Netz, in dem der Kunde nichts zu suchen hat, und
// eine Anfrage von dort aus erreicht Dinge, die er selbst nie erreichen würde.
// Deshalb beginnt dieses Paket mit dem Wähler und nicht mit dem Hochladen.
package transfer

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// SafeDialer baut Verbindungen auf und prüft dabei die Adresse, mit der
// tatsächlich gesprochen wird.
//
// Der Name allein zu prüfen genügt nicht. Zwischen "der Name zeigt auf eine
// erlaubte Adresse" und "die Verbindung geht dorthin" liegt eine zweite
// Auflösung, und wer den DNS-Eintrag stellt, kann dazwischen die Antwort
// wechseln — beim ersten Mal 93.184.216.34, beim zweiten 169.254.169.254.
//
// Deshalb sitzt die Prüfung in Control: die Funktion läuft, nachdem die Adresse
// feststeht und bevor connect() sie benutzt. Was hier abgelehnt wird, wurde nie
// verbunden.
var SafeDialer = &net.Dialer{
	Timeout:   15 * time.Second,
	KeepAlive: 30 * time.Second,
	Control:   controlAddr,
}

func controlAddr(network, address string, _ syscall.RawConn) error {
	if network != "tcp4" && network != "tcp6" && network != "tcp" {
		return fmt.Errorf("%s ist kein zugelassenes protokoll", network)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("adresse %q nicht lesbar", address)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("adresse %q nicht lesbar", host)
	}
	return CheckAddr(addr)
}

// CheckAddr entscheidet, ob der Panel-Server mit dieser Adresse sprechen darf.
//
// Abgelehnt wird, was kein fremder Dienst im Internet sein kann:
//
//   - Loopback. 127.0.0.1 ist das Panel selbst, MariaDB, der Agent-Socket-Host.
//   - Link-local. 169.254.169.254 ist bei praktisch jedem Cloud-Anbieter der
//     Metadaten-Dienst; eine Anfrage dorthin liefert die Zugangsschlüssel des
//     Servers. Das ist der klassische SSRF-Treffer, und er kostet die ganze
//     Maschine.
//   - Multicast und die unspezifische Adresse. Beide sind kein Gegenüber.
//
// Private Netze (10/8, 192.168/16, fc00::/7) bleiben erlaubt. Ein MinIO oder
// ein FTP-Server im selben Rechenzentrumsnetz ist ein üblicher Aufbewahrungsort
// für Backups, und ihn zu verbieten hiesse, das Feature für genau die Leute
// abzuschalten, die es am ehesten richtig benutzen. Wer den Panel-Server in ein
// Netz stellt, in dem das ein Problem wäre, hat dort eine Firewall.
func CheckAddr(addr netip.Addr) error {
	// ::ffff:127.0.0.1 ist Loopback, sieht aber wie IPv6 aus. Erst entpacken,
	// dann urteilen — sonst prüft man die falsche Adresse.
	if addr.Is4In6() {
		addr = addr.Unmap()
	}

	switch {
	case !addr.IsValid():
		return fmt.Errorf("keine gültige adresse")
	case addr.IsUnspecified():
		return fmt.Errorf("0.0.0.0 ist kein ziel")
	case addr.IsLoopback():
		return fmt.Errorf("%s zeigt auf diesen server selbst", addr)

	// Multicast vor link-local: 224.0.0.1 ist beides, und die Erklärung zum
	// Metadaten-Dienst weiter unten passt dafür nicht.
	case addr.IsMulticast(), addr.IsInterfaceLocalMulticast():
		return fmt.Errorf("%s ist eine multicast-adresse und kein gegenüber", addr)

	case addr.IsLinkLocalUnicast():
		return fmt.Errorf("%s ist eine link-local-adresse — dort liegt bei den meisten "+
			"anbietern der metadaten-dienst mit den zugangsschlüsseln des servers", addr)
	}
	return nil
}

// DialContext ist der Wähler für alles in diesem Paket.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return SafeDialer.DialContext(ctx, network, address)
}
