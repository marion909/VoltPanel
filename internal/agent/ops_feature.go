package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// Nachinstallieren, was das Panel verwaltet.
//
// Das Panel meldete an einem halben Dutzend Stellen "X ist auf diesem Server
// nicht installiert" — und bot nichts an. Wer das liest, greift zur Shell, und
// genau dafür ist das Panel nicht da.
//
// Was hier *nicht* steht, ist ein Feld für einen Paketnamen. Der Aufrufer nennt
// eine Fähigkeit aus einer festen Liste; welche Pakete dazugehören, entscheidet
// diese Datei. Ein Paketname über die Leitung wäre `apt-get install` mit
// fremder Eingabe — und apt kennt Pakete, die Postinst-Skripte als root
// ausführen. Das ist kein Nachinstallieren mehr, das ist eine Rootshell.

// featurePakete ist die ganze Liste. Sie ist absichtlich kurz: hier gehört nur
// hinein, was das Panel danach auch verwaltet.
var featurePakete = map[string][]string{
	"docker":   {"docker.io", "docker-cli"},
	"fail2ban": {"fail2ban"},
	// Postfix zieht bei der Installation einen debconf-Dialog hoch; aptInstall
	// setzt DEBIAN_FRONTEND=noninteractive, deshalb geht es durch. Die
	// Grundeinstellung danach macht `mail.setup`.
	"postfix": {"postfix"},
	// dovecot-lmtpd fehlte hier bis Debian 13/Dovecot 2.4 — davor lieferte
	// dovecot-core das LMTP-Binary noch mit. Ohne das Paket startet der
	// lmtp-Dienst nicht ("access(/usr/lib/dovecot/lmtp) failed: No such
	// file or directory"), und mail.setup stellt die Zustellung trotzdem auf
	// LMTP um — Mail bliebe dann in der Postfix-Warteschlange stecken, ohne
	// dass eine der beiden Seiten das laut sagt. Auf einem echten Server so
	// gefunden.
	"dovecot":  {"dovecot-core", "dovecot-imapd", "dovecot-lmtpd"},
	"opendkim": {"opendkim", "opendkim-tools"},
	"rspamd":   {"rspamd"},
	// Redis: Phase 7, der erste Eintrag im Plugin-Katalog (internal/core/plugins.go).
	// Bindet standardmäßig nur an 127.0.0.1 — das bringt das Debian-Paket schon
	// so mit, das Panel ändert daran nichts.
	"redis": {"redis-server"},
	// Node kommt nicht aus apt: das Panel führt eigene Fassungen unter
	// /opt/volt/node. Ein Paket daneben wäre eine zweite Wahrheit.
}

// featureDienste sind Dienste, die nach der Paketinstallation sofort laufen
// müssen, damit die Oberfläche nicht weiter "installiert, aber unbenutzbar"
// meldet. apt darf Dienste während der Installation nicht selbst starten; das
// macht der Agent danach gezielt.
var featureDienste = map[string][]string{
	"docker": {"docker"},
	"redis":  {"redis-server"},
}

// FeatureNames sind die Fähigkeiten, die sich nachinstallieren lassen.
func FeatureNames() []string {
	out := make([]string, 0, len(featurePakete))
	for name := range featurePakete {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ValidFeature sagt, ob eine Fähigkeit bekannt ist.
func ValidFeature(name string) bool {
	_, ok := featurePakete[name]
	return ok
}

// opFeatureInstall installiert die Pakete einer Fähigkeit.
func (s *Server) opFeatureInstall(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Feature string `json:"feature"`
	}](raw, OpFeatureInstall)
	if err != nil {
		return nil, err
	}
	pakete, ok := featurePakete[p.Feature]
	if !ok {
		return nil, opInputErr(OpFeatureInstall,
			"%q ist keine bekannte fähigkeit — bekannt sind: %s",
			p.Feature, strings.Join(FeatureNames(), ", "))
	}

	out, err := s.featurePaketeInstallieren(ctx, p.Feature, pakete)
	if err != nil {
		return nil, opErr(OpFeatureInstall, "%s installieren: %s",
			strings.Join(pakete, ", "), truncate(out, 500))
	}
	if err := s.featureDiensteStarten(ctx, p.Feature); err != nil {
		return nil, err
	}
	if err := s.featureNachpruefen(ctx, p.Feature); err != nil {
		return nil, err
	}
	return TextResult{Text: strings.Join(pakete, ", ") + " installiert"}, nil
}

