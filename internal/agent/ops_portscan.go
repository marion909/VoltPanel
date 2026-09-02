package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/marion909/voltpanel/internal/templates"
)

// Port-Scan-Erkennung.
//
// Die Firewall-Oberfläche stand, aber ein Scan von außen fiel nirgends auf:
// ufw wies die Pakete ab und schrieb eine Zeile ins Protokoll, und dort blieb
// sie liegen. Wer der Reihe nach an tausend Türen klopft, sucht einen offenen
// Dienst und findet ihn irgendwann.
//
// Gezählt und gesperrt wird von fail2ban. Das Panel schreibt zwei Dateien aus
// Vorlagen und sagt Bescheid — es gibt kein Feld für einen regulären Ausdruck
// und keines für eine Aktion. Ein fail2ban-Filter ist ausführbare
// Konfiguration; wer dort Text hineinreichen darf, darf alles.

const (
	portScanJail   = "volt-portscan"
	fail2banDir    = "/etc/fail2ban"
	portScanFilter = fail2banDir + "/filter.d/" + portScanJail + ".conf"
	portScanConf   = fail2banDir + "/jail.d/" + portScanJail + ".conf"
)

// portScanLogs sind die Orte, an denen ufw seine abgewiesenen Pakete
// hinterlässt — in der Reihenfolge, in der nachgesehen wird.
//
// Je nach Einrichtung landet dieselbe Zeile in einer eigenen Datei, im
// Kernel-Protokoll oder im Syslog. Eine feste Vorgabe wäre auf der Hälfte der
// Server falsch, und ein Eingabefeld dafür wäre ein Pfad aus fremder Hand in
// einer Datei, die fail2ban einliest.
var portScanLogs = []string{
	"/var/log/ufw.log",
	"/var/log/kern.log",
	"/var/log/syslog",
	"/var/log/messages",
}

type PortScanParams struct {
	Enabled bool   `json:"enabled"`
	Level   string `json:"level"`
	// IgnoreIPs ist die Whitelist des Panels. Sie kommt aus der config.yaml
	// und wird beim Rendern noch einmal geprüft.
	IgnoreIPs []string `json:"ignore_ips"`
}

type PortScanStatus struct {
	// Available heißt: fail2ban ist da. Ohne es gibt es hier nichts zu
	// schalten, und das soll die Oberfläche sagen können.
	Available bool     `json:"available"`
	Enabled   bool     `json:"enabled"`
	Level     string   `json:"level,omitempty"`
	LogPath   string   `json:"log_path,omitempty"`
	Currently int      `json:"currently"`
	Total     int      `json:"total"`
	Banned    []string `json:"banned"`
	Hinweis   string   `json:"hinweis,omitempty"`
}

// opPortScanStatus sagt, ob die Erkennung steht und wen sie gefasst hat.
func (s *Server) opPortScanStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	res := PortScanStatus{Banned: []string{}}
	if !fileExists(allowedBinaries["fail2ban-client"]) {
		res.Hinweis = "Fail2ban ist auf diesem Server nicht installiert — ohne es " +
			"gibt es niemanden, der die abgewiesenen Pakete zählt."
		return res, nil
	}
	res.Available = true

	raw, err := os.ReadFile(portScanConf)
	if err != nil {
		if logPath := findPortScanLog(); logPath == "" {
			res.Hinweis = "Auf diesem Server ist kein Protokoll zu finden, in dem ufw " +
				"abgewiesene Pakete festhält. Ohne `ufw logging on` gibt es nichts zu zählen."
		}
		return res, nil
	}
	res.Enabled = true
	res.Level, res.LogPath = readPortScanConf(string(raw))

	// Der Zustand kommt vom laufenden Dienst, nicht aus der Datei: eine Datei
	// sagt nur, was gewollt war.
	if out, err := run(ctx, shortTimeout, "fail2ban-client", "status", portScanJail); err == nil {
		res.Currently, res.Total, res.Banned = parseJailStatus(out)
	} else {
		res.Hinweis = "Die Datei steht, fail2ban kennt das Jail aber nicht: " +
			truncate(out, 200)
	}
	return res, nil
}

