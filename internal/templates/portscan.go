package templates

import (
	"bytes"
	"fmt"
	"net/netip"
	"regexp"
	"time"
)

// Port-Scan-Erkennung als fail2ban-Jail.
//
// Die Firewall-Oberfläche stand schon, aber ein Scan von außen fiel nirgends
// auf: ufw wies die Pakete ab und schrieb eine Zeile ins Protokoll, und dort
// blieb sie liegen. Wer der Reihe nach an tausend Türen klopft, sucht einen
// offenen Dienst — und findet ihn irgendwann.
//
// Erkannt wird das nicht von uns, sondern von fail2ban: es liest die Zeilen,
// zählt sie je Adresse und sperrt. Das Panel schreibt die beiden Dateien und
// sagt fail2ban Bescheid; die Regel selbst kommt aus einer Vorlage, wie jede
// andere Konfiguration in diesem Projekt auch.
//
// Was hier bewusst *nicht* steht: ein Feld für einen eigenen regulären
// Ausdruck, ein Feld für eine eigene Aktion, ein Feld für einen Pfad. Ein
// fail2ban-Filter ist ausführbare Konfiguration — eine action-Zeile kann ein
// Kommando starten. Wer dort Text hineinreichen darf, darf alles.

// PortScanLevel ist die Empfindlichkeit.
//
// Drei Stufen statt dreier Zahlenfelder. Die Frage, die ein Betreiber
// beantworten kann, ist "wie schnell soll gesperrt werden" — nicht "wie viele
// Treffer in wie vielen Sekunden". Und drei geprüfte Stufen lassen sich nicht
// so einstellen, dass der Schutz zur Selbstsperre wird.
type PortScanLevel string

const (
	PortScanVorsichtig PortScanLevel = "vorsichtig"
	PortScanNormal     PortScanLevel = "normal"
	PortScanStreng     PortScanLevel = "streng"
)

// portScanStufen sind die einzigen Werte, die je in der Jail-Datei landen.
var portScanStufen = map[PortScanLevel]struct {
	MaxRetry int
	FindTime string
	BanTime  string
}{
	// Zwölf abgewiesene Pakete in zehn Minuten sind kein Versehen mehr, aber
	// noch keine Kampagne. Eine Stunde Sperre reicht, um einen Scanner zu
	// langweilen.
	PortScanVorsichtig: {12, "10m", "1h"},
	PortScanNormal:     {6, "10m", "6h"},
	// Drei Treffer in fünf Minuten und ein Tag Ruhe. Für einen Server, auf dem
	// außer den drei offenen Ports nichts zu suchen ist.
	PortScanStreng: {3, "5m", "24h"},
}

// ValidPortScanLevel sagt, ob eine Stufe bekannt ist.
func ValidPortScanLevel(l PortScanLevel) bool {
	_, ok := portScanStufen[l]
	return ok
}

// PortScanLevels sind die Stufen in der Reihenfolge, in der sie in der
// Oberfläche stehen sollen.
func PortScanLevels() []PortScanLevel {
	return []PortScanLevel{PortScanVorsichtig, PortScanNormal, PortScanStreng}
}

// rePortScanLog: der Pfad der Logdatei kommt aus dem Agent, nicht von außen.
// Geprüft wird er trotzdem — er landet in einer Datei, die fail2ban ausführt.
var rePortScanLog = regexp.MustCompile(`^/[A-Za-z0-9/._-]{1,120}$`)

// PortScanData ist das Eingabemodell beider Vorlagen.
type PortScanData struct {
	GeneratedAt string
	Level       PortScanLevel
	LogPath     string
	MaxRetry    int
	FindTime    string
	BanTime     string
	// IgnoreIPs sind Adressen und Netze, die nie gesperrt werden — die
	// Whitelist des Panels. Sich selbst auszusperren ist bei einem
	// Port-Scan-Schutz die naheliegendste Art, ihn kennenzulernen.
	IgnoreIPs []string
}

// RenderPortScan erzeugt Filter und Jail.
func RenderPortScan(level PortScanLevel, logPath string, ignore []string) (
	filter, jail string, err error) {

	stufe, ok := portScanStufen[level]
	if !ok {
		return "", "", fmt.Errorf("%q ist keine bekannte empfindlichkeit", level)
	}
	if !rePortScanLog.MatchString(logPath) {
		return "", "", fmt.Errorf("%q ist kein zulässiger pfad für das protokoll", logPath)
	}

	d := PortScanData{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Level:       level,
		LogPath:     logPath,
		MaxRetry:    stufe.MaxRetry,
		FindTime:    stufe.FindTime,
		BanTime:     stufe.BanTime,
		IgnoreIPs:   cleanIgnoreIPs(ignore),
	}

	var f, j bytes.Buffer
	if err := tmpl.ExecuteTemplate(&f, "portscan.filter.tmpl", d); err != nil {
		return "", "", err
	}
	if err := tmpl.ExecuteTemplate(&j, "portscan.jail.tmpl", d); err != nil {
		return "", "", err
	}
	return f.String(), j.String(), nil
}

// cleanIgnoreIPs lässt nur durch, was wirklich eine Adresse oder ein Netz ist.
//
// Die Liste kommt aus der config.yaml und damit von einem Menschen. Ein
// Tippfehler darin wäre in einer fail2ban-Datei nicht bloß wirkungslos: die
// Zeile steht in einer Datei, die fail2ban einliest, und was dort steht,
// gehört geprüft — auch wenn es aus dem eigenen Haus kommt.
func cleanIgnoreIPs(in []string) []string {
	out := make([]string, 0, len(in))
	gesehen := map[string]bool{}
	for _, s := range in {
		var norm string
		if p, err := netip.ParsePrefix(s); err == nil {
			norm = p.String()
		} else if a, err := netip.ParseAddr(s); err == nil {
			norm = a.String()
		} else {
			continue
		}
		if !gesehen[norm] {
			gesehen[norm] = true
			out = append(out, norm)
		}
	}
	return out
}