// opFeatureUninstall entfernt die Pakete einer Fähigkeit wieder.
//
// Dieselbe Liste wie beim Installieren, nie ein Paketname aus der Anfrage —
// wer einen Dienst über diesen Weg loswerden darf, darf damit noch lange
// nicht apt-get purge mit beliebigem Namen aufrufen. Systemd-Units und
// Konfigurationsdateien des Pakets gehen mit; eigene Daten des Dienstes
// (bei Redis etwa der Inhalt von /var/lib/redis) bleiben stehen — das ist
// die Vorsicht, die auch beim Löschen eines Mandanten gilt: ein Schritt
// weniger ist besser als ein Datenverlust, den niemand angefordert hat.
func (s *Server) opFeatureUninstall(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Feature string `json:"feature"`
	}](raw, OpFeatureUninstall)
	if err != nil {
		return nil, err
	}
	pakete, ok := featurePakete[p.Feature]
	if !ok {
		return nil, opInputErr(OpFeatureUninstall,
			"%q ist keine bekannte fähigkeit — bekannt sind: %s",
			p.Feature, strings.Join(FeatureNames(), ", "))
	}

	out, err := s.aptPurge(ctx, pakete...)
	if err != nil {
		return nil, opErr(OpFeatureUninstall, "%s entfernen: %s",
			strings.Join(pakete, ", "), truncate(out, 500))
	}
	return TextResult{Text: strings.Join(pakete, ", ") + " entfernt"}, nil
}

func (s *Server) featurePaketeInstallieren(ctx context.Context, feature string, pakete []string) (string, error) {
	if feature == "docker" && dockerCLIPaketOhneBinary(ctx) {
		s.log.Warn("docker-cli ist installiert, aber docker-binary fehlt — paket wird neu installiert")
		return s.aptReinstall(ctx, pakete...)
	}
	return s.aptInstall(ctx, pakete...)
}

func (s *Server) featureDiensteStarten(ctx context.Context, feature string) error {
	for _, dienst := range featureDienste[feature] {
		if err := checkService(dienst); err != nil {
			return err
		}
		if out, err := run(ctx, longTimeout, "systemctl", "enable", dienst); err != nil {
			return opErr(OpFeatureInstall, "%s aktivieren: %s", dienst, truncate(out, 300))
		}
		if err := s.startService(ctx, OpFeatureInstall, dienst, "start"); err != nil {
			return err
		}
	}
	return nil
}

func dockerCLIPaketOhneBinary(ctx context.Context) bool {
	return packageInstalled(ctx, "docker-cli") && !fileExists(allowedBinaries["docker"])
}

func (s *Server) featureNachpruefen(ctx context.Context, feature string) error {
	if feature != "docker" {
		return nil
	}
	out, err := s.opDockerStatus(ctx, nil)
	if err != nil {
		return err
	}
	status, ok := out.(DockerStatus)
	if !ok {
		return opErr(OpFeatureInstall, "docker-status nicht lesbar")
	}
	return dockerFeatureBereit(status)
}

func dockerFeatureBereit(status DockerStatus) error {
	if !status.Installed {
		return opErr(OpFeatureInstall,
			"docker.io wurde installiert, aber /usr/bin/docker ist danach nicht vorhanden")
	}
	if status.Available {
		return nil
	}
	grund := strings.Join(status.Warnings, " ")
	if strings.TrimSpace(grund) == "" {
		grund = "der Docker-Daemon antwortet nach der Installation nicht"
	}
	return opErr(OpFeatureInstall, "docker installiert, aber nicht startklar: %s", grund)
}