// readPortScanConf liest Stufe und Pfad aus der geschriebenen Datei zurück.
//
// Aus der Datei und nicht aus einer zweiten Ablage im Panel: was gilt, ist
// das, was auf dem Server steht. Eine Kopie in der Datenbank wäre die Stelle,
// an der beide auseinanderlaufen — etwa nachdem jemand die Datei von Hand
// entfernt hat.
func readPortScanConf(inhalt string) (level, logPath string) {
	for _, line := range strings.Split(inhalt, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "# Empfindlichkeit:"); ok {
			level = strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(line, "logpath"); ok {
			if _, wert, ok := strings.Cut(rest, "="); ok {
				logPath = strings.TrimSpace(wert)
			}
		}
	}
	return level, logPath
}

// findPortScanLog sucht das Protokoll, in dem die abgewiesenen Pakete stehen.
func findPortScanLog() string {
	for _, p := range portScanLogs {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// opPortScanSet schaltet die Erkennung ein oder aus.
//
// Der heikle Teil ist nicht das Schreiben, sondern das Danach: eine Datei, die
// fail2ban nicht versteht, nimmt den ganzen Dienst mit — auch die Jails, die
// vorher liefen. Deshalb dieselbe Bauart wie beim Vhost: schreiben, den Dienst
// prüfen lassen, und bei einem Fehler zurücknehmen, statt einen kaputten
// Zustand stehen zu lassen.
func (s *Server) opPortScanSet(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[PortScanParams](raw, OpPortScanSet)
	if err != nil {
		return nil, err
	}
	if !fileExists(allowedBinaries["fail2ban-client"]) {
		return nil, opErr(OpPortScanSet, "fail2ban ist auf diesem server nicht installiert")
	}

	if !p.Enabled {
		vorher, _ := os.ReadFile(portScanConf)
		_ = os.Remove(portScanConf)
		_ = os.Remove(portScanFilter)
		if out, err := run(ctx, shortTimeout, "fail2ban-client", "reload"); err != nil {
			// Zurücklegen: ohne die Datei und ohne Reload liefe das Jail
			// weiter, und der Zustand wäre weder das eine noch das andere.
			if len(vorher) > 0 {
				_ = writeFileAtomic(portScanConf, vorher, 0o644)
			}
			return nil, opErr(OpPortScanSet, "fail2ban neu laden: %s", truncate(out, 300))
		}
		return TextResult{Text: "port-scan-erkennung ausgeschaltet"}, nil
	}

	level := templates.PortScanLevel(p.Level)
	if !templates.ValidPortScanLevel(level) {
		return nil, opInputErr(OpPortScanSet, "%q ist keine bekannte empfindlichkeit", p.Level)
	}
	logPath := findPortScanLog()
	if logPath == "" {
		return nil, opErr(OpPortScanSet, "auf diesem server ist kein protokoll zu finden, "+
			"in dem ufw abgewiesene pakete festhält — ohne `ufw logging on` gibt es nichts zu zählen")
	}

	filter, jail, err := templates.RenderPortScan(level, logPath, p.IgnoreIPs)
	if err != nil {
		return nil, opInputErr(OpPortScanSet, "%v", err)
	}

	for _, dir := range []string{filepath.Dir(portScanFilter), filepath.Dir(portScanConf)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, opErr(OpPortScanSet, "%s anlegen: %v", dir, err)
		}
	}
	// Der Filter zuerst: das Jail zeigt auf ihn, und ein Jail ohne seinen
	// Filter ist genau der Fehler, der fail2ban nicht starten lässt.
	if err := writeFileAtomic(portScanFilter, []byte(filter), 0o644); err != nil {
		return nil, opErr(OpPortScanSet, "filter schreiben: %v", err)
	}
	if err := writeFileAtomic(portScanConf, []byte(jail), 0o644); err != nil {
		_ = os.Remove(portScanFilter)
		return nil, opErr(OpPortScanSet, "jail schreiben: %v", err)
	}

	if out, err := run(ctx, shortTimeout, "fail2ban-client", "reload"); err != nil {
		_ = os.Remove(portScanConf)
		_ = os.Remove(portScanFilter)
		_, _ = run(ctx, shortTimeout, "fail2ban-client", "reload")
		return nil, opErr(OpPortScanSet, "fail2ban hat die regel abgelehnt und sie wurde "+
			"zurückgenommen: %s", truncate(out, 300))
	}

	return TextResult{Text: "port-scan-erkennung aktiv (" + string(level) + ", " + logPath + ")"}, nil
}
